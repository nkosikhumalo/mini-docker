//go:build linux

// Package transport exposes the stateless Java-to-Go runtime boundary over
// local gRPC. Java owns the user-visible lifecycle; Go owns kernel execution.
package transport

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtimev1 "mini-docker/go-runtime/gen/runtime/v1"
	"mini-docker/go-runtime/internal/container"
)

const serviceName = "minidock.runtime.v1.RuntimeService"

type Runner func(context.Context, container.Config) error
type ManagedRunner func(context.Context, container.Config, func(container.StartedProcess)) error

// RegisterRuntime exposes the compatibility Run method used by focused tests.
func RegisterRuntime(server *grpc.Server, runner Runner) {
	register(server, &runtimeServer{runner: runner})
}

// RegisterManagedRuntime exposes Start/Wait/Stop. Start returns after the
// child acknowledges namespace, limits, network, and rootfs setup.
func RegisterManagedRuntime(server *grpc.Server, runner ManagedRunner) func() {
	implementation := &runtimeServer{managed: runner, sessions: make(map[string]*session)}
	register(server, implementation)
	return implementation.shutdown
}

func register(server *grpc.Server, implementation *runtimeServer) {
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*runtimeService)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "Run", Handler: runHandler},
			{MethodName: "Start", Handler: startHandler},
			{MethodName: "Wait", Handler: waitHandler},
			{MethodName: "Stop", Handler: stopHandler},
		},
		Streams: []grpc.StreamDesc{{StreamName: "Attach", Handler: attachHandler, ServerStreams: true, ClientStreams: true}},
	}, implementation)
}

type runtimeService interface {
	Run(context.Context, *runtimev1.RunRequest) (*runtimev1.RunResponse, error)
	Start(context.Context, *runtimev1.RunRequest) (*runtimev1.StartResponse, error)
	Wait(context.Context, *runtimev1.WaitRequest) (*runtimev1.RunResponse, error)
	Stop(context.Context, *runtimev1.StopRequest) (*runtimev1.StopResponse, error)
	Attach(grpc.ServerStream) error
}

type session struct {
	id             string
	cancel         context.CancelFunc
	started        chan struct{}
	pid            int
	done           chan struct{}
	err            error
	stdin          io.WriteCloser
	resize         func(uint16, uint16) error
	tty            bool
	output         chan *runtimev1.AttachOutput
	attach         bool
	attachFinished bool
	waited         bool
	attachEnabled  bool
	attachMu       sync.Mutex
	outputWG       sync.WaitGroup
}

type runtimeServer struct {
	runner   Runner
	managed  ManagedRunner
	mu       sync.Mutex
	sessions map[string]*session
}

func (s *runtimeServer) Run(ctx context.Context, request *runtimev1.RunRequest) (*runtimev1.RunResponse, error) {
	if s.runner == nil {
		return nil, status.Error(codes.Unimplemented, "legacy Run is unavailable")
	}
	config := configFromRequest(request)
	if err := config.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	err := s.runner(ctx, config)
	return responseForError(ctx, err)
}

func (s *runtimeServer) Start(_ context.Context, request *runtimev1.RunRequest) (*runtimev1.StartResponse, error) {
	if s.managed == nil {
		return nil, status.Error(codes.Unimplemented, "managed Start is unavailable")
	}
	config := configFromRequest(request)
	if err := config.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	s.mu.Lock()
	if _, exists := s.sessions[config.ID]; exists {
		s.mu.Unlock()
		return nil, status.Error(codes.AlreadyExists, "container id is already active")
	}
	ctx, cancel := context.WithCancel(context.Background())
	item := &session{id: config.ID, cancel: cancel, started: make(chan struct{}), done: make(chan struct{}), attachEnabled: config.Attach}
	s.sessions[config.ID] = item
	s.mu.Unlock()
	if config.Attach {
		// Bound the lifetime of a session whose CLI client never connects.
		// This timer must start at creation: output backpressure may otherwise
		// keep the workload blocked before the runner reaches its completion path.
		time.AfterFunc(10*time.Minute, func() { s.removeSessionIfUnattached(item) })
	}
	go func() {
		err := s.managed(ctx, config, func(process container.StartedProcess) {
			item.pid = process.PID
			if config.Attach {
				item.stdin = process.Stdin
				if config.TTY {
					item.resize = process.Resize
				}
				item.output = make(chan *runtimev1.AttachOutput, 256)
				item.tty = config.TTY
				item.outputWG.Add(2)
				go relayOutput(process.Stdout, 1, item.output, &item.outputWG)
				go relayOutput(process.Stderr, 2, item.output, &item.outputWG)
			}
			close(item.started)
		})
		item.err = err
		if item.output != nil {
			item.outputWG.Wait()
			close(item.output)
		}
		select {
		case <-item.started:
		default:
			close(item.started)
		}
		close(item.done)
		item.attachMu.Lock()
		attached := item.attach
		waited := item.waited
		item.attachMu.Unlock()
		if attached && waited {
			s.removeSession(item)
		}
	}()
	<-item.started
	if item.pid <= 0 {
		s.mu.Lock()
		delete(s.sessions, config.ID)
		s.mu.Unlock()
		cancel()
		return nil, toStatus(item.err)
	}
	return &runtimev1.StartResponse{Pid: int32(item.pid), NetworkIp: config.NetworkIP}, nil
}

