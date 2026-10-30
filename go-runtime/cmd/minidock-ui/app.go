package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type appScreen int

const (
	screenHome appScreen = iota
	screenImages
	screenContainers
	screenAIBuild
	screenBuildProgress
	screenDetails
	screenLogs
)

type containerView struct {
	ID            string   `json:"id"`
	Image         string   `json:"image"`
	Command       []string `json:"command"`
	NetworkIP     string   `json:"networkIp"`
	ContainerPort *int     `json:"containerPort"`
	PublishedPort *int     `json:"publishedPort"`
	PortReady     bool     `json:"portReady"`
	PID           *int     `json:"pid"`
	Status        string   `json:"status"`
	ExitCode      *int     `json:"exitCode"`
	Error         string   `json:"error"`
}

type overviewMsg struct {
	images     []string
	containers []containerView
	err        error
}

type appResultMsg struct {
	action    string
	id        string
	container *containerView
	logs      string
	err       error
}

type aiBuildReply struct {
	Kind          string        `json:"kind"`
	Question      string        `json:"question"`
	Message       string        `json:"message"`
	Summary       string        `json:"summary"`
	ImageName     string        `json:"image_name"`
	ProjectDir    string        `json:"project_dir"`
	Options       []buildOption `json:"options"`
	Image         string        `json:"image"`
	Command       []string      `json:"command"`
	ContainerPort *int          `json:"container_port"`
}

type aiRunPlan struct {
	Image         string
	Command       []string
	ContainerPort *int
}

type aiTurnMsg struct {
	reply aiBuildReply
	err   error
}

type aiBuildMsg struct {
	path string
	err  error
}

type buildEvent struct {
	line string
	path string
	err  error
	done bool
}

type buildSession struct {
	events chan buildEvent
	cancel context.CancelFunc
	ctx    context.Context
}

type aiBuildStartedMsg struct{ session *buildSession }

type aiBuildPollMsg struct{ session *buildSession }

type chatMessage struct {
	role    string
	content string
}

type refreshTick time.Time

type appModel struct {
	apiURL         string
	screen         appScreen
	images         []string
	containers     []containerView
	selected       int
	pendingStartID string
	width          int
	height         int
	online         bool
	loading        bool
	busy           bool
	confirmStop    bool
	confirmDelete  bool
	deleteID       string
	notice         string
	errText        string
	aiFields       [2]string
	aiFocus        int
	aiMessages     []chatMessage
	aiPlan         *buildPlan
	aiRunPlan      *aiRunPlan
	aiImageName    string
	aiSelected     int
	aiPending      bool
	aiConfirm      bool
	aiRunConfirm   bool
	buildSession   *buildSession
	buildLogs      []string
	buildDone      bool
	buildErr       string
	buildPath      string
	nextSelected   int
	detail         *containerView
	logs           string
	logOffset      int
}

func runApp(apiURL string) error {
	if apiURL == "" {
		apiURL = os.Getenv("MINIDOCK_API_URL")
	}
	if apiURL == "" {
		apiURL = "http://127.0.0.1:8080"
	}
	model := appModel{apiURL: strings.TrimRight(apiURL, "/"), screen: screenHome, loading: true}
	final, err := tea.NewProgram(model).Run()
	if finished, ok := final.(appModel); ok && finished.buildSession != nil && !finished.buildDone {
		finished.buildSession.cancel()
	}
	if errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
}

func (m appModel) Init() tea.Cmd {
	return tea.Batch(m.fetchOverview(), tickRefresh())
}

