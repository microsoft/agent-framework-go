# Development Guide - Go SDK

This guide provides information for contributors working on the Go SDK implementation.

## Project Status

🚧 **Early Development** - The Go SDK currently provides stub implementations of the core API surface identified from the .NET and Python implementations.

## Development Setup

### Prerequisites

- Go 1.21 or later
- Git
- An OpenAI or Azure OpenAI API key for testing

### Clone and Setup

```bash
cd agent-framework/golang
go mod download
```

### Running Tests

```bash
go test ./...
```

### Running Examples

```bash
go run examples/basic/main.go
```

## Implementation Priorities

### Phase 1: Core Foundation ✅ (Current)
- [x] Package structure
- [x] Core interfaces and types
- [x] Agent abstraction
- [x] Message types
- [x] Thread management
- [x] Basic error types
- [x] Stub implementations

### Phase 2: Chat Client Implementations
- [ ] OpenAI chat completion client
- [ ] Azure OpenAI chat completion client
- [ ] Streaming support
- [ ] Error handling
- [ ] Retry logic
- [ ] Rate limiting

### Phase 3: Tool/Function Calling
- [ ] Function invocation framework
- [ ] Tool result handling
- [ ] Hosted tool abstractions
- [ ] Tool approval flow

### Phase 4: Workflow System
- [ ] Workflow graph execution
- [ ] Executor implementations
- [ ] Edge routing (conditional, fan-out, fan-in)
- [ ] State management
- [ ] Checkpoint persistence

### Phase 5: Middleware System
- [ ] Middleware pipeline execution
- [ ] Built-in middleware (logging, retry, etc.)
- [ ] Middleware composition

### Phase 6: Memory & Context
- [ ] Context provider implementations
- [ ] Memory integrations
- [ ] Thread persistence

### Phase 7: Telemetry
- [ ] Complete OpenTelemetry integration
- [ ] Metrics collection
- [ ] Distributed tracing
- [ ] Activity tracking

### Phase 8: Testing & Documentation
- [ ] Unit tests
- [ ] Integration tests
- [ ] Example applications
- [ ] API documentation
- [ ] User guides

## Implementation Guidelines

### Code Style

Follow standard Go conventions:

1. **Naming**
   - Use `MixedCaps` or `mixedCaps` rather than underscores
   - Acronyms should be all uppercase (e.g., `ID`, `API`, `HTTP`)
   - Interface names: If interface has single method, use method name + "er" suffix (e.g., `Reader`, `Writer`)

2. **Comments**
   - Package-level comments in `doc.go` files
   - Exported types and functions must have doc comments
   - Comments should be complete sentences
   - Start with the name of the thing being described

3. **Error Handling**
   - Return errors as the last return value
   - Use `fmt.Errorf` with `%w` for error wrapping
   - Create custom error types for domain-specific errors

4. **Concurrency**
   - Use channels for streaming responses
   - Always close channels when done
   - Use `context.Context` for cancellation

### Directory Structure

```
pkg/
├── agent/           # Agent implementations
├── client/          # Chat client implementations
│   ├── openai/     # OpenAI-specific (future)
│   └── azure/      # Azure-specific (future)
├── message/         # Message and content types
├── thread/          # Thread implementations
├── tool/            # Tool implementations
├── workflow/        # Workflow engine
├── middleware/      # Middleware components
├── memory/          # Memory providers
├── telemetry/       # Observability
├── types/           # Common types
└── errors/          # Error types
```

### Testing Strategy

1. **Unit Tests**
   - Test files alongside implementation: `*_test.go`
   - Use table-driven tests
   - Mock external dependencies

2. **Integration Tests**
   - In `tests/` directory
   - Test real API interactions (with API keys)
   - Skip if API keys not available

3. **Examples**
   - Runnable examples in `examples/`
   - Should demonstrate real-world usage

### API Design Principles

1. **Context First**
   - All methods accepting context should have it as first parameter
   - Use `context.Context` for cancellation and deadlines

2. **Options Pattern**
   - Use config structs for constructors
   - Use options structs for method parameters
   - Make options optional where possible

3. **Streaming**
   - Return `iter.Seq2[*RunResponseUpdate, error]` for streaming
   - Handle context cancellation

4. **Errors**
   - Return `error` as last return value
   - Use custom error types for domain errors
   - Support error unwrapping

5. **Interfaces**
   - Keep interfaces small and focused
   - Accept interfaces, return structs

## Key Implementation Notes

### Agent Implementation

The `ChatAgent` should:
1. Combine instructions, thread history, and new messages
2. Call the chat client with appropriate options
3. Handle function calls if tools are provided
4. Update the thread with new messages
5. Return structured responses

### Client Implementation

Chat clients should:
1. Handle HTTP requests to provider APIs
2. Transform messages to provider format
3. Parse responses back to framework types
4. Support both blocking and streaming
5. Include retry logic for transient errors
6. Handle rate limiting

### Thread Management

Threads should:
1. Store conversation history
2. Support serialization/deserialization
3. Handle message truncation if needed
4. Support different storage backends

### Workflow Execution

The workflow engine should:
1. Build a directed graph from executors and edges
2. Validate the graph (no cycles, all references exist)
3. Execute nodes in correct order
4. Handle conditional edges
5. Support parallel execution
6. Checkpoint state for resumption

### Middleware Pipeline

Middleware should:
1. Execute in order (outermost first)
2. Support pre and post-processing
3. Allow short-circuiting
4. Preserve context

## Testing with Real APIs

### Environment Variables

```bash
export OPENAI_API_KEY="sk-..."
export AZURE_OPENAI_ENDPOINT="https://..."
export AZURE_OPENAI_API_KEY="..."
export AZURE_OPENAI_DEPLOYMENT_NAME="gpt-4o"
```

### Integration Test Example

```go
func TestOpenAIIntegration(t *testing.T) {
    apiKey := os.Getenv("OPENAI_API_KEY")
    if apiKey == "" {
        t.Skip("OPENAI_API_KEY not set")
    }

    client, err := client.NewOpenAIChatClient(client.OpenAIChatClientConfig{
        APIKey: apiKey,
        Model:  "gpt-4o-mini",
    })
    require.NoError(t, err)

    // Test implementation
}
```

## Documentation

### API Documentation

Use `godoc` comments:

```go
// Agent is the primary interface for all AI agents.
//
// An agent can process messages and generate responses using various
// backing implementations (chat clients, workflows, etc.).
type Agent interface {
    // Run executes the agent with the provided messages.
    //
    // The agent will process the messages in the context of the provided
    // thread (if any) and return a structured response.
    Run(ctx context.Context, thread AgentThread, options *RunOptions, messages ...*message.ChatMessage) (*RunResponse, error)
}
```

### Examples

Add examples that can be tested:

```go
func ExampleChatAgent() {
    // Example code here
    // Output:
    // Expected output
}
```

## Pull Request Guidelines

1. **One feature per PR**
2. **Include tests**
3. **Update documentation**
4. **Follow Go conventions**
5. **Run `go fmt`**
6. **Run `go vet`**
7. **Pass all tests**

## Resources

- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Effective Go](https://golang.org/doc/effective_go.html)
- [Go Blog](https://blog.golang.org/)
- [OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/)

## Getting Help

- Review the .NET and Python implementations for reference
- Check the architecture decision records (ADRs) in `docs/decisions/`
- Ask questions in GitHub discussions
- Review the API reference documentation
