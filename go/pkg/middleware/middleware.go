// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"
	"sync"

	"github.com/microsoft/agent-framework/go/pkg/agent"
)

// ContextKey is used for storing values in context.
type ContextKey string

const (
	// AgentRunContextKey is the key for storing AgentRunContext in context.
	AgentRunContextKey ContextKey = "agent_run_context"
	// FunctionInvocationContextKey is the key for storing FunctionInvocationContext in context.
	FunctionInvocationContextKey ContextKey = "function_invocation_context"
	// ChatContextKey is the key for storing ChatContext in context.
	ChatContextKey ContextKey = "chat_context"
)

// AgentRunContext is the context passed to agent middleware.
type AgentRunContext struct {
	Agent     *agent.Agent
	Messages  []agent.Message
	Metadata  map[string]any
	Result    any // AgentRunResponse or error result
	Terminate bool
	mu        sync.RWMutex
}

// SetMetadata sets a metadata value.
func (c *AgentRunContext) SetMetadata(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Metadata == nil {
		c.Metadata = make(map[string]any)
	}
	c.Metadata[key] = value
}

// GetMetadata gets a metadata value.
func (c *AgentRunContext) GetMetadata(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.Metadata[key]
	return val, ok
}

// FunctionInvocationContext is the context passed to function middleware.
type FunctionInvocationContext struct {
	Function  *agent.Func
	Arguments any
	Metadata  map[string]any
	Result    any
	Terminate bool
	mu        sync.RWMutex
}

// SetMetadata sets a metadata value.
func (c *FunctionInvocationContext) SetMetadata(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Metadata == nil {
		c.Metadata = make(map[string]any)
	}
	c.Metadata[key] = value
}

// GetMetadata gets a metadata value.
func (c *FunctionInvocationContext) GetMetadata(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.Metadata[key]
	return val, ok
}

// ChatContext is the context passed to chat middleware.
type ChatContext struct {
	ChatClient  any // chat.ChatClient
	Messages    []any
	Metadata    map[string]any
	Result      any // ChatResponse or streaming results
	Terminate   bool
	IsStreaming bool
	mu          sync.RWMutex
}

// SetMetadata sets a metadata value.
func (c *ChatContext) SetMetadata(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Metadata == nil {
		c.Metadata = make(map[string]any)
	}
	c.Metadata[key] = value
}

// GetMetadata gets a metadata value.
func (c *ChatContext) GetMetadata(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.Metadata[key]
	return val, ok
}

// NextFunc is the function to call the next middleware or final handler.
type NextFunc[T any] func(ctx T) error

// AgentMiddleware is the interface for agent middleware.
type AgentMiddleware interface {
	Process(ctx *AgentRunContext, next NextFunc[*AgentRunContext]) error
}

// FunctionMiddleware is the interface for function middleware.
type FunctionMiddleware interface {
	Process(ctx *FunctionInvocationContext, next NextFunc[*FunctionInvocationContext]) error
}

// ChatMiddleware is the interface for chat middleware.
type ChatMiddleware interface {
	Process(ctx *ChatContext, next NextFunc[*ChatContext]) error
}

// AgentMiddlewareFunc is a function-based agent middleware.
type AgentMiddlewareFunc func(ctx *AgentRunContext, next NextFunc[*AgentRunContext]) error

// Process implements AgentMiddleware interface.
func (f AgentMiddlewareFunc) Process(ctx *AgentRunContext, next NextFunc[*AgentRunContext]) error {
	return f(ctx, next)
}

// FunctionMiddlewareFunc is a function-based function middleware.
type FunctionMiddlewareFunc func(ctx *FunctionInvocationContext, next NextFunc[*FunctionInvocationContext]) error

// Process implements FunctionMiddleware interface.
func (f FunctionMiddlewareFunc) Process(ctx *FunctionInvocationContext, next NextFunc[*FunctionInvocationContext]) error {
	return f(ctx, next)
}

// ChatMiddlewareFunc is a function-based chat middleware.
type ChatMiddlewareFunc func(ctx *ChatContext, next NextFunc[*ChatContext]) error

// Process implements ChatMiddleware interface.
func (f ChatMiddlewareFunc) Process(ctx *ChatContext, next NextFunc[*ChatContext]) error {
	return f(ctx, next)
}

