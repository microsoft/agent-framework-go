package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/middleware"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

// LoggingMiddleware logs agent invocations before and after execution.
type LoggingMiddleware struct {
	name string
}

// NewLoggingMiddleware creates a new logging middleware.
func NewLoggingMiddleware(name string) *LoggingMiddleware {
	return &LoggingMiddleware{
		name: name,
	}
}

// Process implements the AgentMiddleware interface.
func (m *LoggingMiddleware) Process(ctx *middleware.AgentRunContext, next middleware.NextFunc[*middleware.AgentRunContext]) error {
	fmt.Printf("\n[%s] Starting agent execution\n", m.name)
	startTime := time.Now()

	// Store the start time in metadata
	ctx.SetMetadata("start_time", startTime)

	// Call next middleware
	err := next(ctx)

	// Log after execution
	duration := time.Since(startTime)
	fmt.Printf("[%s] Agent execution completed in %v\n", m.name, duration)

	if err != nil {
		fmt.Printf("[%s] Error: %v\n", m.name, err)
	}

	return err
}

// FunctionExecutionLogger logs function invocations.
type FunctionExecutionLogger struct {
	name string
}

// NewFunctionExecutionLogger creates a new function execution logger.
func NewFunctionExecutionLogger(name string) *FunctionExecutionLogger {
	return &FunctionExecutionLogger{
		name: name,
	}
}

// Process implements the FunctionMiddleware interface.
func (m *FunctionExecutionLogger) Process(ctx *middleware.FunctionInvocationContext, next middleware.NextFunc[*middleware.FunctionInvocationContext]) error {
	fmt.Printf("\n[%s] Executing function: %s\n", m.name, ctx.Function.Name)
	startTime := time.Now()

	// Store the start time
	ctx.SetMetadata("start_time", startTime)

	// Call next middleware
	err := next(ctx)

	// Log result
	duration := time.Since(startTime)
	fmt.Printf("[%s] Function '%s' completed in %v\n", m.name, ctx.Function.Name, duration)

	if err != nil {
		fmt.Printf("[%s] Function error: %v\n", m.name, err)
	}

	return err
}

func main() {
	fmt.Println("=== Logging Middleware Example ===")
	fmt.Println()

	// Create middleware
	agentLogger := NewLoggingMiddleware("AgentLogger")

	// Create a middleware pipeline
	pipeline := middleware.NewAgentMiddlewarePipeline(agentLogger)

	// Create an agent context
	agentCtx := &middleware.AgentRunContext{
		Agent: nil, // In real usage, this would be an actual agent
		Messages: []agent.Message{
			{
				Role: agent.RoleUser,
				Contents: []agent.Content{
					&agent.TextContent{Text: "What is 2+2?"},
				},
			},
		},
		Metadata: make(map[string]any),
	}

	// Execute with middleware
	ctx := context.Background()
	err := pipeline.Execute(ctx, agentCtx, func(ac *middleware.AgentRunContext) error {
		fmt.Println("\n[Handler] Processing agent request...")

		// Simulate some work
		time.Sleep(100 * time.Millisecond)

		// Store a result
		ac.Result = "Agent response: 2+2=4"

		fmt.Println("[Handler] Request processed")
		return nil
	})

	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	// Print metadata collected by middleware
	fmt.Println("\n=== Execution Metadata ===")
	if startTime, ok := agentCtx.GetMetadata("start_time"); ok {
		fmt.Printf("Start time: %v\n", startTime)
	}

	// Example 2: Function middleware logging
	fmt.Println()
	fmt.Println()
	fmt.Println("=== Function Middleware Logging Example ===")
	fmt.Println()

	functionLogger := NewFunctionExecutionLogger("FunctionLogger")
	funcPipeline := middleware.NewFunctionMiddlewarePipeline(functionLogger)

	funcCtx := &middleware.FunctionInvocationContext{
		Function: &functool.Func{
			Name:        "calculate",
			Description: "Performs calculations",
		},
		Arguments: map[string]any{
			"operation": "add",
			"a":         5,
			"b":         3,
		},
		Metadata: make(map[string]any),
	}

	err = funcPipeline.Execute(ctx, funcCtx, func(fc *middleware.FunctionInvocationContext) error {
		fmt.Println("\n[Handler] Executing function...")
		time.Sleep(50 * time.Millisecond)
		fc.Result = "Result: 8"
		fmt.Println("[Handler] Function result stored")
		return nil
	})

	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	fmt.Println("\n=== Example Complete ===")
}
