// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"

	"github.com/microsoft/agent-framework/golang/pkg/message"
	"github.com/microsoft/agent-framework/golang/pkg/tool"
)

// AgentMiddleware intercepts agent run requests and responses.
type AgentMiddleware interface {
	// OnRunStart is called before an agent run.
	OnRunStart(ctx context.Context, agentCtx *AgentContext) error

	// OnRunComplete is called after an agent run completes.
	OnRunComplete(ctx context.Context, agentCtx *AgentContext, response *message.ChatResponse) error

	// OnRunError is called when an agent run fails.
	OnRunError(ctx context.Context, agentCtx *AgentContext, err error) error
}

// AgentContext contains context for an agent run.
type AgentContext struct {
	AgentID  string
	Messages []*message.ChatMessage
	Metadata map[string]interface{}
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
	Tool      tool.Tool
	Arguments string
	CallID    string
	Metadata  map[string]interface{}
}

// ChatMiddleware intercepts chat client requests and responses.
type ChatMiddleware interface {
	// OnChatRequest is called before a chat completion request.
	OnChatRequest(ctx context.Context, chatCtx *ChatContext) error

	// OnChatResponse is called after a chat completion response.
	OnChatResponse(ctx context.Context, chatCtx *ChatContext, response *message.ChatResponse) error

	// OnChatError is called when a chat completion fails.
	OnChatError(ctx context.Context, chatCtx *ChatContext, err error) error
}

// ChatContext contains context for a chat completion.
type ChatContext struct {
	ModelID  string
	Messages []*message.ChatMessage
	Metadata map[string]interface{}
}

// Pipeline manages a chain of middleware.
type Pipeline struct {
	agentMiddleware    []AgentMiddleware
	functionMiddleware []FunctionMiddleware
	chatMiddleware     []ChatMiddleware
}

// NewPipeline creates a new middleware pipeline.
func NewPipeline() *Pipeline {
	return &Pipeline{
		agentMiddleware:    make([]AgentMiddleware, 0),
		functionMiddleware: make([]FunctionMiddleware, 0),
		chatMiddleware:     make([]ChatMiddleware, 0),
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

// AddChatMiddleware adds chat middleware to the pipeline.
func (p *Pipeline) AddChatMiddleware(middleware ChatMiddleware) {
	p.chatMiddleware = append(p.chatMiddleware, middleware)
}

// ExecuteAgentRun runs agent middleware chain.
func (p *Pipeline) ExecuteAgentRun(ctx context.Context, agentCtx *AgentContext, handler func() (*message.ChatResponse, error)) (*message.ChatResponse, error) {
	// Execute OnRunStart for all middleware
	for _, mw := range p.agentMiddleware {
		if err := mw.OnRunStart(ctx, agentCtx); err != nil {
			return nil, err
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