// AgentMiddlewarePipeline manages agent middleware execution.
type AgentMiddlewarePipeline struct {
	middlewares []AgentMiddleware
	mu          sync.RWMutex
}

// NewAgentMiddlewarePipeline creates a new agent middleware pipeline.
func NewAgentMiddlewarePipeline(middlewares ...AgentMiddleware) *AgentMiddlewarePipeline {
	return &AgentMiddlewarePipeline{
		middlewares: middlewares,
	}
}

// Add adds middleware to the pipeline.
func (p *AgentMiddlewarePipeline) Add(middleware AgentMiddleware) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.middlewares = append(p.middlewares, middleware)
}

// Execute executes the middleware pipeline.
func (p *AgentMiddlewarePipeline) Execute(ctx context.Context, agentCtx *AgentRunContext, handler func(*AgentRunContext) error) error {
	p.mu.RLock()
	middlewares := make([]AgentMiddleware, len(p.middlewares))
	copy(middlewares, p.middlewares)
	p.mu.RUnlock()

	// Create the handler chain
	var chain NextFunc[*AgentRunContext]
	chain = func(c *AgentRunContext) error {
		if c.Terminate {
			return nil
		}
		return handler(c)
	}

	// Build the middleware chain from the end backwards
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw := middlewares[i]
		nextChain := chain
		chain = func(c *AgentRunContext) error {
			if c.Terminate {
				return nil
			}
			return mw.Process(c, nextChain)
		}
	}

	// Execute the chain
	return chain(agentCtx)
}

// FunctionMiddlewarePipeline manages function middleware execution.
type FunctionMiddlewarePipeline struct {
	middlewares []FunctionMiddleware
	mu          sync.RWMutex
}

// NewFunctionMiddlewarePipeline creates a new function middleware pipeline.
func NewFunctionMiddlewarePipeline(middlewares ...FunctionMiddleware) *FunctionMiddlewarePipeline {
	return &FunctionMiddlewarePipeline{
		middlewares: middlewares,
	}
}

// Add adds middleware to the pipeline.
func (p *FunctionMiddlewarePipeline) Add(middleware FunctionMiddleware) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.middlewares = append(p.middlewares, middleware)
}

// Execute executes the middleware pipeline.
func (p *FunctionMiddlewarePipeline) Execute(ctx context.Context, funcCtx *FunctionInvocationContext, handler func(*FunctionInvocationContext) error) error {
	p.mu.RLock()
	middlewares := make([]FunctionMiddleware, len(p.middlewares))
	copy(middlewares, p.middlewares)
	p.mu.RUnlock()

	// Create the handler chain
	var chain NextFunc[*FunctionInvocationContext]
	chain = func(c *FunctionInvocationContext) error {
		if c.Terminate {
			return nil
		}
		return handler(c)
	}

	// Build the middleware chain from the end backwards
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw := middlewares[i]
		nextChain := chain
		chain = func(c *FunctionInvocationContext) error {
			if c.Terminate {
				return nil
			}
			return mw.Process(c, nextChain)
		}
	}

	// Execute the chain
	return chain(funcCtx)
}

// ChatMiddlewarePipeline manages chat middleware execution.
type ChatMiddlewarePipeline struct {
	middlewares []ChatMiddleware
	mu          sync.RWMutex
}

// NewChatMiddlewarePipeline creates a new chat middleware pipeline.
func NewChatMiddlewarePipeline(middlewares ...ChatMiddleware) *ChatMiddlewarePipeline {
	return &ChatMiddlewarePipeline{
		middlewares: middlewares,
	}
}

// Add adds middleware to the pipeline.
func (p *ChatMiddlewarePipeline) Add(middleware ChatMiddleware) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.middlewares = append(p.middlewares, middleware)
}

