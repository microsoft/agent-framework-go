// Copyright (c) Microsoft. All rights reserved.

package contentexthandler

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"

	"github.com/microsoft/agent-framework-go/internal/concurrent"
	"github.com/microsoft/agent-framework-go/workflow"
)

type Handler[TRequest any, TResponse any] struct {
	port        workflow.RequestPort
	intercepted bool
	stateKey    string
	pending     concurrent.Map[string, TRequest]

	requestID       func(TRequest) string
	responseID      func(TResponse) string
	responseHandler func(*workflow.Context, TResponse) error
}

type Options[TRequest any, TResponse any] struct {
	Port            workflow.RequestPort
	StateKey        string
	Intercepted     bool
	RequestID       func(TRequest) string
	ResponseID      func(TResponse) string
	ResponseHandler func(*workflow.Context, TResponse) error
}

func New[TRequest any, TResponse any](options Options[TRequest, TResponse]) *Handler[TRequest, TResponse] {
	return &Handler[TRequest, TResponse]{
		port:            options.Port,
		intercepted:     options.Intercepted,
		stateKey:        options.StateKey,
		requestID:       options.RequestID,
		responseID:      options.ResponseID,
		responseHandler: options.ResponseHandler,
	}
}

func (h *Handler[TRequest, TResponse]) ConfigureRoutes(rb *workflow.RouteBuilder) *workflow.RouteBuilder {
	if !h.intercepted {
		return rb
	}
	return rb.AddHandler(reflect.TypeFor[TResponse](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
		return nil, h.responseHandler(ctx, msg.(TResponse))
	})
}

func (h *Handler[TRequest, TResponse]) HandleExternalResponse(ctx *workflow.Context, resp *workflow.ExternalResponse) (bool, error) {
	if h.intercepted {
		return false, nil
	}
	if resp.PortInfo.PortID != h.port.ID {
		return false, nil
	}
	value, ok := resp.Data.As(h.port.Response)
	if !ok {
		return true, fmt.Errorf("contentexthandler: response for port %q has type %T, want %v", h.port.ID, resp.Data.Any(), h.port.Response)
	}
	return true, h.responseHandler(ctx, value.(TResponse))
}

func (h *Handler[TRequest, TResponse]) HasPending(ctx *workflow.Context) (bool, error) {
	for range h.pending.Keys() {
		return true, nil
	}
	return false, nil
}

func (h *Handler[TRequest, TResponse]) MarkRequestAsHandled(ctx *workflow.Context, response TResponse) (bool, error) {
	id := h.responseID(response)
	request, found := h.pending.LoadAndDelete(id)
	if !found {
		return false, nil
	}
	if err := h.queuePendingState(ctx); err != nil {
		h.pending.Store(id, request)
		return true, err
	}
	return true, nil
}

func (h *Handler[TRequest, TResponse]) TrackRequest(ctx *workflow.Context, request TRequest) (bool, error) {
	id := h.requestID(request)
	if _, loaded := h.pending.LoadOrStore(id, request); loaded {
		return false, nil
	}
	if err := h.queuePendingState(ctx); err != nil {
		h.pending.Delete(id)
		return false, err
	}
	return true, nil
}

func (h *Handler[TRequest, TResponse]) DispatchRequest(ctx *workflow.Context, request TRequest) error {
	if h.intercepted {
		return ctx.SendMessage("", request)
	}
	id := h.requestID(request)
	req, err := workflow.NewExternalRequest(h.createExternalRequestID(id), h.port, request)
	if err != nil {
		return err
	}
	return ctx.PostRequest(req)
}

func (h *Handler[TRequest, TResponse]) createExternalRequestID(requestID string) string {
	return fmt.Sprintf("%d:%s:%s", len(h.port.ID), h.port.ID, requestID)
}

func (h *Handler[TRequest, TResponse]) Reset() error {
	h.pending.Clear()
	return nil
}

func (h *Handler[TRequest, TResponse]) Checkpoint(ctx *workflow.Context) error {
	return h.queuePendingState(ctx)
}

func (h *Handler[TRequest, TResponse]) Restore(ctx *workflow.Context) error {
	h.pending.Clear()
	if ctx.ReadState == nil {
		return nil
	}
	state, err := ctx.ReadState(h.stateKey, "")
	if err != nil {
		return err
	}
	pending, err := pendingRequestsFromState[TRequest](h.stateKey, state)
	if err != nil {
		return err
	}
	for id, request := range pending {
		h.pending.Store(id, request)
	}
	return nil
}

func (h *Handler[TRequest, TResponse]) queuePendingState(ctx *workflow.Context) error {
	if ctx.QueueStateUpdate == nil {
		return nil
	}
	return ctx.QueueStateUpdate(h.stateKey, "", maps.Collect(h.pending.All()))
}

func pendingRequestsFromState[TRequest any](stateKey string, state any) (map[string]TRequest, error) {
	if state == nil {
		return nil, nil
	}
	if pending, ok := state.(map[string]TRequest); ok {
		return pending, nil
	}

	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("contentexthandler: state %q has type %T, want %v", stateKey, state, reflect.TypeFor[map[string]TRequest]())
	}
	var pending map[string]TRequest
	if err := json.Unmarshal(data, &pending); err != nil {
		return nil, fmt.Errorf("contentexthandler: state %q has type %T, want %v", stateKey, state, reflect.TypeFor[map[string]TRequest]())
	}
	return pending, nil
}
