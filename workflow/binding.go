// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

type ExecutorBinding struct {
	ID           string
	ExecutorType reflect.Type
	Raw          any

	IsSharedInstance                  bool
	SupportsConcurrentSharedExecution bool

	NewExecutor func(runID string) (*Executor, error)
	Reset       func() bool
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

func (eb *ExecutorBinding) CreateInstance(runID string) (*Executor, error) {
	if eb.isPlaceholder() {
		return nil, errors.New("cannot create executor from placeholder binding")
	}
	ex, err := eb.NewExecutor(runID)
	if err != nil {
		return nil, err
	}
	if ex == nil {
		return nil, errors.New("executor binding returned nil executor")
	}
	if want, got := eb.ID, ex.ID; got != want {
		return nil, fmt.Errorf("Executor ID mismatch: expected %q, but got %q", want, got)
	}
	return ex, nil
}

func newConfiguredExecutorBinding(conf Configured[*Executor], executorType reflect.Type) *ExecutorBinding {
	return &ExecutorBinding{
		ID:                                conf.ID,
		ExecutorType:                      executorType,
		Raw:                               conf.Raw,
		NewExecutor:                       conf.NewBound,
		SupportsConcurrentSharedExecution: true,
		IsSharedInstance:                  true,
	}
}

func newRequestPortExecutor(port RequestPort, allowWrapped bool) *Executor {
	var e requestPortExecutor
	return &Executor{
		ID: port.ID,
		Options: ExecutorOptions{
			// We need to be able to return the ExternalRequest/Result objects so they can be bubbled up
			// through the event system, but we do not want to forward the Request message.
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
		},
		Config: []*ExecutorConfig{
			{
				ConfigureRoutes: func(rb *RouteBuilder) (*RouteBuilder, error) {
					return rb.
						AddHandler(port.Request, nil, false, e.handleAsync).
						AddCatchAll(false, e.catchAll), nil
				},
			},
		},
	}
}

type requestPortExecutor struct {
	port            RequestPort
	allowWrapped    bool
	wrappedRequests map[string]*ExternalRequest
	requestSink     func(*Context, *ExternalRequest) error
}

func (r *requestPortExecutor) catchAll(ctx *Context, msg PortableValue) (any, error) {
	if data, ok := msg.As(r.port.Response); ok {
		req, err := NewExternalRequest("", r.port, data)
		if err != nil {
			return nil, err
		}
		if r.requestSink != nil {
			if err := r.requestSink(ctx, req); err != nil {
				return nil, err
			}
		}
		return req, nil
	}
	if data, ok := msg.As(reflect.TypeFor[*ExternalRequest]()); ok {
		v, err := r.handleAsync(ctx, data)
		if err != nil {
			return nil, err
		}
		return v.(*ExternalRequest), nil
	}
	return nil, nil
}

func (r *requestPortExecutor) handleAsync(ctx *Context, msg any) (any, error) {
	switch msg := msg.(type) {
	case *ExternalResponse:
		if r.port.ID != msg.RequestPort.ID {
			return nil, nil
		}
		data, ok := msg.Data.As(r.port.Response)
		if !ok {
			return nil, fmt.Errorf("expected response of type %v, got %T", r.port.Response, msg.Data.Any())
		}
		sendMsg := msg
		if r.allowWrapped {
			if original, ok := r.wrappedRequests[msg.RequestID]; ok {
				sendMsg = original.Rewrap(msg)

			}
		}
		if err := ctx.SendMessage("", sendMsg); err != nil {
			return nil, err
		}
		if err := ctx.SendMessage("", data); err != nil {
			return nil, err
		}
		return msg, nil
	case *ExternalRequest:
		if !r.allowWrapped {
			panic("not reachable")
		}
		if !reflect.TypeOf(msg.Data.Any()).AssignableTo(r.port.Request) {
			return nil, fmt.Errorf("expected message of type %v, got %T", r.port.Request, msg.Data.Any())
		}
		if !reflect.TypeOf(msg.RequestPort.Response).AssignableTo(r.port.Response) {
			return nil, fmt.Errorf("expected response type of %v, got %v", r.port.Response, msg.RequestPort.Response)
		}
		if r.wrappedRequests == nil {
			r.wrappedRequests = make(map[string]*ExternalRequest)
		}
		r.wrappedRequests[msg.ID] = msg
		req, err := NewExternalRequest(msg.ID, r.port, msg)
		if err != nil {
			return nil, err
		}
		if r.requestSink != nil {
			if err = r.requestSink(ctx, req); err != nil {
				return nil, err
			}
		}
		return req, nil
	default:
		req, err := NewExternalRequest("", r.port, msg)
		if err != nil {
			return nil, err
		}
		if r.requestSink != nil {
			if err = r.requestSink(ctx, req); err != nil {
				return nil, err
			}
		}
		return req, nil
	}
}

func BindRequestPort(p RequestPort, allowWrapped bool) *ExecutorBinding {
	return &ExecutorBinding{
		ID:           p.ID,
		ExecutorType: reflect.TypeOf(p),
		Raw:          p,
		NewExecutor: func(_ string) (*Executor, error) {
			return newRequestPortExecutor(p, allowWrapped), nil
		},
		SupportsConcurrentSharedExecution: true,
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

func BindFuncCtx[In, Out any](id string, threadSafe bool, fn func(context.Context, In) Out) *ExecutorBinding {
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