func (m appModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
	case overviewMsg:
		m.loading = false
		m.online = message.err == nil
		if message.err != nil {
			m.errText = message.err.Error()
		} else {
			m.images, m.containers = message.images, message.containers
			m.errText = ""
			if m.pendingStartID != "" {
				for _, container := range m.containers {
					if container.ID != m.pendingStartID {
						continue
					}
					if strings.EqualFold(container.Status, "RUNNING") {
						m.notice = "Container " + container.ID + " is RUNNING"
						if container.ContainerPort != nil && container.PublishedPort != nil {
							if container.PortReady {
								m.notice += fmt.Sprintf(" · App port %d → localhost:%d · Open http://127.0.0.1:%d",
									*container.ContainerPort, *container.PublishedPort, *container.PublishedPort)
								m.pendingStartID = ""
							} else {
								m.notice += fmt.Sprintf(" · App port %d → localhost:%d (waiting for HTTP response)",
									*container.ContainerPort, *container.PublishedPort)
								break
							}
						} else {
							m.notice += " · no app port is configured in this image"
							m.pendingStartID = ""
						}
					} else if isActiveStatus(container.Status) {
						m.notice = "Container " + container.ID + " is " + strings.ToUpper(container.Status)
						break
					} else {
						m.notice = "Container " + container.ID + " is no longer running (" + strings.ToUpper(container.Status)
						if container.ExitCode != nil {
							m.notice += fmt.Sprintf(", exit code %d", *container.ExitCode)
						}
						m.notice += "). Open Containers, select it, then press Enter for details or l for logs."
					}
					if !strings.EqualFold(container.Status, "RUNNING") {
						m.pendingStartID = ""
					}
					break
				}
			}
			m.clampSelection()
		}
	case appResultMsg:
		m.busy, m.confirmStop, m.confirmDelete = false, false, false
		if message.err != nil {
			m.errText = message.err.Error()
			return m, nil
		}
		m.errText = ""
		switch message.action {
		case "created":
			m.screen = screenHome
			m.pendingStartID = message.container.ID
			m.notice = "Checking container " + message.container.ID + " startup…"
			return m, m.fetchOverview()
		case "stopped":
			m.notice = "Container stopped: " + message.container.ID
			if m.screen == screenDetails {
				m.detail = message.container
			}
			return m, m.fetchOverview()
		case "deleted":
			m.screen, m.detail = screenContainers, nil
			m.notice = "Container permanently deleted: " + message.id
			return m, m.fetchOverview()
		case "details":
			m.detail, m.screen = message.container, screenDetails
		case "logs":
			m.logs, m.logOffset, m.screen = message.logs, 0, screenLogs
		}
	case aiTurnMsg:
		m.aiPending, m.busy = false, false
		if message.err != nil {
			m.errText = message.err.Error()
			if len(m.aiMessages) > 0 && m.aiMessages[len(m.aiMessages)-1].role == "user" {
				last := m.aiMessages[len(m.aiMessages)-1]
				m.aiFields[1] = last.content
				m.aiMessages = m.aiMessages[:len(m.aiMessages)-1]
			}
			return m, nil
		}
		m.errText = ""
		if message.reply.ProjectDir != "" {
			m.aiFields[0] = message.reply.ProjectDir
		}
		if message.reply.Kind == "question" {
			m.aiMessages = append(m.aiMessages, chatMessage{role: "assistant", content: message.reply.Question})
			m.aiFocus = 1
		} else if message.reply.Kind == "message" {
			m.aiMessages = append(m.aiMessages, chatMessage{role: "assistant", content: message.reply.Message})
			m.aiFocus = 1
		} else if message.reply.Kind == "plan" {
			m.aiMessages = append(m.aiMessages, chatMessage{role: "assistant", content: message.reply.Summary})
			m.aiPlan = &buildPlan{Summary: message.reply.Summary, Options: message.reply.Options}
			m.aiImageName = message.reply.ImageName
			m.aiSelected = 0
			m.aiFocus = 0
		} else if message.reply.Kind == "run" {
			m.aiRunPlan = &aiRunPlan{Image: message.reply.Image, Command: message.reply.Command,
				ContainerPort: message.reply.ContainerPort}
			m.aiMessages = append(m.aiMessages, chatMessage{role: "assistant", content: "I found a staged image and a startup command. Review the launch details below."})
			m.aiRunConfirm = true
			m.aiFocus = 0
		}
	case aiBuildMsg:
		m.busy, m.aiConfirm = false, false
		if message.err != nil {
			m.errText = "Image build failed: " + message.err.Error()
			return m, nil
		}
		m.screen = screenHome
		m.errText = ""
		m.notice = "Image built and staged as " + m.aiImageName
		if message.path != "" {
			m.notice += " at " + message.path
		}
		m.notice += ". Images refreshed."
		m.aiPlan = nil
		return m, m.fetchOverview()
	case aiBuildStartedMsg:
		m.buildSession = message.session
		m.buildLogs = nil
		m.buildDone, m.buildErr, m.buildPath = false, "", ""
		m.nextSelected, m.busy = 0, true
		m.screen = screenBuildProgress
		return m, pollBuild(message.session)
	case aiBuildPollMsg:
		if message.session != m.buildSession {
			return m, nil
		}
		for {
			select {
			case event, open := <-message.session.events:
				if !open {
					return m, nil
				}
				if event.line != "" && !isStagedImageJSON(event.line) {
					m.buildLogs = append(m.buildLogs, event.line)
					if len(m.buildLogs) > 300 {
						m.buildLogs = m.buildLogs[len(m.buildLogs)-300:]
					}
				}
				if event.done {
					message.session.cancel()
					m.buildDone, m.busy = true, false
					m.buildPath = event.path
					if event.err != nil {
						m.buildErr = event.err.Error()
					}
					return m, m.fetchOverview()
				}
			default:
				return m, pollBuild(message.session)
			}
		}
	case refreshTick:
		return m, tea.Batch(m.fetchOverview(), tickRefresh())
	case tea.KeyMsg:
		return m.handleKey(message)
	}
	return m, nil
}

