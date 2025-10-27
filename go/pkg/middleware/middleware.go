// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"

	"github.com/microsoft/agent-framework/go/pkg/agent"
)

// AgentMiddleware intercepts agent run requests and responses.
type AgentMiddleware interface {
	// OnRunStart is called before an agent run.
	OnRunStart(ctx context.Context, agentCtx *AgentContext) error

	// OnRunComplete is called after an agent run completes.
	OnRunComplete(ctx context.Context, agentCtx *AgentContext, response *agent.Message) error

	// OnRunError is called when an agent run fails.
	OnRunError(ctx context.Context, agentCtx *AgentContext, err error) error
}

// AgentContext contains context for an agent run.
type AgentContext struct {
	AgentID  string
	Messages []*agent.Message
	Metadata map[string]any
}

// FunctionMiddleware intercepts tool/function calls.
type FunctionMiddleware interface {
	// OnFunctionCall is called before a function is executed.
	OnFunctionCall(ctx context.Context, functionCtx *FunctionContext) error

	// OnFunctionComplete is called after a function completes.
	OnFunctionComplete(ctx context.Context, functionCtx *FunctionContext, result string) error

	// OnFunctionError is called when a function fails.
	OnFunctionError(ctx context.Context, functionCtx *FunctionContext, err error) error
}

// FunctionContext contains context for a function call.
type FunctionContext struct {
	Tool      agent.Tool
	Arguments string
	CallID    string
	Metadata  map[string]any
}

// Pipeline manages a chain of middleware.
type Pipeline struct {
	agentMiddleware    []AgentMiddleware
	functionMiddleware []FunctionMiddleware
}

// NewPipeline creates a new middleware pipeline.
func NewPipeline() *Pipeline {
	return &Pipeline{
		agentMiddleware:    make([]AgentMiddleware, 0),
		functionMiddleware: make([]FunctionMiddleware, 0),
	}
}

// AddAgentMiddleware adds agent middleware to the pipeline.
func (p *Pipeline) AddAgentMiddleware(middleware AgentMiddleware) {
	p.agentMiddleware = append(p.agentMiddleware, middleware)
}

// AddFunctionMiddleware adds function middleware to the pipeline.
func (p *Pipeline) AddFunctionMiddleware(middleware FunctionMiddleware) {
	p.functionMiddleware = append(p.functionMiddleware, middleware)
}

// ExecuteAgentRun runs agent middleware chain.
func (p *Pipeline) ExecuteAgentRun(ctx context.Context, agentCtx *AgentContext, handler func() (*agent.Message, error)) (*agent.Message, error) {
	// Execute OnRunStart for all middleware
	for _, mw := range p.agentMiddleware {
		if err := mw.OnRunStart(ctx, agentCtx); err != nil {
			var zero *agent.Message
			return zero, err
		}
	}

	// Execute the handler
	response, err := handler()

	// Execute OnRunComplete or OnRunError for all middleware
	for i := len(p.agentMiddleware) - 1; i >= 0; i-- {
		mw := p.agentMiddleware[i]
		if err != nil {
			mw.OnRunError(ctx, agentCtx, err)
		} else {
			mw.OnRunComplete(ctx, agentCtx, response)
		}
	}

	return response, err
}
