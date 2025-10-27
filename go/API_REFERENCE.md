# Microsoft Agent Framework - Go SDK API Reference

This document provides a comprehensive reference for the Go SDK's public API surface.

## Table of Contents

1. [Core Concepts](#core-concepts)
2. [Agent API](#agent-api)
3. [Message API](#message-api)
4. [Thread API](#thread-api)
5. [Client API](#client-api)
6. [Tool API](#tool-api)
7. [Workflow API](#workflow-api)
8. [Middleware API](#middleware-api)
9. [Memory API](#memory-api)
10. [Telemetry API](#telemetry-api)

---

## Core Concepts

### Package Structure

- `pkg/agent` - Agent abstractions and implementations
- `pkg/message` - Message and content types
- `pkg/thread` - Conversation thread management
- `pkg/client` - Chat client implementations
- `pkg/tool` - Tool and function calling
- `pkg/workflow` - Workflow orchestration
- `pkg/middleware` - Middleware pipeline
- `pkg/memory` - Memory and context providers
- `pkg/telemetry` - OpenTelemetry integration
- `pkg/types` - Common types and enums
- `pkg/errors` - Error types

---

## Agent API

### Agent Interface

```go
type Agent interface {
    ID() string
    Name() string
    DisplayName() string
    Description() string
    Run(ctx context.Context, messages []*message.ChatMessage, thread AgentThread, options *RunOptions) (*RunResponse, error)
    RunStream(ctx context.Context, messages []*message.ChatMessage, thread AgentThread, options *RunOptions) iter.Seq2[*RunResponseUpdate, error]
    GetNewThread() AgentThread
    DeserializeThread(data map[string]interface{}) (AgentThread, error)
    GetService(serviceType string, serviceKey interface{}) (interface{}, error)
}
```

### ChatAgent

Basic agent implementation using a chat client.

```go
type ChatAgentConfig struct {
    Name          string
    Description   string
    Instructions  string
    ChatClient    client.ChatClient
    Tools         []tool.Tool
    ThreadFactory func() thread.AgentThread
}

func NewChatAgent(config ChatAgentConfig) *ChatAgent
```

### RunOptions

```go
type RunOptions struct {
    Temperature              *float64
    TopP                     *float64
    MaxTokens                *int
    StopSequences            []string
    ToolMode                 types.ToolMode
    ResponseFormat           map[string]interface{}
    AllowBackgroundResponses bool
    ContinuationToken        interface{}
    AdditionalProperties     map[string]interface{}
}
```

### RunResponse

```go
type RunResponse struct {
    Messages             []*message.ChatMessage
    AgentID              string
    ResponseID           string
    ContinuationToken    interface{}
    CreatedAt            *types.Time
    Usage                *types.UsageDetails
    RawRepresentation    interface{}
    AdditionalProperties map[string]interface{}
}

func (r *RunResponse) Text() string
func (r *RunResponse) ToUpdates() []*RunResponseUpdate
```

---

## Message API

### ChatMessage

```go
type ChatMessage struct {
    Role                 types.Role
    Contents             []Content
    AuthorName           string
    MessageID            string
    AdditionalProperties map[string]interface{}
}

func NewChatMessage(role types.Role, content string) *ChatMessage
func (m *ChatMessage) Text() string
func (m *ChatMessage) AddContent(content Content)
func (m *ChatMessage) Serialize() (map[string]interface{}, error)
```

### Content Types

All content types implement the `Content` interface:

```go
type Content interface {
    Type() string
    Serialize() (map[string]interface{}, error)
}
```

**Available Content Types:**

- `TextContent` - Plain text
- `DataContent` - Binary data with media type
- `URIContent` - URI reference
- `FunctionCallContent` - Function/tool call
- `FunctionResultContent` - Function execution result
- `ErrorContent` - Error information
- `UsageContent` - Resource usage
- `HostedFileContent` - Hosted file reference
- `HostedVectorStoreContent` - Vector store reference
- `TextReasoningContent` - Reasoning/thinking output

---

## Thread API

### AgentThread Interface

```go
type AgentThread interface {
    GetMessages(ctx context.Context) ([]*message.ChatMessage, error)
    AddMessages(ctx context.Context, messages []*message.ChatMessage) error
    Serialize() (map[string]interface{}, error)
    Deserialize(data map[string]interface{}) error
    GetService(serviceType string, serviceKey interface{}) (interface{}, error)
}
```

### InMemoryThread

```go
func NewInMemoryThread() *InMemoryThread
```

---

## Client API

### ChatClient Interface

```go
type ChatClient interface {
    GetResponse(ctx context.Context, messages []*message.ChatMessage, options *ChatOptions) (*message.ChatResponse, error)
    GetStreamingResponse(ctx context.Context, messages []*message.ChatMessage, options *ChatOptions) iter.Seq2[*RunResponseUpdate, error]
}
```

### OpenAIChatClient

```go
type OpenAIChatClientConfig struct {
    APIKey   string
    Model    string
    Endpoint string
}

func NewOpenAIChatClient(config OpenAIChatClientConfig) (*OpenAIChatClient, error)
```

### AzureOpenAIChatClient

```go
type AzureOpenAIChatClientConfig struct {
    APIKey         string
    Endpoint       string
    DeploymentName string
    APIVersion     string
}

func NewAzureOpenAIChatClient(config AzureOpenAIChatClientConfig) (*AzureOpenAIChatClient, error)
```

### ChatOptions

```go
type ChatOptions struct {
    ModelID              string
    Temperature          *float64
    TopP                 *float64
    MaxTokens            *int
    StopSequences        []string
    Tools                []tool.Tool
    ToolMode             types.ToolMode
    ResponseFormat       map[string]interface{}
    AdditionalProperties map[string]interface{}
}
```

---

## Tool API

### Tool Interface

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}
    Invoke(ctx context.Context, arguments map[string]interface{}) (interface{}, error)
}
```

### Function

```go
type FunctionConfig struct {
    Name        string
    Description string
    Parameters  map[string]interface{}
    Handler     func(context.Context, map[string]interface{}) (interface{}, error)
}

func NewFunction(config FunctionConfig) *Function
```

### Hosted Tools

- `HostedCodeInterpreterTool` - Code execution
- `HostedFileSearchTool` - File/vector search
- `HostedWebSearchTool` - Web search

---

## Workflow API

### Workflow Interface

```go
type Workflow interface {
    Run(ctx context.Context, input interface{}, options *RunOptions) (*RunResult, error)
    RunStream(ctx context.Context, input interface{}, options *RunOptions) iter.Seq2[*RunResponseUpdate, error]
}
```

### WorkflowBuilder

```go
func NewWorkflowBuilder() *WorkflowBuilder
func (b *WorkflowBuilder) AddExecutor(executor Executor) *WorkflowBuilder
func (b *WorkflowBuilder) AddEdge(edge Edge) *WorkflowBuilder
func (b *WorkflowBuilder) Build() (Workflow, error)
```

### Executor Interface

```go
type Executor interface {
    ID() string
    Handle(ctx context.Context, input interface{}, context WorkflowContext) (interface{}, error)
}
```

### CheckpointStorage

```go
type CheckpointStorage interface {
    Save(ctx context.Context, runID string, checkpoint *Checkpoint) error
    Load(ctx context.Context, runID string, checkpointID string) (*Checkpoint, error)
    List(ctx context.Context, runID string) ([]*CheckpointInfo, error)
}
```

---

## Middleware API

### AgentMiddleware

```go
type AgentMiddleware interface {
    OnInvoking(ctx context.Context, agentCtx *AgentContext, next func(context.Context, *AgentContext) error) error
    OnInvoked(ctx context.Context, agentCtx *AgentContext, next func(context.Context, *AgentContext) error) error
}
```

### FunctionMiddleware

```go
type FunctionMiddleware interface {
    OnInvoking(ctx context.Context, funcCtx *FunctionContext, next func(context.Context, *FunctionContext) error) error
    OnInvoked(ctx context.Context, funcCtx *FunctionContext, next func(context.Context, *FunctionContext) error) error
}
```

### ChatMiddleware

```go
type ChatMiddleware interface {
    OnInvoking(ctx context.Context, chatCtx *ChatContext, next func(context.Context, *ChatContext) error) error
    OnInvoked(ctx context.Context, chatCtx *ChatContext, next func(context.Context, *ChatContext) error) error
}
```

---

## Memory API

### ContextProvider

```go
type ContextProvider interface {
    GetContext(ctx context.Context, input interface{}) (*Context, error)
}

func NewAggregateContextProvider(providers ...ContextProvider) *AggregateContextProvider
```

### MessageStore

```go
type MessageStore interface {
    GetMessages(ctx context.Context) ([]interface{}, error)
    AddMessages(ctx context.Context, messages []interface{}) error
    Clear(ctx context.Context) error
}

func NewInMemoryMessageStore() *InMemoryMessageStore
```

---

## Telemetry API

### Tracing

```go
func StartAgentSpan(ctx context.Context, operationName string, agentID, agentName string) (context.Context, trace.Span)
```

### Metrics

```go
func RecordUsage(ctx context.Context, inputTokens, outputTokens, totalTokens int64)
func RecordAgentInvocation(ctx context.Context, agentID string, success bool)
func RecordToolInvocation(ctx context.Context, toolName string, durationMs float64, success bool)
```

### Attributes

```go
const (
    AttrAgentID       = attribute.Key("agent.id")
    AttrAgentName     = attribute.Key("agent.name")
    AttrModelID       = attribute.Key("model.id")
    AttrOperationType = attribute.Key("operation.type")
    AttrToolName      = attribute.Key("tool.name")
    AttrInputTokens   = attribute.Key("usage.input_tokens")
    AttrOutputTokens  = attribute.Key("usage.output_tokens")
    AttrTotalTokens   = attribute.Key("usage.total_tokens")
)
```

---

## Common Types

### Enums

```go
type Role string
const (
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleSystem    Role = "system"
    RoleTool      Role = "tool"
)

type FinishReason string
const (
    FinishReasonStop          FinishReason = "stop"
    FinishReasonLength        FinishReason = "length"
    FinishReasonToolCalls     FinishReason = "tool_calls"
    FinishReasonContentFilter FinishReason = "content_filter"
    FinishReasonError         FinishReason = "error"
)

type ToolMode string
const (
    ToolModeAuto     ToolMode = "auto"
    ToolModeRequired ToolMode = "required"
    ToolModeNone     ToolMode = "none"
)
```

### UsageDetails

```go
type UsageDetails struct {
    InputTokens          int64
    OutputTokens         int64
    TotalTokens          int64
    AdditionalProperties map[string]interface{}
}
```

---

## Error Types

```go
type AgentExecutionError
func NewAgentExecutionError(agentID, message string, cause error) *AgentExecutionError

type AgentInitializationError
func NewAgentInitializationError(message string, cause error) *AgentInitializationError

type ToolError
func NewToolError(toolName, message string, cause error) *ToolError

type ContentError
func NewContentError(contentType, message string, cause error) *ContentError

type WorkflowValidationError
func NewWorkflowValidationError(message string, validationErrors []string) *WorkflowValidationError

type ThreadError
func NewThreadError(threadID, message string, cause error) *ThreadError
```

---

## Design Patterns

### Context Pattern

All methods accept `context.Context` as the first parameter for cancellation and deadlines.

### Error Handling

All errors can be unwrapped using `errors.Unwrap()` to access the underlying cause.

---

## Future Additions

The following components are planned:

- Additional content types
- More client implementations (Anthropic, Google, etc.)
- Advanced workflow patterns (sequential, concurrent, fan-in/out)
- Persistent checkpoint storage implementations
- Advanced middleware examples
- Integration with external memory systems
