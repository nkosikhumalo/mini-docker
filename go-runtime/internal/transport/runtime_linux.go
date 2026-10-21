//go:build linux

// Package transport exposes the stateless Java-to-Go runtime boundary over
// local gRPC. Java owns the user-visible lifecycle; Go owns kernel execution.
package transport

import (
	"context"
	"errors"
	"os/exec"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtimev1 "mini-docker/go-runtime/gen/runtime/v1"
	"mini-docker/go-runtime/internal/container"
)

const serviceName = "minidock.runtime.v1.RuntimeService"

type Runner func(context.Context, container.Config) error
type ManagedRunner func(context.Context, container.Config, func(int)) error

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
	}, implementation)
}

type runtimeService interface {
	Run(context.Context, *runtimev1.RunRequest) (*runtimev1.RunResponse, error)
	Start(context.Context, *runtimev1.RunRequest) (*runtimev1.StartResponse, error)
	Wait(context.Context, *runtimev1.WaitRequest) (*runtimev1.RunResponse, error)
	Stop(context.Context, *runtimev1.StopRequest) (*runtimev1.StopResponse, error)
}

type session struct {
	cancel  context.CancelFunc
	started chan struct{}
	pid     int
	done    chan struct{}
	err     error
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
	item := &session{cancel: cancel, started: make(chan struct{}), done: make(chan struct{})}
	s.sessions[config.ID] = item
	s.mu.Unlock()
	go func() {
		err := s.managed(ctx, config, func(pid int) { item.pid = pid; close(item.started) })
		item.err = err
		select {
		case <-item.started:
		default:
			close(item.started)
		}
		close(item.done)
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
	s.mu.Lock()
	delete(s.sessions, request.GetId())
	s.mu.Unlock()
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
	return container.Config{ID: request.GetId(), RootFS: request.GetRootfs(), Command: request.GetCommand(), Env: request.GetEnv(), Hostname: request.GetHostname(), MemoryMax: request.GetMemoryMax(), CPUQuota: request.GetCpuQuota(), CPUPeriod: request.GetCpuPeriod(), Network: request.GetNetwork(), NetworkIP: request.GetNetworkIp()}
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