func (m appModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	name := key.String()
	if m.confirmStop {
		switch name {
		case "y", "Y", "enter":
			if m.detail != nil {
				m.busy = true
				return m, m.stopContainer(m.detail.ID)
			}
			if len(m.containers) > 0 && m.selected < len(m.containers) {
				m.busy = true
				return m, m.stopContainer(m.containers[m.selected].ID)
			}
		case "n", "N", "esc", "q":
			m.confirmStop = false
		}
		return m, nil
	}
	if m.confirmDelete {
		switch name {
		case "y", "Y", "enter":
			m.busy = true
			return m, m.deleteContainer(m.deleteID)
		case "n", "N", "esc":
			m.confirmDelete = false
		}
		return m, nil
	}
	if m.screen == screenAIBuild {
		return m.handleAIBuildKey(key)
	}
	if m.screen == screenBuildProgress {
		return m.handleBuildProgressKey(key)
	}
	if m.screen == screenLogs {
		lines := strings.Split(m.logs, "\n")
		switch name {
		case "esc", "backspace", "q":
			m.screen = screenDetails
		case "up", "k":
			if m.logOffset > 0 {
				m.logOffset--
			}
		case "down", "j":
			if m.logOffset+1 < len(lines) {
				m.logOffset++
			}
		}
		return m, nil
	}

	switch m.screen {
	case screenHome:
		switch name {
		case "up", "k":
			m.selected = (m.selected - 1 + 6) % 6
		case "down", "j":
			m.selected = (m.selected + 1) % 6
		case "i":
			m.screen, m.selected = screenImages, 0
		case "c":
			m.screen, m.selected = screenContainers, 0
		case "n":
			m.screen, m.selected = screenImages, 0
		case "b":
			m.openAIBuildForm()
		case "r":
			m.loading = true
			return m, m.fetchOverview()
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			switch m.selected {
			case 0:
				m.screen, m.selected = screenContainers, 0
			case 1:
				m.screen, m.selected = screenImages, 0
			case 2:
				m.screen, m.selected = screenImages, 0
			case 3:
				m.openAIBuildForm()
			case 4:
				m.loading = true
				return m, m.fetchOverview()
			case 5:
				return m, tea.Quit
			}
		}
	case screenImages:
		switch name {
		case "esc", "q":
			m.screen, m.selected = screenHome, 0
		case "up", "k":
			if len(m.images) > 0 {
				m.selected = (m.selected - 1 + len(m.images)) % len(m.images)
			}
		case "down", "j":
			if len(m.images) > 0 {
				m.selected = (m.selected + 1) % len(m.images)
			}
		case "enter":
			if len(m.images) > 0 {
				m.busy = true
				m.errText = ""
				return m, m.startImage(m.images[m.selected])
			}
		case "n":
			if len(m.images) > 0 {
				m.busy = true
				m.errText = ""
				return m, m.startImage(m.images[m.selected])
			}
		case "r":
			m.loading = true
			return m, m.fetchOverview()
		}
	case screenContainers:
		switch name {
		case "esc", "q":
			m.screen, m.selected = screenHome, 0
		case "up", "k":
			if len(m.containers) > 0 {
				m.selected = (m.selected - 1 + len(m.containers)) % len(m.containers)
			}
		case "down", "j":
			if len(m.containers) > 0 {
				m.selected = (m.selected + 1) % len(m.containers)
			}
		case "enter":
			if len(m.containers) > 0 {
				m.busy = true
				return m, m.fetchDetails(m.containers[m.selected].ID)
			}
		case "s":
			if len(m.containers) > 0 {
				if isActiveStatus(m.containers[m.selected].Status) {
					m.confirmStop = true
				} else {
					m.errText = "This container has already stopped. Use d to delete it permanently."
				}
			}
		case "d":
			if len(m.containers) > 0 {
				m.prepareDelete(m.containers[m.selected])
			}
		case "r":
			m.loading = true
			return m, m.fetchOverview()
		case "n":
			m.screen, m.selected = screenImages, 0
		}
	case screenDetails:
		switch name {
		case "esc", "q":
			m.screen, m.detail = screenContainers, nil
		case "s":
			if m.detail != nil && isActiveStatus(m.detail.Status) {
				m.confirmStop = true
			} else {
				m.errText = "This container has already stopped. Use d to delete it permanently."
			}
		case "d":
			if m.detail != nil {
				m.prepareDelete(*m.detail)
			}
		case "l":
			if m.detail != nil {
				m.busy = true
				return m, m.fetchLogs(m.detail.ID)
			}
		case "r":
			if m.detail != nil {
				m.busy = true
				return m, m.fetchDetails(m.detail.ID)
			}
		}
	}
	return m, nil
}

func isActiveStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PREPARING", "RUNNING", "STOPPING":
		return true
	default:
		return false
	}
}

func (m appModel) runningContainerCount() int {
	count := 0
	for _, container := range m.containers {
		if strings.EqualFold(container.Status, "RUNNING") {
			count++
		}
	}
	return count
}

func isDeletableStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "EXITED", "STOPPED", "FAILED":
		return true
	default:
		return false
	}
}

func (m *appModel) prepareDelete(container containerView) {
	if !isDeletableStatus(container.Status) {
		m.errText = "Stop the container and wait for it to finish before deleting it permanently."
		return
	}
	m.errText = ""
	m.deleteID = container.ID
	m.confirmDelete = true
}

func (m appModel) handleBuildProgressKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.buildDone {
		return m, nil
	}
	options := m.nextBuildOptions()
	switch key.String() {
	case "up", "k":
		m.nextSelected = (m.nextSelected - 1 + len(options)) % len(options)
	case "down", "j":
		m.nextSelected = (m.nextSelected + 1) % len(options)
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.screen = screenHome
	case "enter":
		switch options[m.nextSelected] {
		case "Run this image":
			m.busy = true
			m.notice = "Starting image " + m.aiImageName + "…"
			return m, m.startImage(m.aiImageName)
		case "Build another image":
			m.openAIBuildForm()
		case "View images":
			m.screen, m.selected = screenImages, 0
			m.loading = true
			return m, m.fetchOverview()
		case "Back to dashboard":
			m.screen = screenHome
		case "Exit MiniDock":
			return m, tea.Quit
		case "Return to AI chat":
			m.openAIBuildForm()
		}
	}
	return m, nil
}

func (m appModel) nextBuildOptions() []string {
	if m.buildErr != "" {
		return []string{"Return to AI chat", "Back to dashboard", "Exit MiniDock"}
	}
	return []string{"Run this image", "Build another image", "View images", "Back to dashboard", "Exit MiniDock"}
}

