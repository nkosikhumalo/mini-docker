package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type buildOption struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Recipe      string `json:"recipe"`
}

type buildPlan struct {
	Summary          string        `json:"summary"`
	RecommendedOrder []string      `json:"recommended_order"`
	Options          []buildOption `json:"options"`
}

type menuModel struct {
	plan     buildPlan
	selected int
	choice   string
	done     bool
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		m.selected = (m.selected - 1 + len(m.plan.Options)) % len(m.plan.Options)
	case "down", "j":
		m.selected = (m.selected + 1) % len(m.plan.Options)
	case "enter":
		m.choice = m.plan.Options[m.selected].ID
		m.done = true
		return m, tea.Quit
	case "q", "esc", "ctrl+c":
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m menuModel) View() string {
	var out strings.Builder
	out.WriteString(brandStyle.Render("◆ MINIDOCK  AI PROJECT ASSESSMENT"))
	out.WriteString("\n\n")
	out.WriteString(mutedStyle.Render(clean(m.plan.Summary)))
	out.WriteString("\n\n")
	out.WriteString(mutedStyle.Render("Choose a build recipe · ↑/↓ move · Enter select · q cancel"))
	out.WriteString("\n")
	for index, option := range m.plan.Options {
		marker := "  "
		if index == m.selected {
			marker = "▸ "
		}
		title := clean(option.Title)
		if index == 0 {
			title += mutedStyle.Render("  AI recommendation")
		}
		if index == m.selected {
			out.WriteString(selectedStyle.Render(marker + title))
		} else {
			out.WriteString(optionStyle.Render(marker + title))
		}
		out.WriteString("\n")
		out.WriteString(mutedStyle.Render("    " + clean(option.Description)))
		out.WriteString("\n\n")
	}
	return out.String()
}

var (
	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#c78bff"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#48d9ff"))
	optionStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f4f7ff"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
	goodStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ade80"))
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#48d9ff"))
	blueStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#789bff"))
	badStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6678"))
	userNameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff9acb"))
	aiNameStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8be9ff"))
)

func main() {
	configureStyles()
	if len(os.Args) < 2 {
		fatal("usage: minidock-ui app [API_URL] | render KIND | select PLAN.json OUTPUT.txt")
	}
	switch os.Args[1] {
	case "app":
		if len(os.Args) > 3 {
			fatal("usage: minidock-ui app [API_URL]")
		}
		apiURL := ""
		if len(os.Args) == 3 {
			apiURL = os.Args[2]
		}
		if err := runApp(apiURL); err != nil {
			fatal(err.Error())
		}
	case "render":
		if len(os.Args) != 3 {
			fatal("usage: minidock-ui render KIND")
		}
		if err := render(os.Args[2], os.Stdin, os.Stdout); err != nil {
			fatal(err.Error())
		}
	case "select":
		if len(os.Args) != 4 {
			fatal("usage: minidock-ui select PLAN.json OUTPUT.txt")
		}
		if err := selectRecipe(os.Args[2], os.Args[3]); err != nil {
			fatal(err.Error())
		}
	default:
		fatal("unknown UI action: " + os.Args[1])
	}
}

func configureStyles() {
	if os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		return
	}
	brandStyle = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Bold(true)
	optionStyle = lipgloss.NewStyle()
	mutedStyle = lipgloss.NewStyle()
	goodStyle = lipgloss.NewStyle().Bold(true)
	infoStyle = lipgloss.NewStyle()
	blueStyle = lipgloss.NewStyle()
	badStyle = lipgloss.NewStyle()
	userNameStyle = lipgloss.NewStyle().Bold(true)
	aiNameStyle = lipgloss.NewStyle().Bold(true)
}

func render(kind string, input io.Reader, output io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(input, 16<<20))
	if err != nil {
		return err
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		_, writeErr := output.Write(raw)
		return writeErr
	}
	if !isTTY(os.Stdout) || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(data)
	}

	switch kind {
	case "build":
		return renderBuild(output, object(data))
	case "images":
		return renderImages(output, data)
	case "containers":
		return renderContainers(output, data)
	case "container":
		return renderContainer(output, object(data), "CONTAINER")
	case "stopped":
		return renderContainer(output, object(data), "CONTAINER STOPPED")
	case "plan":
		return renderPlan(output, object(data))
	default:
		pretty, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, string(pretty))
		return err
	}
}

func selectRecipe(planPath, outputPath string) error {
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var plan buildPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("invalid build plan: %w", err)
	}
	if len(plan.Options) == 0 {
		return errors.New("build plan has no available recipes")
	}
	if !isTTY(os.Stdin) && !isTTY(os.Stderr) {
		return errors.New("interactive build selection requires a terminal")
	}
	model := menuModel{plan: plan}
	result, err := tea.NewProgram(model, tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		return err
	}
	final, ok := result.(menuModel)
	if !ok || !final.done {
		return errors.New("build selection did not complete")
	}
	return os.WriteFile(outputPath, []byte(final.choice), 0600)
}

