// Copyright (c) Microsoft. All rights reserved.

// Package modelcall supplies neutral request and response metadata to decorators
// around model-adapter calls.
package modelcall

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

// Metadata belongs to one adapter invocation. RequestModelID is resolved before
// the decorator runs; RecordResponse supplies service-reported identifiers.
type Metadata struct {
	RequestModelID  string
	ResponseModelID string
	ResponseID      string
}

// Decorator wraps one adapter invocation without changing its public contract.
type Decorator func(agent.RunFunc, *Metadata) agent.RunFunc

type (
	decoratorKey struct{}
	responseKey  struct{}
)

type decoratorState struct {
	owner    *agent.Agent
	decorate Decorator
}

type responseState struct {
	owner    *agent.Agent
	metadata *Metadata
}

// WithDecorator enables decoration for the current agent only. A nested agent
// must enable its own decorator rather than inheriting its caller's settings.
func WithDecorator(ctx context.Context, decorate Decorator) context.Context {
	owner, _ := agent.AgentFromContext(ctx)
	return context.WithValue(ctx, decoratorKey{}, decoratorState{owner: owner, decorate: decorate})
}

// Wrap adds the adapter's neutral metadata when a decorator is enabled.
// Uninstrumented calls retain the adapter's original execution behavior.
func Wrap(run agent.RunFunc, requestModel func([]agent.Option) string) agent.RunFunc {
	return func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		state, enabled := ctx.Value(decoratorKey{}).(decoratorState)
		owner, _ := agent.AgentFromContext(ctx)
		if !enabled || state.owner != owner {
			return run(ctx, messages, options...)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			metadata := &Metadata{RequestModelID: requestModel(options)}
			ctx := context.WithValue(ctx, responseKey{}, responseState{owner: owner, metadata: metadata})
			state.decorate(run, metadata)(ctx, messages, options...)(yield)
		}
	}
}

// RecordResponse supplies service-reported identifiers without changing response
// updates. Empty values do not clear metadata from earlier streaming events.
func RecordResponse(ctx context.Context, modelID, responseID string) {
	state, enabled := ctx.Value(responseKey{}).(responseState)
	owner, _ := agent.AgentFromContext(ctx)
	if !enabled || state.owner != owner {
		return
	}
	if modelID != "" {
		state.metadata.ResponseModelID = modelID
	}
	if responseID != "" {
		state.metadata.ResponseID = responseID
	}
}