func isStagedImageJSON(line string) bool {
	if !json.Valid([]byte(line)) {
		return false
	}
	var result map[string]any
	return json.Unmarshal([]byte(line), &result) == nil && result["path"] != nil
}

func (m appModel) handleAIBuildKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy && !m.aiPending {
		return m, nil
	}
	if m.aiRunConfirm {
		switch key.String() {
		case "y", "Y", "enter":
			m.aiRunConfirm, m.busy = false, true
			return m, m.createAIPlannedContainer()
		case "n", "N", "esc":
			m.aiRunConfirm, m.aiRunPlan = false, nil
			m.aiFocus = 1
		}
		return m, nil
	}
	if m.aiConfirm {
		switch key.String() {
		case "y", "Y", "enter":
			m.aiConfirm, m.busy = false, true
			return m, m.buildAIImage()
		case "n", "N", "esc":
			m.aiConfirm = false
		}
		return m, nil
	}
	if m.aiPlan != nil && m.aiFocus == 0 {
		switch key.String() {
		case "up", "k":
			if len(m.aiPlan.Options) > 0 {
				m.aiSelected = (m.aiSelected - 1 + len(m.aiPlan.Options)) % len(m.aiPlan.Options)
			}
			return m, nil
		case "down", "j":
			if len(m.aiPlan.Options) > 0 {
				m.aiSelected = (m.aiSelected + 1) % len(m.aiPlan.Options)
			}
			return m, nil
		case "enter":
			if len(m.aiPlan.Options) > 0 {
				m.aiConfirm = true
			}
			return m, nil
		case "tab":
			m.aiFocus = 1
			return m, nil
		}
	}
	switch key.String() {
	case "esc":
		m.screen, m.errText = screenHome, ""
	case "tab", "down":
		m.aiFocus = (m.aiFocus + 1) % 2
	case "shift+tab", "up":
		m.aiFocus = (m.aiFocus - 1 + 2) % 2
	case "ctrl+s":
		return m.submitAIChat()
	case "enter":
		if m.aiFocus == 0 {
			m.aiFocus = 1
			return m, nil
		}
		return m.submitAIChat()
	case "backspace", "delete":
		value := []rune(m.aiFields[m.aiFocus])
		if len(value) > 0 {
			m.aiFields[m.aiFocus] = string(value[:len(value)-1])
		}
	default:
		if key.Type == tea.KeyRunes && !m.aiPending {
			m.aiFields[m.aiFocus] += string(key.Runes)
		} else if key.Type == tea.KeySpace && !m.aiPending {
			m.aiFields[m.aiFocus] += " "
		}
	}
	return m, nil
}

func (m *appModel) openAIBuildForm() {
	m.screen, m.aiFields, m.aiFocus = screenAIBuild, [2]string{}, 0
	m.aiFocus = 1
	m.aiMessages, m.aiPlan, m.aiImageName = nil, nil, ""
	m.aiRunPlan, m.aiRunConfirm = nil, false
	m.aiSelected, m.aiPending, m.aiConfirm = 0, false, false
	m.errText, m.notice = "", ""
}

func (m appModel) submitAIChat() (tea.Model, tea.Cmd) {
	if m.aiPending {
		return m, nil
	}
	if strings.TrimSpace(m.aiFields[1]) == "" {
		m.errText = "Type what you want help building in the chat box."
		return m, nil
	}
	message := strings.TrimSpace(m.aiFields[1])
	if len([]rune(message)) > 2000 {
		m.errText = "Keep each message to 2,000 characters or less."
		return m, nil
	}
	m.aiFields[1] = ""
	m.aiMessages = append(m.aiMessages, chatMessage{role: "user", content: message})
	m.aiPlan, m.aiRunPlan, m.errText = nil, nil, ""
	m.aiPending, m.busy = true, true
	return m, m.requestAITurn()
}

func (m appModel) requestAITurn() tea.Cmd {
	repository := strings.TrimSpace(os.Getenv("MINIDOCK_REPO"))
	if repository == "" {
		if executable, err := os.Executable(); err == nil {
			repository = filepath.Dir(filepath.Dir(executable))
		}
	}
	messages := make([]map[string]string, 0, len(m.aiMessages))
	for _, message := range m.aiMessages {
		messages = append(messages, map[string]string{"role": message.role, "content": message.content})
	}
	images := m.images
	if images == nil {
		images = []string{}
	}
	payload, _ := json.Marshal(map[string]any{"messages": messages, "images": images})
	projectDir := strings.TrimSpace(m.aiFields[0])
	if projectDir == "" {
		projectDir = "."
	}
	command := exec.Command(filepath.Join(repository, "bin", "minidock"), "ask-build-turn",
		projectDir, "")
	command.Env = replaceEnv(os.Environ(), "MINIDOCK_API_URL", m.apiURL)
	command.Env = replaceEnv(command.Env, "MINIDOCK_REPO", repository)
	command.Stdin = bytes.NewReader(payload)
	return func() tea.Msg {
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			message := strings.TrimSpace(stderr.String())
			if message == "" {
				message = err.Error()
			}
			return aiTurnMsg{err: errors.New(message)}
		}
		var reply aiBuildReply
		if err := json.Unmarshal(output, &reply); err != nil {
			return aiTurnMsg{err: fmt.Errorf("invalid AI response: %w", err)}
		}
		if reply.Kind == "question" && strings.TrimSpace(reply.Question) != "" ||
			reply.Kind == "message" && strings.TrimSpace(reply.Message) != "" ||
			reply.Kind == "plan" && len(reply.Options) > 0 && reply.ImageName != "" && reply.ProjectDir != "" {
			return aiTurnMsg{reply: reply}
		}
		if reply.Kind != "question" && reply.Kind != "message" && reply.Kind != "plan" {
			return aiTurnMsg{err: errors.New("AI response was missing a question or supported build plan")}
		}
		return aiTurnMsg{err: errors.New("AI returned an empty response or unsupported build plan")}
	}
}