func (s *runtimeServer) Wait(ctx context.Context, request *runtimev1.WaitRequest) (*runtimev1.RunResponse, error) {
	item, err := s.lookup(request.GetId())
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	case <-item.done:
	}
	item.attachMu.Lock()
	item.waited = true
	remove := !item.attachEnabled || item.attachFinished
	item.attachMu.Unlock()
	if remove {
		s.removeSession(item)
	}
	return responseForError(ctx, item.err)
}

func (s *runtimeServer) Stop(_ context.Context, request *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	item, err := s.lookup(request.GetId())
	if err != nil {
		return nil, err
	}
	item.cancel()
	return &runtimev1.StopResponse{Stopped: true}, nil
}

func (s *runtimeServer) Attach(stream grpc.ServerStream) error {
	first := new(runtimev1.AttachInput)
	if err := stream.RecvMsg(first); err != nil {
		return status.Error(codes.InvalidArgument, "attach stream must begin with a container id")
	}
	item, err := s.lookup(first.GetContainerId())
	if err != nil {
		return err
	}
	if item.output == nil || item.stdin == nil {
		return status.Error(codes.FailedPrecondition, "container was not started with attach enabled")
	}
	item.attachMu.Lock()
	if item.attach {
		item.attachMu.Unlock()
		return status.Error(codes.AlreadyExists, "container already has an attached terminal")
	}
	item.attach = true
	item.attachMu.Unlock()
	go func() {
		defer item.stdin.Close()
		message := first
		for {
			if message.GetColumns() > 0 && message.GetRows() > 0 && item.resize != nil {
				if resizeErr := item.resize(uint16(message.GetColumns()), uint16(message.GetRows())); resizeErr != nil {
					return
				}
			}
			if data := message.GetStdin(); len(data) != 0 {
				for len(data) != 0 {
					written, writeErr := item.stdin.Write(data)
					if writeErr != nil {
						return
					}
					data = data[written:]
				}
			}
			if message.GetStdinEof() {
				if item.tty {
					_, _ = item.stdin.Write([]byte{4})
				}
				return
			}
			message = new(runtimev1.AttachInput)
			if recvErr := stream.RecvMsg(message); recvErr != nil {
				return
			}
		}
	}()
	connected := true
	for output := range item.output {
		if !connected {
			continue
		}
		if stream.Context().Err() != nil {
			connected = false
			_ = item.stdin.Close()
			continue
		}
		if sendErr := stream.SendMsg(output); sendErr != nil {
			connected = false
			_ = item.stdin.Close()
		}
	}
	_ = item.stdin.Close()
	item.attachMu.Lock()
	item.attachFinished = true
	remove := item.waited
	item.attachMu.Unlock()
	if remove {
		s.removeSession(item)
	}
	if connected {
		if item.err != nil {
			var exitError interface{ ExitCode() int }
			if errors.As(item.err, &exitError) {
				return stream.SendMsg(&runtimev1.AttachOutput{Exited: true, ExitCode: int32(exitError.ExitCode())})
			}
			var processError *exec.ExitError
			if errors.As(item.err, &processError) {
				return stream.SendMsg(&runtimev1.AttachOutput{Exited: true, ExitCode: int32(processError.ExitCode())})
			}
			return toStatus(item.err)
		}
		return stream.SendMsg(&runtimev1.AttachOutput{Exited: true})
	}
	return nil
}

func (s *runtimeServer) removeSession(item *session) {
	s.mu.Lock()
	if s.sessions[item.id] == item {
		delete(s.sessions, item.id)
	}
	s.mu.Unlock()
}

func (s *runtimeServer) removeSessionIfUnattached(item *session) {
	item.attachMu.Lock()
	defer item.attachMu.Unlock()
	if item.attach {
		return
	}
	s.mu.Lock()
	removed := false
	if s.sessions[item.id] == item {
		delete(s.sessions, item.id)
		removed = true
	}
	s.mu.Unlock()
	if removed {
		// An attach-enabled process that never gets a client must not remain
		// blocked forever on its bounded output queue.
		item.cancel()
	}
}

func relayOutput(reader io.ReadCloser, streamID int32, output chan<- *runtimev1.AttachOutput, wait *sync.WaitGroup) {
	defer wait.Done()
	if reader == nil {
		return
	}
	defer reader.Close()
	buffer := make([]byte, 32*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			chunk := &runtimev1.AttachOutput{Stream: streamID, Data: append([]byte(nil), buffer[:count]...)}
			// Preserve every byte. Backpressure is preferable to silently
			// corrupting an interactive session when the client is slow to read.
			output <- chunk
		}
		if err != nil {
			return
		}
	}
}

