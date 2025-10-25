# Microsoft Agent Framework - Go SDK

Go implementation of the Microsoft Agent Framework for building AI agents with support for multiple LLM providers, workflows, and orchestration.

## Overview

This SDK provides a comprehensive framework for building, orchestrating, and deploying AI agents in Go, with feature parity to the .NET and Python implementations.

## Features

- **Multi-Provider Support**: OpenAI, Azure OpenAI, and custom providers
- **Agent Abstraction**: Consistent API across different agent implementations
- **Conversation Threading**: Stateful conversations with persistence
- **Tool/Function Calling**: Extensible function and tool integration
- **Workflow Orchestration**: Graph-based multi-agent workflows
- **Middleware Pipeline**: Request/response processing and filtering
- **Memory & Context**: Pluggable memory and context providers
- **Observability**: OpenTelemetry integration for tracing and metrics
- **Streaming Support**: Real-time streaming responses

## Installation

```bash
go get github.com/microsoft/agent-framework/golang
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"
    
    "github.com/microsoft/agent-framework/golang/pkg/agent"
    "github.com/microsoft/agent-framework/golang/pkg/client"
)

func main() {
    // Create a chat client
    chatClient, err := client.NewOpenAIChatClient(client.OpenAIChatClientConfig{
        APIKey: "your-api-key",
        Model:  "gpt-4o-mini",
    })
    if err != nil {
        log.Fatal(err)
    }
    
    // Create an agent
    myAgent := agent.NewChatAgent(agent.ChatAgentConfig{
        Name:         "Assistant",
        Instructions: "You are a helpful assistant.",
        ChatClient:   chatClient,
    })
    
    // Run the agent
    response, err := myAgent.Run(context.Background(), "Write a haiku about Go programming")
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Println(response.Text())
}
```

## Project Structure

```
golang/
├── pkg/              # Public API packages
│   ├── agent/        # Agent abstractions and implementations
│   ├── thread/       # Conversation thread management
│   ├── message/      # Message and content types
│   ├── client/       # Chat client implementations
│   ├── tool/         # Tool and function calling
│   ├── workflow/     # Workflow orchestration
│   ├── middleware/   # Middleware pipeline
│   ├── memory/       # Memory and context providers
│   ├── telemetry/    # Observability and tracing
│   ├── types/        # Common types and interfaces
│   └── errors/       # Error types
├── internal/         # Private implementation details
├── examples/         # Example applications
├── tests/            # Integration tests
└── cmd/              # Command-line tools
```

## Documentation

- [Agent Guide](docs/agents.md)
- [Workflow Guide](docs/workflows.md)
- [Tools & Functions](docs/tools.md)
- [API Reference](https://pkg.go.dev/github.com/microsoft/agent-framework/golang)

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md) for contribution guidelines.

## License

See [LICENSE](../LICENSE) for license information.