func (m appModel) buildAIImage() tea.Cmd {
	repository := strings.TrimSpace(os.Getenv("MINIDOCK_REPO"))
	if repository == "" {
		if executable, err := os.Executable(); err == nil {
			repository = filepath.Dir(filepath.Dir(executable))
		}
	}
	option := m.aiPlan.Options[m.aiSelected]
	projectDir := strings.TrimSpace(m.aiFields[0])
	if projectDir == "" {
		projectDir = "."
	}
	args := []string{"build", "-t", m.aiImageName, projectDir}
	switch option.ID {
	case "build", "go-build", "maven-build":
	case "package-existing-dist":
		args = []string{"build", "--from-dist", "-t", m.aiImageName, projectDir}
	case "package-existing-jar":
		args = []string{"build", "--from-jar", "-t", m.aiImageName, projectDir}
	default:
		return func() tea.Msg { return aiBuildMsg{err: errors.New("selected recipe is not supported")} }
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, filepath.Join(repository, "bin", "minidock"), args...)
	command.Env = replaceEnv(os.Environ(), "MINIDOCK_API_URL", m.apiURL)
	command.Env = replaceEnv(command.Env, "MINIDOCK_REPO", repository)
	return func() tea.Msg {
		stdout, err := command.StdoutPipe()
		if err != nil {
			cancel()
			return aiBuildMsg{err: err}
		}
		stderr, err := command.StderrPipe()
		if err != nil {
			cancel()
			return aiBuildMsg{err: err}
		}
		session := &buildSession{events: make(chan buildEvent, 512), cancel: cancel, ctx: ctx}
		if err := command.Start(); err != nil {
			cancel()
			return aiBuildMsg{err: err}
		}
		var stdoutData bytes.Buffer
		var readers sync.WaitGroup
		readers.Add(2)
		go streamBuildOutput(stdout, session, &stdoutData, &readers)
		go streamBuildOutput(stderr, session, nil, &readers)
		go func() {
			readers.Wait()
			waitErr := command.Wait()
			var staged struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(stdoutData.Bytes(), &staged)
			select {
			case session.events <- buildEvent{path: staged.Path, err: waitErr, done: true}:
			case <-session.ctx.Done():
			}
			close(session.events)
		}()
		return aiBuildStartedMsg{session: session}
	}
}

func streamBuildOutput(reader io.Reader, session *buildSession, captured *bytes.Buffer, readers *sync.WaitGroup) {
	defer readers.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if captured != nil {
			captured.WriteString(line)
			captured.WriteByte('\n')
		}
		select {
		case session.events <- buildEvent{line: line}:
		case <-sessionDone(session):
			return
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case session.events <- buildEvent{line: "output reader: " + err.Error()}:
		case <-sessionDone(session):
		}
	}
}

func sessionDone(session *buildSession) <-chan struct{} {
	return session.ctx.Done()
}

func pollBuild(session *buildSession) tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return aiBuildPollMsg{session: session} })
}

