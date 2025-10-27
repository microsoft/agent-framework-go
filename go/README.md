# Get Started with Microsoft Agent Framework for Go Developers

## Quick Install

Install the Agent Framework for Go using Go modules:

```bash
go get github.com/microsoft/agent-framework/go
```

This installs the core framework with support for multiple LLM providers, agent abstractions, workflows, and orchestration capabilities.

**Features included:**

- **Multi-Provider Support**: OpenAI, Azure OpenAI, and custom providers
- **Agent Abstraction**: Consistent API across different agent implementations
- **Conversation Threading**: Stateful conversations with persistence
- **Tool/Function Calling**: Extensible function and tool integration
- **Workflow Orchestration**: Graph-based multi-agent workflows
- **Middleware Pipeline**: Request/response processing and filtering
- **Memory & Context**: Pluggable memory and context providers
- **Observability**: OpenTelemetry integration for tracing and metrics
- **Streaming Support**: Real-time streaming responses

Supported Platforms:

- Go: 1.24+

## 1. Setup API Keys

Set as environment variables, or create a .env file at your project root:

```bash
OPENAI_API_KEY=sk-...
OPENAI_CHAT_MODEL_ID=gpt-4o-mini
...
AZURE_OPENAI_API_KEY=...
AZURE_OPENAI_ENDPOINT=...
AZURE_OPENAI_CHAT_DEPLOYMENT_NAME=...
...
AZURE_AI_PROJECT_ENDPOINT=...
AZURE_AI_MODEL_DEPLOYMENT_NAME=...
```

You can also override environment variables by explicitly passing configuration parameters to the chat client constructor:

```go
chatClient, err := client.NewAzureOpenAIChatClient(client.AzureOpenAIChatClientConfig{
    APIKey:         "your-api-key",
    Endpoint:       "https://your-resource.openai.azure.com/",
    DeploymentName: "your-deployment",
    APIVersion:     "2024-02-15-preview",
})
```

## 2. Create a Simple Agent

Create agents and invoke them directly:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/microsoft/agent-framework/go/pkg/agent"
    "github.com/microsoft/agent-framework/go/pkg/client"
)

func main() {
    myAgent := agent.NewChatAgent(agent.ChatAgentConfig{
        ChatClient: client.NewOpenAIChatClient(),
        Instructions: `
        1) A robot may not injure a human being...
        2) A robot must obey orders given it by human beings...
        3) A robot must protect its own existence...

        Give me the TLDR in exactly 5 words.
        `,
    })

    result, err := myAgent.Run(context.Background(), "Summarize the Three Laws of Robotics")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(result.Text())
    // Output: Protect humans, obey, self-preserve, prioritized.
}
```

## 3. Directly Use Chat Clients (No Agent Required)

You can use the chat client packages directly for advanced workflows:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/microsoft/agent-framework/go/pkg/client"
    "github.com/microsoft/agent-framework/go/pkg/message"
)

func main() {
    chatClient := client.NewOpenAIChatClient()

    messages := []message.ChatMessage{
        message.NewSystemMessage("You are a helpful assistant."),
        message.NewUserMessage("Write a haiku about Agent Framework."),
    }

    response, err := chatClient.GetResponse(context.Background(), messages)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(response.Messages[0].Text())

    /*
    Output:

    Agents work in sync,
    Framework threads through each task—
    Code sparks collaboration.
    */
}
```

## 4. Build an Agent with Tools and Functions

Enhance your agent with custom tools and function calling:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "math/rand"

    "github.com/microsoft/agent-framework/go/pkg/agent"
    "github.com/microsoft/agent-framework/go/pkg/client"
    "github.com/microsoft/agent-framework/go/pkg/tool"
)

// GetWeather gets the weather for a given location
func GetWeather(location string) string {
    conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
    temp := rand.Intn(21) + 10 // 10-30°C
    condition := conditions[rand.Intn(len(conditions))]
    return fmt.Sprintf("The weather in %s is %s with a high of %d°C.", location, condition, temp)
}

// GetMenuSpecials gets today's menu specials
func GetMenuSpecials() string {
    return `
    Special Soup: Clam Chowder
    Special Salad: Cobb Salad
    Special Drink: Chai Tea
    `
}

