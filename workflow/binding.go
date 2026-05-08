// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

type ExecutorBinding struct {
	ID           string
	ExecutorType reflect.Type
	Raw          any

	IsSharedInstance                  bool
	SupportsConcurrentSharedExecution bool

	NewExecutor func(sessionID string) (*Executor, error)
	Reset       func() bool

	// Ports lists additional [RequestPort]s that this executor uses to
	// raise [ExternalRequest]s via [Context.PostRequest]. The builder
	// registers them in [Workflow.Ports] so they appear in workflow
	// metadata. Bindings that themselves are a request port boundary
	// (created via [BindRequestPort]) do not need to set this; the
	// builder picks the port up from [ExecutorBinding.Raw].
	Ports []RequestPort
}

func (eb *ExecutorBinding) String() string {
	if eb.isPlaceholder() {
		return eb.ID + ":<unbound>"
	}
	return eb.ID + ":" + eb.ExecutorType.Name()
}

func (eb *ExecutorBinding) isPlaceholder() bool {
	return eb.NewExecutor == nil
}

func (eb *ExecutorBinding) TryReset() bool {
	if !eb.IsSharedInstance {
		// Non-shared instances do not need resetting
		return true
	}
	if eb.Reset == nil {
		return false
	}
	return eb.Reset()
}

func (eb *ExecutorBinding) CreateInstance(sessionID string) (*Executor, error) {
	if eb.isPlaceholder() {
		return nil, errors.New("cannot create executor from placeholder binding")
	}
	ex, err := eb.NewExecutor(sessionID)
	if err != nil {
		return nil, err
	}
	if ex == nil {
		return nil, errors.New("executor binding returned nil executor")
	}
	if want, got := eb.ID, ex.ID; got != want {
		return nil, fmt.Errorf("Executor ID mismatch: expected %q, but got %q", want, got)
	}
	ex.ExecutorType = eb.ExecutorType
	return ex, nil
}

// BindRequestPort returns an [ExecutorBinding] that exposes a [RequestPort]
// at the workflow boundary. The resulting executor accepts messages of
// `port.Request` type, raises them as [ExternalRequest]s via
// [Context.PostRequest], and forwards the matching [ExternalResponse] data to
// downstream executors.
func BindRequestPort(p RequestPort) *ExecutorBinding {
	return &ExecutorBinding{
		ID:           p.ID,
		ExecutorType: reflect.TypeFor[RequestPort](),
		Raw:          p,
		NewExecutor: func(_ string) (*Executor, error) {
			return newRequestPortExecutor(p), nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}

func newRequestPortExecutor(port RequestPort) *Executor {
	var (
		wrappedMu       sync.Mutex
		wrappedRequests = make(map[string]*ExternalRequest)
	)

	return &Executor{
		ID: port.ID,
		Options: ExecutorOptions{
			// The executor's handler return values are exposed via PostRequest /
			// SendMessage explicitly; suppress the default auto-forwarding.
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
		},
		Config: []*ExecutorConfig{
			{
				ConfigureRoutes: func(rb *RouteBuilder) (*RouteBuilder, error) {
					return rb.
						AddHandler(port.Request, nil, false, func(ctx *Context, msg any) (any, error) {
							req, err := NewExternalRequest("", port, msg)
							if err != nil {
								return nil, err
							}
							if err := ctx.PostRequest(req); err != nil {
								return nil, err
							}
							return nil, nil
						}).
						AddHandler(reflect.TypeFor[*ExternalRequest](), nil, false, func(ctx *Context, msg any) (any, error) {
							req := msg.(*ExternalRequest)
							data, ok := req.Data.As(port.Request)
							if !ok {
								return nil, fmt.Errorf("message type %v could not be interpreted as request type %v", req.PortInfo.RequestType, port.Request)
							}
							if !req.PortInfo.ResponseType.Match(port.Response) {
								return nil, fmt.Errorf("response type %v is not valid for request port response type %v", port.Response, req.PortInfo.ResponseType)
							}
							wrapped, err := NewExternalRequest(req.ID, port, data)
							if err != nil {
								return nil, err
							}
							wrappedMu.Lock()
							wrappedRequests[req.ID] = req
							wrappedMu.Unlock()
							if err := ctx.PostRequest(wrapped); err != nil {
								return nil, err
							}
							return nil, nil
						}).
						AddHandler(reflect.TypeFor[*ExternalResponse](), nil, false, func(ctx *Context, msg any) (any, error) {
							resp := msg.(*ExternalResponse)
							if resp.PortInfo.PortID != port.ID {
								return nil, nil
							}
							data, ok := resp.Data.As(port.Response)
							if !ok {
								return nil, fmt.Errorf("expected response of type %v, got %T", port.Response, resp.Data.Any())
							}
							wrappedMu.Lock()
							original, wrapped := wrappedRequests[resp.RequestID]
							delete(wrappedRequests, resp.RequestID)
							wrappedMu.Unlock()
							if wrapped {
								return nil, ctx.SendMessage("", &ExternalResponse{
									PortInfo:  original.PortInfo,
									RequestID: resp.RequestID,
									Data:      resp.Data,
								})
							}
							if err := ctx.SendMessage("", resp); err != nil {
								return nil, err
							}
							return nil, ctx.SendMessage("", data)
						}), nil
				},
			},
		},
	}
}

func BindFunc[In, Out any](id string, threadSafe bool, fn func(In) Out) *ExecutorBinding {
	// Don't set Raw to fn, it is not comparable.
	return &ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeOf(fn),
		NewExecutor: func(_ string) (*Executor, error) {
			return funcExecutor(id, nil, func(_ *Context, input In) (Out, error) {
				return fn(input), nil
			}), nil
		},
		SupportsConcurrentSharedExecution: threadSafe,
	}
}

func BindFuncContext[In, Out any](id string, threadSafe bool, fn func(context.Context, In) Out) *ExecutorBinding {
	// Don't set Raw to fn, it is not comparable.
	return &ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeOf(fn),
		NewExecutor: func(_ string) (*Executor, error) {
			return funcExecutor(id, nil, func(ctx *Context, input In) (Out, error) {
				return fn(ctx, input), nil
			}), nil
		},
		SupportsConcurrentSharedExecution: threadSafe,
	}
}

func funcExecutor[In, Out any](id string, options *ExecutorOptions, handler func(*Context, In) (Out, error)) *Executor {
	var opts ExecutorOptions
	if options != nil {
		opts = *options
	}
	return &Executor{
		ID:      id,
		Options: opts,
		Config: []*ExecutorConfig{
			{
				ConfigureRoutes: func(rb *RouteBuilder) (*RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[In](), reflect.TypeFor[Out](), false, func(ctx *Context, msg any) (any, error) {
						return handler(ctx, msg.(In))
					}), nil
				},
			},
		},
	}
}