func replaceEnv(environment []string, name, value string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func (m *appModel) clampSelection() {
	count := len(m.images)
	if m.screen == screenContainers || m.screen == screenDetails {
		count = len(m.containers)
	}
	if count == 0 {
		m.selected = 0
	} else if m.selected >= count {
		m.selected = count - 1
	}
}

func (m appModel) fetchOverview() tea.Cmd {
	return func() tea.Msg {
		var images []string
		if err := apiRequest(m.apiURL, http.MethodGet, "/api/v1/images", nil, &images); err != nil {
			return overviewMsg{err: err}
		}
		var containers []containerView
		if err := apiRequest(m.apiURL, http.MethodGet, "/api/v1/containers?all=true", nil, &containers); err != nil {
			return overviewMsg{err: err}
		}
		return overviewMsg{images: images, containers: containers}
	}
}

func tickRefresh() tea.Cmd {
	return tea.Tick(5*time.Second, func(now time.Time) tea.Msg { return refreshTick(now) })
}

func (m appModel) startImage(image string) tea.Cmd {
	request := map[string]any{"image": image, "network": true, "attach": false, "tty": false}
	return func() tea.Msg {
		var created containerView
		if err := apiRequest(m.apiURL, http.MethodPost, "/api/v1/containers", request, &created); err != nil {
			return appResultMsg{action: "created", err: err}
		}
		return appResultMsg{action: "created", container: &created}
	}
}

func (m appModel) createAIPlannedContainer() tea.Cmd {
	if m.aiRunPlan == nil {
		return func() tea.Msg {
			return appResultMsg{action: "created", err: errors.New("there is no reviewed run plan")}
		}
	}
	plan := *m.aiRunPlan
	request := map[string]any{
		"image": plan.Image, "command": plan.Command,
		"network": true, "attach": false, "tty": false,
	}
	if plan.ContainerPort != nil {
		request["container_port"] = *plan.ContainerPort
	}
	return func() tea.Msg {
		var created containerView
		if err := apiRequest(m.apiURL, http.MethodPost, "/api/v1/containers", request, &created); err != nil {
			return appResultMsg{action: "created", err: err}
		}
		return appResultMsg{action: "created", container: &created}
	}
}

func (m appModel) stopContainer(id string) tea.Cmd {
	return func() tea.Msg {
		var stopped containerView
		path := "/api/v1/containers/" + url.PathEscape(id)
		if err := apiRequest(m.apiURL, http.MethodDelete, path, nil, &stopped); err != nil {
			return appResultMsg{action: "stopped", err: err}
		}
		return appResultMsg{action: "stopped", container: &stopped}
	}
}

func (m appModel) deleteContainer(id string) tea.Cmd {
	path := "/api/v1/containers/" + url.PathEscape(id) + "/record"
	return func() tea.Msg {
		if err := apiRequest(m.apiURL, http.MethodDelete, path, nil, nil); err != nil {
			return appResultMsg{action: "deleted", id: id, err: err}
		}
		return appResultMsg{action: "deleted", id: id}
	}
}

func (m appModel) fetchDetails(id string) tea.Cmd {
	return func() tea.Msg {
		var detail containerView
		if err := apiRequest(m.apiURL, http.MethodGet, "/api/v1/containers/"+url.PathEscape(id), nil, &detail); err != nil {
			return appResultMsg{action: "details", err: err}
		}
		return appResultMsg{action: "details", container: &detail}
	}
}

func (m appModel) fetchLogs(id string) tea.Cmd {
	return func() tea.Msg {
		path := "/api/v1/containers/" + url.PathEscape(id) + "/logs?offset=0&limit=65536"
		request, err := http.NewRequest(http.MethodGet, m.apiURL+path, nil)
		if err != nil {
			return appResultMsg{action: "logs", err: err}
		}
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			return appResultMsg{action: "logs", err: err}
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 65536))
		if err != nil {
			return appResultMsg{action: "logs", err: err}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return appResultMsg{action: "logs", err: fmt.Errorf("API %s: %s", response.Status, strings.TrimSpace(string(data)))}
		}
		return appResultMsg{action: "logs", logs: string(data)}
	}
}

