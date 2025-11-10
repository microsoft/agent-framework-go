// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var executors sync.Map // map[ExecutorishKind]Executor

type TurnToken struct {
	EmitEvents bool
}

// ExecutorOptions holds configuration options for [Executor] behavior.
type ExecutorOptions struct {
	AutoSendMessageHandlerResultObject bool
	AutoYieldOutputHandlerResultObject bool
}

type Executor interface {
	ID() string
	DefaultOptions() ExecutorOptions
	Router() (*MessageRouter, error)
}

type crossRunShareableExecutor interface {
	Executor

	CrossRunShareable() bool
}

type resettableExecutor interface {
	Executor

	Reset()
}

func Execute(ctx context.Context, wctx Context, e Executor, message any) (any, error) {
	eid := e.ID()
	if err := wctx.AddEvent(ctx, ExecutorInvokedEvent{ExecutorID: eid, Message: message}); err != nil {
		return nil, err
	}
	router, err := e.Router()
	if err != nil {
		return nil, fmt.Errorf("error getting router for executor %q: %w", eid, err)
	}
	ret, found := router.RouteMessage(ctx, wctx, message)
	if !found {
		return nil, fmt.Errorf("no handler found for message type %T", message)
	}
	if ret.Error != nil {
		if err := wctx.AddEvent(ctx, ExecutorFailedEvent{ExecutorID: eid, Error: ret.Error}); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("error invoking handler for message type %T: %w", message, ret.Error)
	} else {
		if err := wctx.AddEvent(ctx, ExecutorCompletedEvent{ExecutorID: eid, Result: ret.Result}); err != nil {
			return nil, err
		}
	}
	if ret.IsVoid {
		return nil, nil
	}
	// If we had a real return type, raise it as a SendMessage
	if ret.Result != nil {
		opts := e.DefaultOptions()
		if opts.AutoSendMessageHandlerResultObject {
			if err := wctx.SendMessage(ctx, ret.Result, ""); err != nil {
				return nil, err
			}
		}
		if opts.AutoYieldOutputHandlerResultObject {
			if err := wctx.YieldOutput(ctx, ret.Result); err != nil {
				return nil, err
			}
		}
	}
	return ret.Result, nil
}

type CallResult struct {
	IsVoid bool
	Result any
	Error  error
}

func (cr CallResult) Handled() bool {
	return cr != CallResult{}
}

func (cr CallResult) Canceled() bool {
	return errors.Is(cr.Error, context.Canceled)
}

type executorRegistration struct {
	id       string
	typ      reflect.Type
	rawData  any
	provider func(runId string) Executor
}

func (er *executorRegistration) isUnresettableSharedInstance() bool {
	e, ok := er.rawData.(Executor)
	if !ok {
		return false
	}
	if e, ok := er.rawData.(crossRunShareableExecutor); !ok || !e.CrossRunShareable() {
		// Cross-Run Shareable executors are "trivially" resettable,
		// since they have no on-object state.
		return false
	}
	_, ok = e.(resettableExecutor)
	return !ok
}

func (er *executorRegistration) supportsConcurrent() bool {
	switch er := er.rawData.(type) {
	case crossRunShareableExecutor:
		// Assume all Executors support concurrent use.
		return er.CrossRunShareable()
	default:
		return false
	}
}

func (er *executorRegistration) tryReset() bool {
	if !er.isUnresettableSharedInstance() {
		return false
	}
	if er.supportsConcurrent() {
		// If the executor supports concurrent use, then resetting is a no-op.
		return false
	}
	// Technically we definitely know this is true, since if rawData is an Executor, if it was not resettable
	// then we would have returned in the first condition, and if rawData is not an Executor, we would have
	// returned in the second condition. That only leaves the possibility of rawData is Executor and also
	// ResettableExecutor.
	if resettableExecutor, ok := er.rawData.(resettableExecutor); ok {
		resettableExecutor.Reset()
		return true
	}
	return false
}

func (er *executorRegistration) newExecutor(runId string) Executor {
	e := er.provider(runId)
	if id := e.ID(); id != er.id {
		// This should never happen unless the provider is buggy.
		panic(fmt.Sprintf("executor ID mismatch: expected %q, got %q", er.id, id))
	}
	return e
}

type ExecutorishKind int

const (
	ExecutorishKindUnbound ExecutorishKind = iota
	ExecutorishKindExecutor
	ExecutorishKindFunction
	ExecutorishKindRequestPort
	ExecutorishKindAgent
	ExecutorishKindWorkflow
)

// Executorish is a tagged union representing an object that can function like
// an [Executor] in a [Workflow], or a reference to one by ID.
type Executorish struct {
	id          string // for executorishKindUnbound
	kind        ExecutorishKind
	executor    Executor
	requestPort *RequestPort
}

func (e *Executorish) ID() string {
	switch e.kind {
	case ExecutorishKindUnbound:
		if e.id == "" {
			panic("unbound Executorish has no ID")
		}
		return e.id
	case ExecutorishKindExecutor, ExecutorishKindFunction, ExecutorishKindWorkflow:
		if e.executor == nil {
			return ""
		}
		return e.executor.ID()
	case ExecutorishKindRequestPort:
		if e.requestPort == nil {
			return ""
		}
		return e.requestPort.ID
	case ExecutorishKindAgent:
		// TODO
		return ""
	default:
		panic(fmt.Sprintf("unknown kind %d", e.kind))
	}
}

