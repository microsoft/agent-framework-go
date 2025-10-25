# Agent Framework - Golang SDK

This directory contains the Go implementation of the Microsoft Agent Framework.

## Status

🚧 **Under Development** - This SDK is currently in early development and provides stub implementations for the core API surface.

## Structure

```
golang/
├── pkg/                    # Public packages
│   ├── agent/             # Agent abstractions (Agent, ChatAgent, RunResponse)
│   ├── client/            # Chat client implementations (OpenAI, Azure OpenAI)
│   ├── message/           # Message and content types
│   ├── thread/            # Conversation thread management
│   ├── tool/              # Tool and function calling
│   ├── workflow/          # Workflow orchestration
│   ├── middleware/        # Middleware pipeline
│   ├── memory/            # Memory and context providers
│   ├── telemetry/         # OpenTelemetry integration
│   ├── types/             # Common types and interfaces
│   └── errors/            # Error types
├── internal/              # Private implementation details
├── examples/              # Example applications
│   └── basic/            # Basic agent example
├── tests/                 # Integration tests
├── cmd/                   # Command-line tools
├── go.mod                 # Go module definition
└── README.md              # This file
```

## Core API Components

### 1. Agent Interface
```go
type Agent interface {
    ID() string
    Name() string
    Run(ctx, messages, thread, options) (*RunResponse, error)
    RunStream(ctx, messages, thread, options) (<-chan *RunResponseUpdate, <-chan error)
    GetNewThread() AgentThread
}
```

### 2. Chat Client Interface
```go
type ChatClient interface {
    GetResponse(ctx, messages, options) (*ChatResponse, error)
    GetStreamingResponse(ctx, messages, options) (<-chan *ChatResponseUpdate, <-chan error)
}
```

### 3. Message Types
- `ChatMessage` - Single message with role and contents
- `TextContent` - Plain text
- `DataContent` - Binary data
- `FunctionCallContent` - Function/tool calls
- `FunctionResultContent` - Function results

### 4. Thread Management
- `AgentThread` - Conversation state management
- `InMemoryThread` - In-memory implementation

### 5. Tools
- `Tool` interface for extensible functions
- `HostedCodeInterpreterTool`
- `HostedFileSearchTool`
- `HostedWebSearchTool`

### 6. Workflow
- Graph-based orchestration
- `Executor` nodes
- `Edge` connections
- Checkpoint support

### 7. Middleware
- `AgentMiddleware` - Intercept agent calls
- `FunctionMiddleware` - Intercept function calls
- `ChatMiddleware` - Intercept chat client calls

### 8. Telemetry
- OpenTelemetry integration
- Distributed tracing
- Metrics collection

## Example Usage

See `examples/basic/main.go` for a complete example.

```go
package main

import (
    "context"
    "github.com/microsoft/agent-framework/golang/pkg/agent"
    "github.com/microsoft/agent-framework/golang/pkg/client"
)

func main() {
    ctx := context.Background()
    
    // Create client
    chatClient, _ := client.NewOpenAIChatClient(client.OpenAIChatClientConfig{
        APIKey: "your-api-key",
        Model:  "gpt-4o-mini",
    })
    
    // Create agent
    myAgent := agent.NewChatAgent(agent.ChatAgentConfig{
        Name:         "Assistant",
        Instructions: "You are a helpful assistant.",
        ChatClient:   chatClient,
    })
    
    // Run agent
    response, _ := myAgent.Run(ctx, messages, nil, nil)
    println(response.Text())
}
```

## Development

This is currently a stub implementation. The following work is planned:

- [ ] Complete OpenAI client implementation
- [ ] Complete Azure OpenAI client implementation
- [ ] Workflow graph execution engine
- [ ] Checkpoint persistence
- [ ] Middleware pipeline execution
- [ ] Additional content types
- [ ] Comprehensive tests
- [ ] Documentation and examples

## Contributing

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for guidelines.

## License

See [LICENSE](../../LICENSE) for license information.