// Execute executes the middleware pipeline.
func (p *ChatMiddlewarePipeline) Execute(ctx context.Context, chatCtx *ChatContext, handler func(*ChatContext) error) error {
	p.mu.RLock()
	middlewares := make([]ChatMiddleware, len(p.middlewares))
	copy(middlewares, p.middlewares)
	p.mu.RUnlock()

	// Create the handler chain
	var chain NextFunc[*ChatContext]
	chain = func(c *ChatContext) error {
		if c.Terminate {
			return nil
		}
		return handler(c)
	}

	// Build the middleware chain from the end backwards
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw := middlewares[i]
		nextChain := chain
		chain = func(c *ChatContext) error {
			if c.Terminate {
				return nil
			}
			return mw.Process(c, nextChain)
		}
	}

	// Execute the chain
	return chain(chatCtx)
}

// GetAgentRunContext retrieves the AgentRunContext from a context.
func GetAgentRunContext(ctx context.Context) (*AgentRunContext, bool) {
	val, ok := ctx.Value(AgentRunContextKey).(*AgentRunContext)
	return val, ok
}

// WithAgentRunContext adds an AgentRunContext to a context.
func WithAgentRunContext(ctx context.Context, agentCtx *AgentRunContext) context.Context {
	return context.WithValue(ctx, AgentRunContextKey, agentCtx)
}

// GetFunctionInvocationContext retrieves the FunctionInvocationContext from a context.
func GetFunctionInvocationContext(ctx context.Context) (*FunctionInvocationContext, bool) {
	val, ok := ctx.Value(FunctionInvocationContextKey).(*FunctionInvocationContext)
	return val, ok
}

// WithFunctionInvocationContext adds a FunctionInvocationContext to a context.
func WithFunctionInvocationContext(ctx context.Context, funcCtx *FunctionInvocationContext) context.Context {
	return context.WithValue(ctx, FunctionInvocationContextKey, funcCtx)
}

// GetChatContext retrieves the ChatContext from a context.
func GetChatContext(ctx context.Context) (*ChatContext, bool) {
	val, ok := ctx.Value(ChatContextKey).(*ChatContext)
	return val, ok
}

// WithChatContext adds a ChatContext to a context.
func WithChatContext(ctx context.Context, chatCtx *ChatContext) context.Context {
	return context.WithValue(ctx, ChatContextKey, chatCtx)
}

// MiddlewareChain represents a chain of middleware that can be executed.
type MiddlewareChain struct {
	agentMiddleware    *AgentMiddlewarePipeline
	functionMiddleware *FunctionMiddlewarePipeline
	chatMiddleware     *ChatMiddlewarePipeline
}

// NewMiddlewareChain creates a new middleware chain.
func NewMiddlewareChain() *MiddlewareChain {
	return &MiddlewareChain{
		agentMiddleware:    NewAgentMiddlewarePipeline(),
		functionMiddleware: NewFunctionMiddlewarePipeline(),
		chatMiddleware:     NewChatMiddlewarePipeline(),
	}
}

// AddAgentMiddleware adds agent middleware to the chain.
func (mc *MiddlewareChain) AddAgentMiddleware(middleware AgentMiddleware) {
	mc.agentMiddleware.Add(middleware)
}

// AddFunctionMiddleware adds function middleware to the chain.
func (mc *MiddlewareChain) AddFunctionMiddleware(middleware FunctionMiddleware) {
	mc.functionMiddleware.Add(middleware)
}

// AddChatMiddleware adds chat middleware to the chain.
func (mc *MiddlewareChain) AddChatMiddleware(middleware ChatMiddleware) {
	mc.chatMiddleware.Add(middleware)
}

// ExecuteAgent executes the agent middleware chain with a handler.
func (mc *MiddlewareChain) ExecuteAgent(ctx context.Context, agentCtx *AgentRunContext, handler func(*AgentRunContext) error) error {
	return mc.agentMiddleware.Execute(ctx, agentCtx, handler)
}

// ExecuteFunction executes the function middleware chain with a handler.
func (mc *MiddlewareChain) ExecuteFunction(ctx context.Context, funcCtx *FunctionInvocationContext, handler func(*FunctionInvocationContext) error) error {
	return mc.functionMiddleware.Execute(ctx, funcCtx, handler)
}

// ExecuteChat executes the chat middleware chain with a handler.
func (mc *MiddlewareChain) ExecuteChat(ctx context.Context, chatCtx *ChatContext, handler func(*ChatContext) error) error {
	return mc.chatMiddleware.Execute(ctx, chatCtx, handler)
}
