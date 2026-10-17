//go:build linux

// Package transport exposes the low-level runtime handshake to the Java
// control plane over local gRPC.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/status"
	"mini-docker/go-runtime/internal/container"
)

const serviceName = "minidock.runtime.v1.RuntimeService"

type RunRequest struct {
	Config container.Config `json:"config"`
}

type RunResponse struct {
	ExitCode int32 `json:"exit_code"`
}

type Runner func(context.Context, container.Config) error

type jsonCodec struct{}

func (jsonCodec) Name() string                           { return "json" }
func (jsonCodec) Marshal(value any) ([]byte, error)      { return json.Marshal(value) }
func (jsonCodec) Unmarshal(data []byte, value any) error { return json.Unmarshal(data, value) }

var registerCodec sync.Once

// RegisterRuntime installs the stateless Run RPC. The runner owns only the
// kernel launch and blocks until the workload exits or the request is canceled.
func RegisterRuntime(server *grpc.Server, runner Runner) {
	registerCodec.Do(func() { encoding.RegisterCodec(jsonCodec{}) })
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*interface{})(nil),
		Methods:     []grpc.MethodDesc{{MethodName: "Run", Handler: runHandler}},
	}, &runtimeServer{runner: runner})
}

type runtimeServer struct{ runner Runner }

func runHandler(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(RunRequest)
	if err := decode(request); err != nil {
		return nil, err
	}
	call := func(ctx context.Context, request any) (any, error) {
		config := request.(*RunRequest).Config
		if err := config.Validate(); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		err := srv.(*runtimeServer).runner(ctx, config)
		if err == nil {
			return &RunResponse{}, nil
		}
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		var exitError interface{ ExitCode() int }
		if errors.As(err, &exitError) {
			return &RunResponse{ExitCode: int32(exitError.ExitCode())}, nil
		}
		var processError *exec.ExitError
		if errors.As(err, &processError) {
			return &RunResponse{ExitCode: int32(processError.ExitCode())}, nil
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if interceptor == nil {
		return call(ctx, request)
	}
	return interceptor(ctx, request, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + serviceName + "/Run"}, call)
}

type RuntimeClient struct{ connection grpc.ClientConnInterface }

func NewRuntimeClient(connection grpc.ClientConnInterface) *RuntimeClient {
	return &RuntimeClient{connection: connection}
}

func (c *RuntimeClient) Run(ctx context.Context, request *RunRequest) (*RunResponse, error) {
	response := new(RunResponse)
	err := c.connection.Invoke(ctx, "/"+serviceName+"/Run", request, response, grpc.CallContentSubtype("json"))
	return response, err
}
