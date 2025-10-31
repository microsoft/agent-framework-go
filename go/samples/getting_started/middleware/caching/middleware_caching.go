package main

import (
	"context"
	"crypto/md5"
	"fmt"
	"sync"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/middleware"
)

// CachingMiddleware caches function results to avoid duplicate calls.
type CachingMiddleware struct {
	cache map[string]any
	mu    sync.RWMutex
}

// NewCachingMiddleware creates a new caching middleware.
func NewCachingMiddleware() *CachingMiddleware {
	return &CachingMiddleware{
		cache: make(map[string]any),
	}
}

// getCacheKey generates a cache key from function name and arguments.
func (m *CachingMiddleware) getCacheKey(funcName string, args any) string {
	hash := md5.Sum([]byte(fmt.Sprintf("%s:%v", funcName, args)))
	return fmt.Sprintf("%s", hash)
}

// Process implements the FunctionMiddleware interface.
func (m *CachingMiddleware) Process(ctx *middleware.FunctionInvocationContext, next middleware.NextFunc[*middleware.FunctionInvocationContext]) error {
	// Generate cache key
	cacheKey := m.getCacheKey(ctx.Function.Name, ctx.Arguments)

	// Check cache
	m.mu.RLock()
	if cachedResult, ok := m.cache[cacheKey]; ok {
		m.mu.RUnlock()
		fmt.Printf("[Cache] HIT: Function '%s' returned cached result\n", ctx.Function.Name)
		ctx.Result = cachedResult
		ctx.SetMetadata("cache_hit", true)
		return nil
	}
	m.mu.RUnlock()

	fmt.Printf("[Cache] MISS: Function '%s' not in cache, executing...\n", ctx.Function.Name)
	ctx.SetMetadata("cache_hit", false)

	// Call next middleware
	err := next(ctx)

	// Cache the result
	if err == nil && ctx.Result != nil {
		m.mu.Lock()
		m.cache[cacheKey] = ctx.Result
		m.mu.Unlock()
		fmt.Printf("[Cache] STORE: Function '%s' result cached\n", ctx.Function.Name)
	}

	return err
}

// RetryMiddleware retries function calls on failure.
type RetryMiddleware struct {
	maxRetries int
}

// NewRetryMiddleware creates a new retry middleware.
func NewRetryMiddleware(maxRetries int) *RetryMiddleware {
	return &RetryMiddleware{
		maxRetries: maxRetries,
	}
}

// Process implements the FunctionMiddleware interface.
func (m *RetryMiddleware) Process(ctx *middleware.FunctionInvocationContext, next middleware.NextFunc[*middleware.FunctionInvocationContext]) error {
	var lastErr error

	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		if attempt > 0 {
			fmt.Printf("[Retry] Attempt %d of %d for function '%s'\n", attempt, m.maxRetries, ctx.Function.Name)
		}

		err := next(ctx)

		if err == nil {
			if attempt > 0 {
				fmt.Printf("[Retry] Function '%s' succeeded on attempt %d\n", ctx.Function.Name, attempt+1)
			}
			return nil
		}

		lastErr = err
		fmt.Printf("[Retry] Function '%s' failed: %v\n", ctx.Function.Name, err)
	}

	fmt.Printf("[Retry] Function '%s' failed after %d retries\n", ctx.Function.Name, m.maxRetries+1)
	return lastErr
}

// ResultValidationMiddleware validates function results.
type ResultValidationMiddleware struct {
	validator func(any) bool
	name      string
}

// NewResultValidationMiddleware creates a new result validation middleware.
func NewResultValidationMiddleware(name string, validator func(any) bool) *ResultValidationMiddleware {
	return &ResultValidationMiddleware{
		name:      name,
		validator: validator,
	}
}

// Process implements the FunctionMiddleware interface.
func (m *ResultValidationMiddleware) Process(ctx *middleware.FunctionInvocationContext, next middleware.NextFunc[*middleware.FunctionInvocationContext]) error {
	fmt.Printf("\n[%s] Validating result for function '%s'...\n", m.name, ctx.Function.Name)

	err := next(ctx)

	if err != nil {
		return err
	}

	// Validate result
	if ctx.Result != nil && !m.validator(ctx.Result) {
		fmt.Printf("[%s] Result validation FAILED for function '%s'\n", m.name, ctx.Function.Name)
		ctx.Result = nil
		return fmt.Errorf("result validation failed")
	}

	fmt.Printf("[%s] Result validation PASSED for function '%s'\n", m.name, ctx.Function.Name)
	return nil
}

