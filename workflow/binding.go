// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// ExecutorBinding describes how a workflow executor ID is bound to an
// executable [Executor]. Builders store bindings while constructing a
// [Workflow], and runners use them to instantiate executors for a workflow
// session.
type ExecutorBinding struct {
	// ID is the workflow-unique identifier for the executor. It is also the
	// address used by edges and messages inside the workflow graph.
	ID string

	// ExecutorType identifies the executor implementation or source type for
	// diagnostics, validation, and workflow metadata.
	ExecutorType reflect.Type

	// RawValue optionally carries the comparable source value behind this binding.
	// When the builder sees another binding with the same ID and [ExecutorType],
	// it compares RawValue to catch accidental reuse of the ID for a different
	// source value. RawValue must be nil or comparable; the builder rejects
	// non-comparable values. Leave it nil for sources such as function values.
	RawValue any

	// IsSharedInstance reports whether [NewExecutor] returns a shared executor
	// instance rather than creating an independent instance for each session.
	// Shared instances participate in workflow reset checks through [Reset].
	IsSharedInstance bool

	// SupportsConcurrentSharedExecution reports whether this binding may be used
	// by concurrent workflow runs. Shared-instance bindings should copy this from
	// [Executor.CrossRunShareable]; factory-created bindings should set it when
	// they can create isolated per-run executor instances.
	SupportsConcurrentSharedExecution bool

	// Ports lists [RequestPort]s that this binding exposes, either as the
	// workflow boundary port created by [BindRequestPort] or as additional ports
	// an executor uses to raise [ExternalRequest]s via [Context.PostRequest]. The
	// builder registers them so they appear in [Workflow.ReflectPorts]
	// metadata.
	Ports []RequestPort

	// NewExecutorFunc creates the executor instance for a workflow session. The
	// returned executor must have the same ID as this binding; [CreateInstance]
	// validates that contract and stamps [Executor.ExecutorType] for non-shared
	// instances.
	NewExecutorFunc func(sessionID string) (*Executor, error)

	// ResetFunc restores shared resources to their initial state. It is only called
	// for bindings marked as [IsSharedInstance]. A false return value means the
	// workflow could not be reset safely.
	ResetFunc func() bool
}

func bindingTypeName(typ reflect.Type) string {
	if typ == nil {
		return "<unknown>"
	}
	if name := typ.Name(); name != "" {
		return name
	}
	return typ.String()
}

// String returns a compact representation of the binding for diagnostics.
func (eb ExecutorBinding) String() string {
	if eb.isPlaceholder() {
		return eb.ID + ":<unbound>"
	}
	return eb.ID + ":" + bindingTypeName(eb.ExecutorType)
}

// isPlaceholder reports whether this binding only reserves an executor ID.
func (eb ExecutorBinding) isPlaceholder() bool {
	return eb.NewExecutorFunc == nil
}

// TryReset resets this binding if it wraps a shared executor instance.
// Non-shared bindings are already isolated per session and therefore report
// success without invoking [ExecutorBinding.Reset].
func (eb ExecutorBinding) TryReset() bool {
	if !eb.IsSharedInstance {
		// Non-shared instances do not need resetting
		return true
	}
	if eb.ResetFunc == nil {
		return false
	}
	return eb.ResetFunc()
}

// CreateInstance creates the executor for sessionID and validates that the
// returned executor matches this binding. It returns an error for placeholder
// bindings, nil executors, factory errors, or executor ID mismatches.
func (eb ExecutorBinding) CreateInstance(sessionID string) (*Executor, error) {
	if eb.isPlaceholder() {
		return nil, errors.New("cannot create executor from placeholder binding")
	}
	ex, err := eb.NewExecutorFunc(sessionID)
	if err != nil {
		return nil, err
	}
	if ex == nil {
		return nil, errors.New("executor binding returned nil executor")
	}
	if ex.ID != eb.ID {
		return nil, fmt.Errorf("Executor ID mismatch: expected %q, but got %q", eb.ID, ex.ID)
	}
	if !eb.IsSharedInstance && ex.ExecutorType == nil {
		ex.ExecutorType = eb.ExecutorType
	}
	return ex, nil
}

