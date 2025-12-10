# Middleware Examples

This directory contains essential middleware examples demonstrating key patterns and use cases.

## Overview

Middleware provides a way to intercept and modify requests/responses at three levels:

1. **Agent Middleware** - Intercepts agent invocations
2. **Function Middleware** - Intercepts function/tool calls
3. **Chat Middleware** - Intercepts chat client requests (future)

## Middleware Architecture

Middleware executes in a **chain pattern** where each middleware can:
- Perform **pre-processing** (before calling `next()`)
- Call the **next middleware or handler** via `next()`
- Perform **post-processing** (after `next()` returns)
- **Terminate execution** by setting `Terminate = true`
- **Override results** by setting `Result` directly

### Context Objects

Each middleware type operates on a specific context:

- **AgentRunContext**: Contains agent, messages, metadata
- **FunctionInvocationContext**: Contains function, arguments, metadata
- **ChatContext**: Contains chat client, messages, options, metadata

All contexts support:
- `SetMetadata(key, value)` / `GetMetadata(key)` for sharing data
- `Terminate` flag to short-circuit execution
- `Result` field to store/override execution results

## Samples

### 1. **logging_middleware** - Observability & Monitoring

Demonstrates logging and timing of middleware execution.

**Run:**
```bash
cd logging_middleware
go run main.go
```

**What it shows:**
- How to log before/after execution
- How to measure execution time using metadata
- Pre and post-processing patterns

**Key Middleware:**
- `LoggingMiddleware` - Logs agent execution with timing
- `FunctionExecutionLogger` - Logs function calls

---

### 2. **filtering_middleware** - Security & Validation

Demonstrates request/response filtering and validation.

**Run:**
```bash
cd filtering_middleware
go run main.go
```

**What it shows:**
- How to filter sensitive information
- How to validate requests and block unwanted calls
- How to transform responses
- Using `Terminate` to short-circuit execution

**Key Middleware:**
- `SecurityFilterMiddleware` - Blocks requests with sensitive keywords
- `ResponseTransformMiddleware` - Adds prefixes to responses
- `FunctionArgumentValidationMiddleware` - Validates allowed functions

---

### 3. **caching_middleware** - Performance & Resilience

Demonstrates caching, retries, and result validation.

**Run:**
```bash
cd caching_middleware
go run main.go
```

**What it shows:**
- How to cache results to avoid duplicate calls
- How to implement retry logic
- How to validate function results
- Cache hit/miss patterns

**Key Middleware:**
- `CachingMiddleware` - Caches function results using MD5 hashing
- `RetryMiddleware` - Retries failed functions up to N times
- `ResultValidationMiddleware` - Validates results match criteria

---

## Common Patterns

### Pre/Post Processing

```go
type LoggingMiddleware struct{}

func (m *LoggingMiddleware) Process(ctx *middleware.AgentRunContext, next middleware.NextFunc[*middleware.AgentRunContext]) error {
    // Pre-processing
    fmt.Println("Before execution")
    
    // Call next
    err := next(ctx)
    
    // Post-processing
    fmt.Println("After execution")
    
    return err
}
```

### Result Override

```go
type CachingMiddleware struct{}

func (m *CachingMiddleware) Process(ctx *middleware.FunctionInvocationContext, next middleware.NextFunc[*middleware.FunctionInvocationContext]) error {
    // Check cache
    if cachedResult, ok := m.cache[key]; ok {
        ctx.Result = cachedResult
        return nil  // Don't call next - result already set
    }
    
    // Call next to execute
    return next(ctx)
}
```

### Execution Termination

```go
type SecurityMiddleware struct{}

func (m *SecurityMiddleware) Process(ctx *middleware.AgentRunContext, next middleware.NextFunc[*middleware.AgentRunContext]) error {
    // Check for violations
    if isBlocked(ctx) {
        ctx.Result = "Blocked"
        ctx.Terminate = true
        return nil  // Stop execution
    }
    
    // Continue normally
    return next(ctx)
}
```

### Metadata Sharing

```go
// In middleware
ctx.SetMetadata("start_time", time.Now())

// In another middleware or later
if startTime, ok := ctx.GetMetadata("start_time"); ok {
    duration := time.Since(startTime.(time.Time))
    fmt.Printf("Execution took %v\n", duration)
}
```

## Building Middleware Chains

```go
// Create individual middleware
logging := middleware.LoggingMiddleware{...}
security := middleware.SecurityMiddleware{...}
caching := middleware.CachingMiddleware{...}

// Create pipeline (order matters!)
pipeline := middleware.NewFunctionMiddlewarePipeline(
    security,      // Runs first - check permissions
    caching,       // Then - check cache
    logging,       // Then - log execution
)

// Execute
err := pipeline.Execute(ctx, funcCtx, handler)
```

**Execution order:**
1. `security.Process()` runs, calls `next()`
2. `caching.Process()` runs, calls `next()`
3. `logging.Process()` runs, calls `next()`
4. `handler()` executes
5. `logging.Process()` completes (post-processing)
6. `caching.Process()` completes (post-processing)
7. `security.Process()` completes (post-processing)

## Use Cases

| Middleware | Use Case |
|-----------|----------|
| **Logging** | Audit trails, performance monitoring, debugging |
| **Security Filter** | Block sensitive keywords, implement access control |
| **Validation** | Validate arguments, check output format |
| **Caching** | Avoid redundant expensive calls |
| **Retry** | Handle transient failures automatically |
| **Rate Limiting** | Throttle calls, protect resources |
| **Authentication** | Verify credentials, validate tokens |
| **Transformation** | Normalize inputs, format outputs |

## Best Practices

1. **Single Responsibility**: Each middleware should do one thing
2. **Order Matters**: Place security middleware first, observability last
3. **Metadata Sharing**: Use metadata to share data between middleware
4. **Termination**: Use `Terminate` sparingly - mainly for security blocks
5. **Error Handling**: Propagate errors through `next()` unless recovering
6. **Performance**: Cache results to avoid redundant work
7. **Testing**: Test middleware independently and in chains

## Next Steps

- Implement middleware for your agents
- Combine multiple middleware for complex workflows
- Create domain-specific middleware for your use cases