func (s *runtimeServer) lookup(id string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.sessions[id]
	if item == nil {
		return nil, status.Error(codes.NotFound, "container session not found")
	}
	return item, nil
}

func (s *runtimeServer) shutdown() {
	s.mu.Lock()
	items := make([]*session, 0, len(s.sessions))
	for _, item := range s.sessions {
		items = append(items, item)
		item.cancel()
	}
	s.mu.Unlock()
	for _, item := range items {
		<-item.done
	}
}

func configFromRequest(request *runtimev1.RunRequest) container.Config {
	return container.Config{ID: request.GetId(), RootFS: request.GetRootfs(), Command: request.GetCommand(), Env: request.GetEnv(), Hostname: request.GetHostname(), MemoryMax: request.GetMemoryMax(), CPUQuota: request.GetCpuQuota(), CPUPeriod: request.GetCpuPeriod(), Network: request.GetNetwork(), NetworkIP: request.GetNetworkIp(), Attach: request.GetAttach(), TTY: request.GetTty()}
}

func responseForError(ctx context.Context, err error) (*runtimev1.RunResponse, error) {
	if err == nil {
		return &runtimev1.RunResponse{}, nil
	}
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) {
		return &runtimev1.RunResponse{ExitCode: int32(exitError.ExitCode())}, nil
	}
	var processError *exec.ExitError
	if errors.As(err, &processError) {
		return &runtimev1.RunResponse{ExitCode: int32(processError.ExitCode())}, nil
	}
	return nil, toStatus(err)
}

func toStatus(err error) error {
	if err == nil {
		return status.Error(codes.Internal, "container failed before start")
	}
	return status.Error(codes.Internal, err.Error())
}

func runHandler(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(runtimev1.RunRequest)
	if err := decode(request); err != nil {
		return nil, err
	}
	return invoke(srv, ctx, interceptor, "/"+serviceName+"/Run", func(ctx context.Context) (any, error) { return srv.(runtimeService).Run(ctx, request) }, request)
}
func startHandler(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(runtimev1.RunRequest)
	if err := decode(request); err != nil {
		return nil, err
	}
	return invoke(srv, ctx, interceptor, "/"+serviceName+"/Start", func(ctx context.Context) (any, error) { return srv.(runtimeService).Start(ctx, request) }, request)
}
func waitHandler(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(runtimev1.WaitRequest)
	if err := decode(request); err != nil {
		return nil, err
	}
	return invoke(srv, ctx, interceptor, "/"+serviceName+"/Wait", func(ctx context.Context) (any, error) { return srv.(runtimeService).Wait(ctx, request) }, request)
}
func stopHandler(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(runtimev1.StopRequest)
	if err := decode(request); err != nil {
		return nil, err
	}
	return invoke(srv, ctx, interceptor, "/"+serviceName+"/Stop", func(ctx context.Context) (any, error) { return srv.(runtimeService).Stop(ctx, request) }, request)
}

func attachHandler(srv any, stream grpc.ServerStream) error {
	return srv.(runtimeService).Attach(stream)
}
func invoke(srv any, ctx context.Context, interceptor grpc.UnaryServerInterceptor, method string, call func(context.Context) (any, error), request any) (any, error) {
	if interceptor == nil {
		return call(ctx)
	}
	return interceptor(ctx, request, &grpc.UnaryServerInfo{Server: srv, FullMethod: method}, func(ctx context.Context, _ any) (any, error) { return call(ctx) })
}

// RuntimeClient is a typed client useful to Go callers and integration tests.
type RuntimeClient struct{ connection grpc.ClientConnInterface }

func NewRuntimeClient(connection grpc.ClientConnInterface) *RuntimeClient {
	return &RuntimeClient{connection: connection}
}
func (c *RuntimeClient) Run(ctx context.Context, request *runtimev1.RunRequest) (*runtimev1.RunResponse, error) {
	response := new(runtimev1.RunResponse)
	err := c.connection.Invoke(ctx, "/"+serviceName+"/Run", request, response)
	return response, err
}
func (c *RuntimeClient) Start(ctx context.Context, request *runtimev1.RunRequest) (*runtimev1.StartResponse, error) {
	response := new(runtimev1.StartResponse)
	err := c.connection.Invoke(ctx, "/"+serviceName+"/Start", request, response)
	return response, err
}
func (c *RuntimeClient) Wait(ctx context.Context, request *runtimev1.WaitRequest) (*runtimev1.RunResponse, error) {
	response := new(runtimev1.RunResponse)
	err := c.connection.Invoke(ctx, "/"+serviceName+"/Wait", request, response)
	return response, err
}
func (c *RuntimeClient) Stop(ctx context.Context, request *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	response := new(runtimev1.StopResponse)
	err := c.connection.Invoke(ctx, "/"+serviceName+"/Stop", request, response)
	return response, err
}

func (c *RuntimeClient) Attach(ctx context.Context) (grpc.ClientStream, error) {
	return c.connection.NewStream(ctx, &grpc.StreamDesc{StreamName: "Attach", ServerStreams: true, ClientStreams: true}, "/"+serviceName+"/Attach")
}