func main() {
	fmt.Println("=== Caching Middleware Example ===")

	// Example 1: Result caching
	fmt.Println("\n--- Result Caching Example ---")
	caching := NewCachingMiddleware()
	pipeline := middleware.NewFunctionMiddlewarePipeline(caching)

	// First call - should miss cache
	fmt.Println("\nCall 1: First time calling function")
	ctx1 := &middleware.FunctionInvocationContext{
		Function: &agent.Func{
			Name:        "expensive_calculation",
			Description: "An expensive calculation",
		},
		Arguments: map[string]any{"x": 10, "y": 20},
		Metadata:  make(map[string]any),
	}

	err := pipeline.Execute(context.Background(), ctx1, func(fc *middleware.FunctionInvocationContext) error {
		fmt.Println("[Handler] Performing expensive calculation...")
		fc.Result = 42
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("Result: %v\n", ctx1.Result)
	if cacheHit, ok := ctx1.GetMetadata("cache_hit"); ok {
		fmt.Printf("Cache hit: %v\n", cacheHit)
	}

	// Second call - should hit cache
	fmt.Println("\nCall 2: Calling function again with same arguments")
	ctx2 := &middleware.FunctionInvocationContext{
		Function: &agent.Func{
			Name:        "expensive_calculation",
			Description: "An expensive calculation",
		},
		Arguments: map[string]any{"x": 10, "y": 20},
		Metadata:  make(map[string]any),
	}

	err = pipeline.Execute(context.Background(), ctx2, func(fc *middleware.FunctionInvocationContext) error {
		fmt.Println("[Handler] This should NOT execute due to cache hit")
		fc.Result = 42
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("Result: %v\n", ctx2.Result)
	if cacheHit, ok := ctx2.GetMetadata("cache_hit"); ok {
		fmt.Printf("Cache hit: %v\n", cacheHit)
	}

	// Example 2: Result validation
	fmt.Println("\n\n--- Result Validation Example ---")
	validator := NewResultValidationMiddleware("Validator", func(result any) bool {
		// Only allow results that are positive integers
		if val, ok := result.(int); ok {
			return val > 0
		}
		return false
	})

	valPipeline := middleware.NewFunctionMiddlewarePipeline(validator)

	// Valid result
	fmt.Println("\nTest: Valid result")
	ctx3 := &middleware.FunctionInvocationContext{
		Function: &agent.Func{
			Name:        "get_count",
			Description: "Get count",
		},
		Arguments: nil,
		Metadata:  make(map[string]any),
	}

	err = valPipeline.Execute(context.Background(), ctx3, func(fc *middleware.FunctionInvocationContext) error {
		fmt.Println("[Handler] Returning valid result...")
		fc.Result = 5
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("Result: %v\n", ctx3.Result)

	// Invalid result
	fmt.Println("\n\nTest: Invalid result")
	ctx4 := &middleware.FunctionInvocationContext{
		Function: &agent.Func{
			Name:        "get_count",
			Description: "Get count",
		},
		Arguments: nil,
		Metadata:  make(map[string]any),
	}

	err = valPipeline.Execute(context.Background(), ctx4, func(fc *middleware.FunctionInvocationContext) error {
		fmt.Println("[Handler] Returning invalid result...")
		fc.Result = -5
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("Result: %v\n", ctx4.Result)

	// Example 3: Retry middleware
	fmt.Println("\n\n--- Retry Middleware Example ---")
	retry := NewRetryMiddleware(2)
	retryPipeline := middleware.NewFunctionMiddlewarePipeline(retry)

	callCount := 0
	fmt.Println("\nTest: Function succeeds on second attempt")
	ctx5 := &middleware.FunctionInvocationContext{
		Function: &agent.Func{
			Name:        "unreliable_function",
			Description: "A function that sometimes fails",
		},
		Arguments: nil,
		Metadata:  make(map[string]any),
	}

	err = retryPipeline.Execute(context.Background(), ctx5, func(fc *middleware.FunctionInvocationContext) error {
		callCount++
		if callCount < 2 {
			fmt.Printf("[Handler] Simulating failure (attempt %d)\n", callCount)
			return fmt.Errorf("temporary error")
		}
		fmt.Printf("[Handler] Success (attempt %d)\n", callCount)
		fc.Result = "Finally succeeded"
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("Result: %v\n", ctx5.Result)

	fmt.Println("\n\n=== Example Complete ===")
}