func renderBuild(output io.Writer, data map[string]any) error {
	if _, err := fmt.Fprintln(output, brandStyle.Render("◆ MINIDOCK  IMAGE BUILT")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, mutedStyle.Render("Your image is staged and ready to use")); err != nil {
		return err
	}
	writeField(output, "Image", data["name"], goodStyle)
	writeField(output, "Archive", data["archive"], infoStyle)
	if path := data["path"]; path != nil {
		writeField(output, "Location", path, optionStyle)
	}
	return nil
}

func renderImages(output io.Writer, data any) error {
	images, _ := data.([]any)
	fmt.Fprintf(output, "%s  %s\n", brandStyle.Render("◆ MINIDOCK  IMAGES"), mutedStyle.Render(fmt.Sprintf("%d image(s) in your local store", len(images))))
	if len(images) == 0 {
		_, err := fmt.Fprintln(output, mutedStyle.Render("  No images yet. Build one with `bin/minidock build`."))
		return err
	}
	for _, image := range images {
		fmt.Fprintf(output, "  %s %s\n", infoStyle.Render("●"), optionStyle.Render(clean(image)))
	}
	return nil
}

func renderContainers(output io.Writer, data any) error {
	containers, _ := data.([]any)
	fmt.Fprintf(output, "%s  %s\n", brandStyle.Render("◆ MINIDOCK  CONTAINERS"), mutedStyle.Render(fmt.Sprintf("%d active container(s)", len(containers))))
	if len(containers) == 0 {
		_, err := fmt.Fprintln(output, mutedStyle.Render("  Nothing running. Start one with `bin/minidock run`."))
		return err
	}
	fmt.Fprintf(output, "  %-12s %-20s %-11s %-8s %-15s %s\n", "ID", "IMAGE", "STATUS", "PID", "IP", "COMMAND")
	for _, value := range containers {
		item := object(value)
		command := strings.Join(stringSlice(item["command"]), " ")
		status := clean(item["status"])
		style := statusStyle(status)
		fmt.Fprintf(output, "  %-12s %-20s %s %-8s %-15s %s\n",
			truncate(clean(item["id"]), 12), truncate(clean(item["image"]), 20),
			style.Render(fmt.Sprintf("%-11s", truncate(status, 11))), truncate(clean(item["pid"]), 8),
			truncate(clean(item["networkIp"]), 15), clean(command))
	}
	return nil
}

func renderContainer(output io.Writer, item map[string]any, title string) error {
	fmt.Fprintf(output, "%s  %s\n", brandStyle.Render("◆ MINIDOCK  "+title), mutedStyle.Render(clean(item["image"])+" · "+clean(item["status"])))
	writeField(output, "ID", item["id"], infoStyle)
	writeField(output, "PID", item["pid"], optionStyle)
	if item["networkIp"] != nil {
		writeField(output, "Network", item["networkIp"], blueStyle)
	}
	if containerPort, hostPort := item["containerPort"], item["publishedPort"]; containerPort != nil && hostPort != nil && clean(item["status"]) == "RUNNING" {
		writeField(output, "Port mapping", fmt.Sprintf("container :%s → localhost:%s", clean(containerPort), clean(hostPort)), blueStyle)
		if ready, _ := item["portReady"].(bool); ready {
			writeField(output, "Open", "http://127.0.0.1:"+clean(hostPort), blueStyle)
		} else {
			writeField(output, "Reachability", "waiting for an HTTP response from the app", mutedStyle)
		}
	} else if clean(item["status"]) == "RUNNING" {
		writeField(output, "Port", "no app port was published for this image", mutedStyle)
	}
	writeField(output, "Command", strings.Join(stringSlice(item["command"]), " "), optionStyle)
	if item["error"] != nil {
		writeField(output, "Error", item["error"], badStyle)
	}
	return nil
}

func renderPlan(output io.Writer, data map[string]any) error {
	fmt.Fprintf(output, "%s\n%s\n", brandStyle.Render("◆ MINIDOCK  AI PROPOSAL"), mutedStyle.Render("Review the generated plan before continuing"))
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := data[key]
		if list, ok := value.([]any); ok {
			parts := make([]string, len(list))
			for index, part := range list {
				parts[index] = clean(part)
			}
			value = strings.Join(parts, " ")
		}
		style := optionStyle
		if key == "image" || key == "builder" {
			style = infoStyle
		}
		writeField(output, strings.Title(strings.ReplaceAll(key, "_", " ")), value, style)
	}
	return nil
}

func writeField(output io.Writer, label string, value any, style lipgloss.Style) {
	fmt.Fprintf(output, "  %s  %s\n", mutedStyle.Render(label), style.Render(clean(value)))
}

func statusStyle(status string) lipgloss.Style {
	switch status {
	case "RUNNING":
		return goodStyle
	case "PREPARING", "STOPPING":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#facc58"))
	case "FAILED":
		return badStyle
	default:
		return mutedStyle
	}
}

func object(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func stringSlice(value any) []string {
	values, _ := value.([]any)
	result := make([]string, len(values))
	for index, part := range values {
		result[index] = clean(part)
	}
	return result
}

func clean(value any) string {
	if value == nil {
		return "—"
	}
	text := fmt.Sprint(value)
	return strings.Map(func(r rune) rune {
		if r < 32 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, text)
}

func truncate(value string, limit int) string {
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit-1]) + "…"
}

func isTTY(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