func main() {
    myAgent := agent.NewChatAgent(agent.ChatAgentConfig{
        ChatClient:   client.NewOpenAIChatClient(),
        Instructions: "You are a helpful assistant that can provide weather and restaurant information.",
        Tools: []tool.Tool{
            tool.NewFunctionTool("get_weather", "Get the weather for a given location", GetWeather),
            tool.NewFunctionTool("get_menu_specials", "Get today's menu specials", GetMenuSpecials),
        },
    })

    response, err := myAgent.Run(context.Background(), "What's the weather in Amsterdam and what are today's specials?")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(response.Text())

    /*
    Output:
    The weather in Amsterdam is sunny with a high of 22°C. Today's specials include
    Clam Chowder soup, Cobb Salad, and Chai Tea as the special drink.
    */
}
```

## 5. Multi-Agent Orchestration

Coordinate multiple agents to collaborate on complex tasks using orchestration patterns:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/microsoft/agent-framework/go/pkg/agent"
    "github.com/microsoft/agent-framework/go/pkg/client"
)

func main() {
    // Create specialized agents
    writer := agent.NewChatAgent(agent.ChatAgentConfig{
        Name:         "Writer",
        ChatClient:   client.NewOpenAIChatClient(),
        Instructions: "You are a creative content writer. Generate and refine slogans based on feedback.",
    })

    reviewer := agent.NewChatAgent(agent.ChatAgentConfig{
        Name:         "Reviewer",
        ChatClient:   client.NewOpenAIChatClient(),
        Instructions: "You are a critical reviewer. Provide detailed feedback on proposed slogans.",
    })

    // Sequential workflow: Writer creates, Reviewer provides feedback
    task := "Create a slogan for a new electric SUV that is affordable and fun to drive."

    // Step 1: Writer creates initial slogan
    initialResult, err := writer.Run(context.Background(), task)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Writer: %s\n", initialResult.Text())

    // Step 2: Reviewer provides feedback
    feedbackRequest := fmt.Sprintf("Please review this slogan: %s", initialResult.Text())
    feedback, err := reviewer.Run(context.Background(), feedbackRequest)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Reviewer: %s\n", feedback.Text())

    // Step 3: Writer refines based on feedback
    refinementRequest := fmt.Sprintf("Please refine this slogan based on the feedback: %s\nFeedback: %s",
        initialResult.Text(), feedback.Text())
    finalResult, err := writer.Run(context.Background(), refinementRequest)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Final Slogan: %s\n", finalResult.Text())

    // Example Output:
    // Writer: "Charge Forward: Affordable Adventure Awaits!"
    // Reviewer: "Good energy, but 'Charge Forward' is overused in EV marketing..."
    // Final Slogan: "Power Up Your Adventure: Premium Feel, Smart Price!"
}
```

**Note**: Advanced orchestration patterns like GroupChat, Sequential, and Concurrent orchestrations are coming soon.

## More Examples & Samples

- [Getting Started Examples](https://github.com/microsoft/agent-framework/tree/main/go/examples): Basic agent creation and tool usage
- [Chat Client Examples](https://github.com/microsoft/agent-framework/tree/main/go/examples/chat_client): Direct chat client usage patterns
- [Azure OpenAI Integration](https://github.com/microsoft/agent-framework/tree/main/go/pkg/client): Azure OpenAI integration
- [Workflow Samples](https://github.com/microsoft/agent-framework/tree/main/workflow-samples): Advanced multi-agent patterns

## Project Structure

```
go/
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

## Agent Framework Documentation

- [Agent Framework Repository](https://github.com/microsoft/agent-framework)
- [Go Package Documentation](https://pkg.go.dev/github.com/microsoft/agent-framework/go)
- [Python Package Documentation](https://github.com/microsoft/agent-framework/tree/main/python)
- [.NET Package Documentation](https://github.com/microsoft/agent-framework/tree/main/dotnet)
- [Design Documents](https://github.com/microsoft/agent-framework/tree/main/docs/design)
- Learn docs are coming soon.

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md) for contribution guidelines.

## License

See [LICENSE](../LICENSE) for license information.
