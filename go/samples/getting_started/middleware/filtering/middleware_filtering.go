package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/middleware"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

// SecurityFilterMiddleware filters sensitive information from requests.
type SecurityFilterMiddleware struct {
	sensitiveKeywords []string
}

// NewSecurityFilterMiddleware creates a new security filter middleware.
func NewSecurityFilterMiddleware(keywords []string) *SecurityFilterMiddleware {
	return &SecurityFilterMiddleware{
		sensitiveKeywords: keywords,
	}
}

// Process implements the AgentMiddleware interface.
func (m *SecurityFilterMiddleware) Process(ctx *middleware.AgentRunContext, next middleware.NextFunc[*middleware.AgentRunContext]) error {
	fmt.Println("\n[SecurityFilter] Checking for sensitive information...")

	// Check messages for sensitive keywords
	for _, msg := range ctx.Messages {
		content := m.extractTextContent(&msg)
		for _, keyword := range m.sensitiveKeywords {
			if strings.Contains(strings.ToLower(content), strings.ToLower(keyword)) {
				fmt.Printf("[SecurityFilter] BLOCKED: Message contains sensitive keyword '%s'\n", keyword)
				ctx.Result = "Error: Request blocked due to sensitive content"
				ctx.Terminate = true
				return nil
			}
		}
	}

	fmt.Println("[SecurityFilter] Request passed security check")
	return next(ctx)
}

// extractTextContent extracts text from a message.
func (m *SecurityFilterMiddleware) extractTextContent(msg *agent.Message) string {
	var text strings.Builder
	for _, content := range msg.Contents {
		if textContent, ok := content.(*agent.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String()
}

// ResponseTransformMiddleware transforms responses.
type ResponseTransformMiddleware struct {
	prefix string
}

// NewResponseTransformMiddleware creates a new response transform middleware.
func NewResponseTransformMiddleware(prefix string) *ResponseTransformMiddleware {
	return &ResponseTransformMiddleware{
		prefix: prefix,
	}
}

// Process implements the AgentMiddleware interface.
func (m *ResponseTransformMiddleware) Process(ctx *middleware.AgentRunContext, next middleware.NextFunc[*middleware.AgentRunContext]) error {
	fmt.Printf("\n[ResponseTransform] Adding prefix: '%s'\n", m.prefix)

	// Call next middleware
	err := next(ctx)

	// Transform the result
	if ctx.Result != nil {
		if resultStr, ok := ctx.Result.(string); ok {
			ctx.Result = m.prefix + ": " + resultStr
			fmt.Printf("[ResponseTransform] Transformed result: %s\n", ctx.Result)
		}
	}

	return err
}

// FunctionArgumentValidationMiddleware validates function arguments.
type FunctionArgumentValidationMiddleware struct {
	allowedFunctions map[string]bool
}

// NewFunctionArgumentValidationMiddleware creates a new validation middleware.
func NewFunctionArgumentValidationMiddleware(allowedFunctions []string) *FunctionArgumentValidationMiddleware {
	allowed := make(map[string]bool)
	for _, fn := range allowedFunctions {
		allowed[fn] = true
	}
	return &FunctionArgumentValidationMiddleware{
		allowedFunctions: allowed,
	}
}

// Process implements the FunctionMiddleware interface.
func (m *FunctionArgumentValidationMiddleware) Process(ctx *middleware.FunctionInvocationContext, next middleware.NextFunc[*middleware.FunctionInvocationContext]) error {
	fmt.Printf("\n[FunctionValidator] Validating function: %s\n", ctx.Function.Name)

	// Check if function is allowed
	if !m.allowedFunctions[ctx.Function.Name] {
		fmt.Printf("[FunctionValidator] BLOCKED: Function '%s' is not allowed\n", ctx.Function.Name)
		ctx.Result = nil
		ctx.Terminate = true
		return nil
	}

	fmt.Printf("[FunctionValidator] Function '%s' is allowed\n", ctx.Function.Name)
	return next(ctx)
}

func main() {
	fmt.Println("=== Filtering Middleware Example ===")

	// Example 1: Security filtering
	fmt.Println("\n--- Security Filter Example ---")
	securityFilter := NewSecurityFilterMiddleware([]string{"password", "secret", "apikey"})
	pipeline := middleware.NewAgentMiddlewarePipeline(securityFilter)

	// Test 1: Normal request
	fmt.Println("\nTest 1: Normal request")
	ctx1 := &middleware.AgentRunContext{
		Agent: nil,
		Messages: []agent.Message{
			{
				Role: agent.RoleUser,
				Contents: []agent.Content{
					&agent.TextContent{Text: "What is the weather today?"},
				},
			},
		},
		Metadata: make(map[string]any),
	}

	err := pipeline.Execute(context.Background(), ctx1, func(ac *middleware.AgentRunContext) error {
		fmt.Println("[Handler] Processing normal request...")
		ac.Result = "Agent response: Today is sunny"
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Test 2: Request with sensitive keyword
	fmt.Println("\n\nTest 2: Request with sensitive keyword")
	ctx2 := &middleware.AgentRunContext{
		Agent: nil,
		Messages: []agent.Message{
			{
				Role: agent.RoleUser,
				Contents: []agent.Content{
					&agent.TextContent{Text: "What is my API key?"},
				},
			},
		},
		Metadata: make(map[string]any),
	}

	err = pipeline.Execute(context.Background(), ctx2, func(ac *middleware.AgentRunContext) error {
		fmt.Println("[Handler] This should not execute")
		ac.Result = "Agent response"
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Example 2: Response transformation
	fmt.Println("\n\n--- Response Transform Example ---")
	responseTransform := NewResponseTransformMiddleware("RESPONSE")
	transformPipeline := middleware.NewAgentMiddlewarePipeline(responseTransform)

	ctx3 := &middleware.AgentRunContext{
		Agent:    nil,
		Messages: []agent.Message{},
		Metadata: make(map[string]any),
	}

	err = transformPipeline.Execute(context.Background(), ctx3, func(ac *middleware.AgentRunContext) error {
		fmt.Println("[Handler] Generating response...")
		ac.Result = "Original response"
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("\nFinal result: %v\n", ctx3.Result)

	// Example 3: Function argument validation
	fmt.Println("\n\n--- Function Argument Validation Example ---")
	validator := NewFunctionArgumentValidationMiddleware([]string{"get_weather", "get_time"})
	funcPipeline := middleware.NewFunctionMiddlewarePipeline(validator)

	// Test allowed function
	fmt.Println("\nTest: Allowed function")
	ctx4 := &middleware.FunctionInvocationContext{
		Function: &functool.Func{
			Name:        "get_weather",
			Description: "Get weather",
		},
		Arguments: nil,
		Metadata:  make(map[string]any),
	}

	err = funcPipeline.Execute(context.Background(), ctx4, func(fc *middleware.FunctionInvocationContext) error {
		fmt.Println("[Handler] Executing allowed function...")
		fc.Result = "Weather data"
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Test blocked function
	fmt.Println("\n\nTest: Blocked function")
	ctx5 := &middleware.FunctionInvocationContext{
		Function: &functool.Func{
			Name:        "delete_data",
			Description: "Delete data",
		},
		Arguments: nil,
		Metadata:  make(map[string]any),
	}

	err = funcPipeline.Execute(context.Background(), ctx5, func(fc *middleware.FunctionInvocationContext) error {
		fmt.Println("[Handler] This should not execute")
		fc.Result = "Data deleted"
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	fmt.Println("\n\n=== Example Complete ===")
}