func (e *Executorish) rawData() any {
	switch e.kind {
	case ExecutorishKindUnbound:
		return e.id
	case ExecutorishKindExecutor, ExecutorishKindFunction, ExecutorishKindWorkflow:
		return e.executor
	case ExecutorishKindRequestPort:
		return e.requestPort
	case ExecutorishKindAgent:
		// TODO
		return nil
	default:
		panic(fmt.Sprintf("unknown kind %d", e.kind))
	}
}

func (e *Executorish) runtimeType() reflect.Type {
	switch e.kind {
	case ExecutorishKindUnbound:
		panic("unbound Executorish has no runtime type")
	case ExecutorishKindExecutor, ExecutorishKindFunction, ExecutorishKindWorkflow:
		return reflect.TypeOf(e.executor)
	case ExecutorishKindRequestPort:
		return reflect.TypeFor[*requestPortExecutor]()
	case ExecutorishKindAgent:
		ex, found := executors.Load(ExecutorishKindAgent)
		if !found {
			panic("agent executor not registered")
		}
		return reflect.TypeOf(ex)
	default:
		panic(fmt.Sprintf("unknown kind %d", e.kind))
	}
}

func (e *Executorish) executorProvider() func(string) Executor {
	switch e.kind {
	case ExecutorishKindUnbound:
		panic("unbound Executorish has no runtime type")
	case ExecutorishKindExecutor, ExecutorishKindFunction, ExecutorishKindWorkflow:
		return func(runID string) Executor {
			return e.executor
		}
	case ExecutorishKindRequestPort:
		return func(runID string) Executor {
			return &requestPortExecutor{
				port: *e.requestPort,
			}
		}
	case ExecutorishKindAgent:
		return func(runID string) Executor {
			ex, found := executors.Load(ExecutorishKindAgent)
			if !found {
				panic("agent executor not registered")
			}
			return ex.(Executor)
		}
	default:
		panic(fmt.Sprintf("unknown kind %d", e.kind))
	}
}

func (e *Executorish) registration() *executorRegistration {
	return &executorRegistration{
		id:      e.ID(),
		typ:     e.runtimeType(),
		rawData: e.rawData(),
	}
}

var _ Executor = (*requestPortExecutor)(nil)

type requestPortExecutor struct {
	port         RequestPort
	allowWrapped bool
	requestSink  func(*ExternalRequest) error

	wrappedRequests map[string]*ExternalRequest
	routeOnce       sync.Once
	builder         RouteBuilder
}

func (r *requestPortExecutor) ID() string {
	return r.port.ID
}

func (r *requestPortExecutor) DefaultOptions() ExecutorOptions {
	// We need to be able to return the ExternalRequest/Result objects so they can be bubbled up
	// through the event system, but we do not want to forward the Request message.
	return ExecutorOptions{}
}

func (r *requestPortExecutor) Router() (*MessageRouter, error) {
	if route, err, built := r.builder.Cached(); built {
		return route, err
	}
	return r.builder.
		AddHandler(r.port.RequestType, nil, false, func(ctx context.Context, wctx Context, msg any) CallResult {
			req, err := r.handleAsync(ctx, wctx, msg)
			return CallResult{Result: req, Error: err}
		}).
		AddCatchAll(false, func(ctx context.Context, wctx Context, msg Value) CallResult {
			req, err := r.handleCatchAll(ctx, wctx, msg)
			return CallResult{Result: req, Error: err}
		}).
		Build()
}

func (r *requestPortExecutor) handleCatchAll(ctx context.Context, wctx Context, msg Value) (*ExternalRequest, error) {
	if data, ok := msg.As(r.port.ResponseType); ok {
		req, err := NewExternalRequest("", r.port, data)
		if err != nil {
			return nil, err
		}
		if r.requestSink != nil {
			if err := r.requestSink(req); err != nil {
				return nil, err
			}
		}
		return req, nil
	}
	if data, ok := msg.As(reflect.TypeFor[ExternalRequest]()); ok {
		v, err := r.handleAsync(ctx, wctx, data)
		if err != nil {
			return nil, err
		}
		return v.(*ExternalRequest), nil
	}
	return nil, nil
}

func (r *requestPortExecutor) handleAsync(ctx context.Context, wctx Context, msg any) (any, error) {
	switch msg := msg.(type) {
	case *ExternalResponse:
		if r.port.ID != msg.RequestPort.ID {
			return nil, nil
		}
		data, ok := msg.Data.As(r.port.ResponseType)
		if !ok {
			return nil, fmt.Errorf("expected response of type %v, got %T", r.port.ResponseType, msg.Data.Any())
		}
		sendMsg := msg
		if r.allowWrapped {
			if original, ok := r.wrappedRequests[msg.RequestID]; ok {
				sendMsg = original.Rewrap(msg)

			}
		}
		if err := wctx.SendMessage(ctx, sendMsg, ""); err != nil {
			return nil, err
		}
		if err := wctx.SendMessage(ctx, data, ""); err != nil {
			return nil, err
		}
		return msg, nil
	case *ExternalRequest:
		if !r.allowWrapped {
			panic("not reachable")
		}
		if !reflect.TypeOf(msg.Data.Any()).AssignableTo(r.port.RequestType) {
			return nil, fmt.Errorf("expected message of type %v, got %T", r.port.RequestType, msg.Data.Any())
		}
		if !reflect.TypeOf(msg.RequestPort.ResponseType).AssignableTo(r.port.ResponseType) {
			return nil, fmt.Errorf("expected response type of %v, got %v", r.port.ResponseType, msg.RequestPort.ResponseType)
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
			if err = r.requestSink(req); err != nil {
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
			if err = r.requestSink(req); err != nil {
				return nil, err
			}
		}
		return req, nil
	}
}

type agentExecutor struct {
	emitEvent bool
}