func apiRequest(base, method, path string, body any, output any) error {
	var source io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		source = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, base+path, source)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("MiniDock API unavailable: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &problem)
		if problem.Error != "" {
			return errors.New(problem.Error)
		}
		return fmt.Errorf("MiniDock API returned %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	if output == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, output)
}

func (m appModel) View() string {
	var out strings.Builder
	out.WriteString(brandStyle.Render("◆ MINIDOCK"))
	out.WriteString(mutedStyle.Render("  ·  local container workspace"))
	if m.online {
		out.WriteString("   " + goodStyle.Render("● CONNECTED"))
	} else {
		out.WriteString("   " + badStyle.Render("● OFFLINE"))
	}
	out.WriteString("\n")
	out.WriteString(mutedStyle.Render("  " + m.apiURL))
	out.WriteString("\n\n")
	if m.notice != "" {
		out.WriteString(goodStyle.Render("  ✓ "+clean(m.notice)) + "\n\n")
	}
	if m.errText != "" {
		out.WriteString(badStyle.Render("  ! "+clean(m.errText)) + "\n\n")
	}
	if (m.busy && m.screen != screenBuildProgress && m.screen != screenAIBuild) || m.loading {
		out.WriteString(infoStyle.Render("  Working…") + "\n\n")
	}

	switch m.screen {
	case screenHome:
		out.WriteString(lipgloss.NewStyle().Bold(true).Render("  Your MiniDock"))
		out.WriteString("\n\n")
		out.WriteString(fmt.Sprintf("  %s  %s     %s  %s\n\n",
			mutedStyle.Render("Images"), infoStyle.Render(fmt.Sprint(len(m.images))),
			mutedStyle.Render("Running"), goodStyle.Render(fmt.Sprint(m.runningContainerCount()))))
		out.WriteString(fmt.Sprintf("  %s  %s\n\n", mutedStyle.Render("Container records"), infoStyle.Render(fmt.Sprint(len(m.containers)))))
		options := []string{"Containers", "Images", "Run a container", "Build image with AI", "Refresh", "Close dashboard"}
		for index, item := range options {
			marker := "  "
			style := optionStyle
			if index == m.selected {
				marker, style = "▸ ", selectedStyle
			}
			out.WriteString("  " + style.Render(marker+item) + "\n")
		}
		out.WriteString("\n" + mutedStyle.Render("  ↑/↓ navigate · Enter select · i images · c containers · n run · b AI build · r refresh · q close"))
	case screenImages:
		out.WriteString(brandStyle.Render("  SELECT AN IMAGE TO RUN") + "\n")
		out.WriteString(mutedStyle.Render("  Selecting an image starts a new container from it.") + "\n\n")
		if len(m.images) == 0 {
			out.WriteString(mutedStyle.Render("  No images staged yet. Build one with the CLI."))
		}
		for index, image := range m.images {
			marker, style := "  ", optionStyle
			if index == m.selected {
				marker, style = "▸ ", selectedStyle
			}
			out.WriteString("  " + style.Render(marker+image) + "\n")
		}
		out.WriteString("\n" + mutedStyle.Render("  Enter start selected image · r refresh · Esc back"))
	case screenContainers:
		out.WriteString(brandStyle.Render("  CONTAINERS") + "\n\n")
		if len(m.containers) == 0 {
			out.WriteString(mutedStyle.Render("  No containers recorded."))
		}
		for index, container := range m.containers {
			marker, style := "  ", optionStyle
			if index == m.selected {
				marker, style = "▸ ", selectedStyle
			}
			status := statusStyle(container.Status).Render(container.Status)
			pid := "—"
			if container.PID != nil {
				pid = fmt.Sprint(*container.PID)
			}
			line := fmt.Sprintf("  %s%s  %s  %s  pid %s  %s", marker, style.Render(container.ID),
				container.Image, status, pid, container.NetworkIP)
			if container.ContainerPort != nil && container.PublishedPort != nil && isActiveStatus(container.Status) {
				line += fmt.Sprintf("  app :%d → localhost:%d", *container.ContainerPort, *container.PublishedPort)
				if container.PortReady {
					line += fmt.Sprintf("  http://127.0.0.1:%d", *container.PublishedPort)
				} else {
					line += "  (checking HTTP)"
				}
			}
			out.WriteString(line + "\n")
		}
		out.WriteString("\n" + mutedStyle.Render("  Enter details · s stop · d permanently delete · n run · r refresh · Esc back"))
	case screenAIBuild:
		out.WriteString(brandStyle.Render("  BUILD AN IMAGE WITH AI") + "\n")
		out.WriteString(mutedStyle.Render("  Chat with MiniDock about this project. It can explain the app, ask questions, and suggest a supported image build.") + "\n\n")
		pathMarker, pathStyle := "  ", optionStyle
		if m.aiFocus == 0 {
			pathMarker, pathStyle = "▸ ", selectedStyle
		}
		projectPath := strings.TrimSpace(m.aiFields[0])
		if projectPath == "" {
			projectPath = ". (current directory)"
		}
		out.WriteString("  " + pathStyle.Render(pathMarker+"Project folder: "+projectPath) + "\n\n")
		chatWidth := max(32, m.width-8)
		var transcript strings.Builder
		if len(m.aiMessages) == 0 {
			transcript.WriteString(mutedStyle.Render("Tell me what you want to build, or ask me to explain this project."))
		} else {
			start := max(0, len(m.aiMessages)-8)
			userName := strings.TrimSpace(os.Getenv("SUDO_USER"))
			if userName == "" {
				userName = strings.TrimSpace(os.Getenv("USER"))
			}
			if userName == "" {
				userName = "You"
			}
			userName = truncate(clean(userName), 32)
			messageStyle := lipgloss.NewStyle().Width(max(20, chatWidth-4))
			for _, entry := range m.aiMessages[start:] {
				if entry.role == "user" {
					line := userNameStyle.Render(userName+": ") + clean(entry.content)
					transcript.WriteString(messageStyle.Render(line) + "\n\n")
				} else {
					line := aiNameStyle.Render("✦ MiniDock AI: ") + clean(entry.content)
					transcript.WriteString(messageStyle.Render(line) + "\n\n")
				}
			}
		}
		chatStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#48d9ff")).Padding(0, 1).Width(chatWidth)
		out.WriteString(chatStyle.Render(transcript.String()) + "\n\n")
		composerMarker, composerColor := "  ", lipgloss.Color("#94a3b8")
		if m.aiFocus == 1 {
			composerMarker, composerColor = "▸ ", lipgloss.Color("#48d9ff")
		}
		input := m.aiFields[1]
		if input == "" && !m.aiPending {
			input = "Write a message…"
		}
		if m.aiPending {
			input = "MiniDock AI is thinking…"
		}
		composerStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(composerColor).Padding(0, 1).Width(chatWidth)
		out.WriteString(composerStyle.Render(composerMarker+"› "+input) + "\n")
		if m.aiPlan != nil {
			out.WriteString("\n" + mutedStyle.Render("  Suggested image name: ") + infoStyle.Render(m.aiImageName) + "\n")
			for index, option := range m.aiPlan.Options {
				marker, style := "  ", optionStyle
				if index == m.aiSelected {
					marker, style = "▸ ", selectedStyle
				}
				out.WriteString("  " + style.Render(marker+clean(option.Title)) + "\n")
				out.WriteString(mutedStyle.Render("     "+clean(option.Description)) + "\n")
			}
		}
		if m.aiRunPlan != nil {
			out.WriteString("\n" + brandStyle.Render("  LAUNCH PROPOSAL") + "\n")
			out.WriteString("  Image:   " + infoStyle.Render(clean(m.aiRunPlan.Image)) + "\n")
			if m.aiRunPlan.ContainerPort != nil {
				out.WriteString("  App port: " + infoStyle.Render(fmt.Sprint(*m.aiRunPlan.ContainerPort)) + " (MiniDock assigns a localhost address)\n")
			}
			if m.aiRunConfirm {
				out.WriteString("\n" + goodStyle.Render("  Start this container? Press y/Enter to start or n to cancel."))
			} else if m.busy {
				out.WriteString("\n" + infoStyle.Render("  Starting the approved container…"))
			}
		}
		if m.aiConfirm && m.aiPlan != nil && m.aiSelected < len(m.aiPlan.Options) {
			out.WriteString("\n" + goodStyle.Render("  Build "+m.aiImageName+" using "+m.aiPlan.Options[m.aiSelected].Title+"? Press y to confirm or n to cancel."))
		} else if m.aiPlan != nil {
			out.WriteString("\n" + mutedStyle.Render("  ↑/↓ choose recipe · Enter review and confirm · Tab chat again · Esc back"))
		} else {
			out.WriteString("\n" + mutedStyle.Render("  Enter send · Tab project folder · Ctrl+S send · Esc back"))
		}
	case screenBuildProgress:
		if !m.buildDone {
			frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			frame := frames[(time.Now().UnixMilli()/120)%int64(len(frames))]
			out.WriteString(infoStyle.Render("  "+frame+" Building image "+m.aiImageName+"…") + "\n")
			out.WriteString(mutedStyle.Render("  Live build output") + "\n\n")
		} else if m.buildErr != "" {
			out.WriteString(badStyle.Render("  ✗ Image build failed") + "\n")
			out.WriteString(badStyle.Render("  "+clean(m.buildErr)) + "\n\n")
		} else {
			out.WriteString(goodStyle.Render("  ✓ Image built successfully") + "\n")
			if m.buildPath != "" {
				out.WriteString(mutedStyle.Render("  Saved at "+clean(m.buildPath)) + "\n")
			}
			out.WriteString("\n" + brandStyle.Render("  What would you like to do next?") + "\n\n")
		}
		linesToShow := max(1, m.height-13)
		start := max(0, len(m.buildLogs)-linesToShow)
		logWidth := max(24, m.width-4)
		for _, line := range m.buildLogs[start:] {
			out.WriteString("  " + truncate(clean(line), logWidth) + "\n")
		}
		if !m.buildDone && len(m.buildLogs) == 0 {
			out.WriteString(mutedStyle.Render("  Preparing build tools and collecting output…") + "\n")
		}
		if m.buildDone {
			out.WriteString("\n")
			for index, option := range m.nextBuildOptions() {
				marker, style := "  ", optionStyle
				if index == m.nextSelected {
					marker, style = "▸ ", selectedStyle
				}
				out.WriteString("  " + style.Render(marker+option) + "\n")
			}
			out.WriteString("\n" + mutedStyle.Render("  ↑/↓ choose · Enter select · q exit MiniDock"))
		} else {
			out.WriteString("\n" + mutedStyle.Render("  Build in progress… please wait"))
		}
	case screenDetails:
		if m.detail != nil {
			out.WriteString(brandStyle.Render("  CONTAINER DETAILS") + "\n\n")
			out.WriteString(fmt.Sprintf("  %s  %s\n", mutedStyle.Render("ID"), infoStyle.Render(m.detail.ID)))
			out.WriteString(fmt.Sprintf("  %s  %s\n", mutedStyle.Render("Image"), m.detail.Image))
			out.WriteString(fmt.Sprintf("  %s  %s\n", mutedStyle.Render("Status"), statusStyle(m.detail.Status).Render(m.detail.Status)))
			out.WriteString(fmt.Sprintf("  %s  %s\n", mutedStyle.Render("Command"), strings.Join(m.detail.Command, " ")))
			if m.detail.ContainerPort != nil && m.detail.PublishedPort != nil && isActiveStatus(m.detail.Status) {
				out.WriteString(fmt.Sprintf("  %s  container :%d → localhost:%d\n",
					mutedStyle.Render("Port mapping"), *m.detail.ContainerPort, *m.detail.PublishedPort))
				if m.detail.PortReady {
					out.WriteString(fmt.Sprintf("  %s  http://127.0.0.1:%d\n",
						mutedStyle.Render("App URL"), *m.detail.PublishedPort))
				} else {
					out.WriteString(mutedStyle.Render("  Reachability  waiting for an HTTP response\n"))
				}
			}
			pid := "—"
			if m.detail.PID != nil {
				pid = fmt.Sprint(*m.detail.PID)
			}
			out.WriteString(fmt.Sprintf("  %s  %s / %s\n", mutedStyle.Render("PID / IP"), pid, m.detail.NetworkIP))
			if m.detail.Error != "" {
				out.WriteString(badStyle.Render("  Error: "+m.detail.Error) + "\n")
			}
		}
		out.WriteString("\n" + mutedStyle.Render("  l logs · s stop · d permanently delete · r refresh · Esc back"))
	case screenLogs:
		out.WriteString(brandStyle.Render("  CONTAINER LOGS") + "\n\n")
		lines := strings.Split(m.logs, "\n")
		start := m.logOffset
		end := start + max(1, m.height-8)
		if end > len(lines) {
			end = len(lines)
		}
		for _, line := range lines[start:end] {
			out.WriteString(clean(line) + "\n")
		}
		out.WriteString("\n" + mutedStyle.Render("  ↑/↓ scroll · Esc back"))
	}
	if m.confirmStop {
		out.WriteString("\n\n" + badStyle.Render("  Stop this container? Press y to confirm or n to cancel."))
	}
	if m.confirmDelete {
		out.WriteString("\n\n" + badStyle.Render("  Permanently delete container "+m.deleteID+" and its saved files? Press y to confirm or n to cancel."))
	}
	return out.String() + "\n"
}