// BindExecutor returns an [ExecutorBinding] for an existing executor instance.
// The executor is shared across workflow runs, so concurrent-run support is
// copied from [Executor.CrossRunShareable].
func BindExecutor(executor *Executor) ExecutorBinding {
	if executor == nil {
		panic("workflow: cannot bind nil Executor")
	}

	binding := ExecutorBinding{
		ID:                                executor.ID,
		ExecutorType:                      executor.ExecutorType,
		RawValue:                          executor,
		IsSharedInstance:                  true,
		SupportsConcurrentSharedExecution: executor.CrossRunShareable,
		NewExecutorFunc: func(string) (*Executor, error) {
			return executor, nil
		},
	}
	if executor.Spec.Reset != nil {
		binding.ResetFunc = func() bool {
			return executor.Reset() == nil
		}
	}
	return binding
}

// BindRequestPort returns an [ExecutorBinding] that exposes a [RequestPort]
// at the workflow boundary. The resulting executor accepts messages of
// `port.Request` type, raises them as [ExternalRequest]s via
// [Context.PostRequest], and forwards the matching [ExternalResponse] data to
// downstream executors.
func BindRequestPort(p RequestPort) ExecutorBinding {
	return ExecutorBinding{
		ID:           p.ID,
		ExecutorType: reflect.TypeFor[RequestPort](),
		RawValue:     p,
		Ports:        []RequestPort{p},
		NewExecutorFunc: func(_ string) (*Executor, error) {
			return newRequestPortExecutor(p), nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}

// newRequestPortExecutor creates the executor that turns request-port messages
// into [ExternalRequest]s and routes matching [ExternalResponse]s back into the
// workflow.
func newRequestPortExecutor(port RequestPort) *Executor {
	var (
		wrappedMu       sync.Mutex
		wrappedRequests = make(map[string]*ExternalRequest)
	)

	return &Executor{
		ID: port.ID,
		Spec: ExecutorSpec{
			// The executor's handler return values are exposed via PostRequest /
			// SendMessage explicitly; suppress the default auto-forwarding.
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *ProtocolBuilder) (*ProtocolBuilder, error) {
				rb.SendsMessageType(port.Response, reflect.TypeFor[*ExternalResponse]())
				rb.RouteBuilder.
					AddHandlerRaw(port.Request, nil, func(ctx *Context, msg any) (any, error) {
						req, err := NewExternalRequest("", port, msg)
						if err != nil {
							return nil, err
						}
						if err := ctx.PostRequest(req); err != nil {
							return nil, err
						}
						return nil, nil
					}).
					AddHandlerRaw(reflect.TypeFor[*ExternalRequest](), nil, func(ctx *Context, msg any) (any, error) {
						req := msg.(*ExternalRequest)
						data, ok := req.Data.As(port.Request)
						if !ok {
							return nil, fmt.Errorf("message type %v could not be interpreted as request type %v", req.PortInfo.RequestType, port.Request)
						}
						if !req.PortInfo.ResponseType.Match(port.Response) {
							return nil, fmt.Errorf("response type %v is not valid for request port response type %v", port.Response, req.PortInfo.ResponseType)
						}
						wrapped, err := NewExternalRequest(req.RequestID, port, data)
						if err != nil {
							return nil, err
						}
						wrappedMu.Lock()
						wrappedRequests[req.RequestID] = req
						wrappedMu.Unlock()
						if err := ctx.PostRequest(wrapped); err != nil {
							return nil, err
						}
						return nil, nil
					}).
					AddHandlerRaw(reflect.TypeFor[*ExternalResponse](), nil, func(ctx *Context, msg any) (any, error) {
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
					})
				return rb, nil
			},
		},
	}
}

// BindFunc adapts a function into an [ExecutorBinding]. The input type In
// becomes the executor's accepted message type and Out becomes its declared
// output type.
func BindFunc[In, Out any](id string, fn func(In) Out) ExecutorBinding {
	return ExecutorBinding{
		ID:                                id,
		ExecutorType:                      reflect.TypeOf(fn),
		RawValue:                          nil,  // don't set RawValue to fn, it is not comparable.
		SupportsConcurrentSharedExecution: true, // the binding creates an executor wrapper per run.
		NewExecutorFunc: func(_ string) (*Executor, error) {
			return &Executor{
				ID: id,
				Spec: ExecutorSpec{
					ConfigureProtocol: func(rb *ProtocolBuilder) (*ProtocolBuilder, error) {
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[In](), reflect.TypeFor[Out](), func(_ *Context, msg any) (any, error) {
							return fn(msg.(In)), nil
						})
						return rb, nil
					},
				},
			}, nil
		},
	}
}
