# .NET and Go SDK Feature Comparison

Date: May 14, 2026

This document compares the .NET SDK at `microsoft/agent-framework/dotnet` with the Go SDK in this repository. It is based on the package, sample, and public API inventory present on this date.

## Status Legend

| Status | Meaning |
| --- | --- |
| Aligned | The Go SDK has the same feature category and broadly equivalent behavior. |
| Partial | The Go SDK has the core concept, but the API shape, integrations, storage, hosting, samples, or provider coverage differ. |
| .NET only | The feature exists in the .NET SDK and no equivalent was found in the Go SDK. |
| Go only | The feature exists in the Go SDK and no equivalent first-class .NET package was found in the inspected surface. |

## Executive Summary

The Go SDK covers the core agent and workflow model: agents, sessions, history, context providers, streaming response updates, structured output, function tools, shell execution with environment-aware context, tool auto-calling and approvals, initial harness utilities, A2A, AGUI, MCP, skills, compaction, in-process workflows, checkpoint/resume, human-in-the-loop request ports, state, and workflow-as-agent/agent-in-workflow adapters.

The .NET SDK has a much wider integration and product layer. The largest gaps are DevUI/Aspire, evaluation, declarative agents/workflows, durable agents/workflows, Azure Functions and ASP.NET hosting integrations, OpenAI-compatible hosting, Foundry and Azure AI Persistent agents, Copilot Studio, GitHub Copilot, Mem0, Cosmos DB storage, Purview, RAG, remaining harness utilities such as file access/memory/store and subagents, richer sample coverage, and source-generator/declarative workflow tooling.

Within overlapping features, the main misalignments are API shape and ecosystem integration. .NET is centered on `Microsoft.Extensions.AI` types (`AIAgent`, `AgentRunOptions`, `ChatMessage`, `AIContent`, `AIFunction`, `AITool`, dependency injection, ASP.NET, Durable Task). Go has idiomatic packages and interfaces (`agent.Agent`, `agent.Option`, `message.Message`, `message.Content`, `tool.Tool`, `workflow.Builder`) with less framework hosting and fewer service-specific adapters.

## Feature Matrix

| Feature area | .NET SDK | Go SDK | Status | Misalignment |
| --- | --- | --- | --- | --- |
| Core agent abstraction | `AIAgent`, `DelegatingAIAgent`, `AgentRunOptions`, `AgentResponse`, `AgentResponseUpdate`, current run context, metadata, typed structured responses. | `agent.Agent`, `agent.ProviderConfig`, `agent.Config`, `agent.Option`, `Response`, `ResponseUpdate`, `Run`, `RunText`, `RunMessage`, `ResponseStream`. | Aligned | .NET exposes extension-method adapters around `Microsoft.Extensions.AI`; Go uses a provider `RunFunc` contract and package-level option wrappers. |
| Agent identity and metadata | `AIAgentMetadata`, agent ID/name/description, source attribution extensions. | Agent ID/name/description, provider name, response author stamping. | Partial | Go does not expose the same request source attribution helpers as .NET. |
| Sessions | `AgentSession`, `AgentSessionStateBag`, session serialization helpers, provider session state. | `agent.Session`, marshal/unmarshal hooks, provider session hooks, local/service ID support. | Aligned | .NET has a richer typed state bag and extension helpers; Go stores provider/session values through its own session abstraction. |
| Chat history | `ChatHistoryProvider`, `InMemoryChatHistoryProvider`, per-service-call persistence, reducer triggers. | `HistoryProvider`, default in-memory history for local sessions, third-party storage example. | Partial | Go has the core lifecycle but fewer built-in storage providers and no first-class reducer trigger options on the history provider. |
| Context providers and memory injection | `AIContextProvider`, `MessageAIContextProvider`, provider invoking/invoked lifecycle. | `agent.ContextProvider`, `(*agent.ContextProvider).Middleware`, before/after lifecycle. | Aligned | .NET context providers are integrated with `Microsoft.Extensions.AI`; Go providers directly transform `message.Message` slices and options. |
| Memory integrations | Chat history memory, bounded chat history, Mem0, Foundry memory, RAG samples, file memory. | In-memory history/context examples and custom context providers. | .NET only | Go has primitives to build memory, but no Mem0, Foundry memory, RAG, bounded memory package, or file memory provider. |
| Compaction and chat reduction | Compaction provider, triggers, message index/groups, sliding window, context window, truncation, summarization, tool-result, pipeline, chat reducer adapter. | `agent/compaction` provider, triggers, message index/groups, sliding window, `ContextWindowStrategy`, truncation, summarization, tool-result, pipeline. | Aligned | `IChatReducer` is a .NET-only abstraction that does not exist in Go; all compaction strategies now align. |
| Structured output | Typed `AgentResponse<T>`, structured output options, provider adapters. | `WithStructuredOutput`, `ResponseFormat`, provider `Format`/`Unmarshal` hooks, `agent/format/jsonformat`, typed JSON schema helpers. | Aligned | .NET response typing is part of the response type; Go uses options and provider-declared structured output support. |
| JSON schema/format helpers | Uses `AIJsonUtilities`, `JsonSerializerOptions`, schema helpers through extensions and tools. | `jsonformat.New`, `Any`, `Nothing`, `For[T]`, `MustFor[T]`, `ForType`, validation/normalization. | Partial | Go has a dedicated JSON format package; .NET leans on platform JSON and MEAI tool/function metadata. |
| Message/content model | `ChatMessage`, `AIContent`, text/data/error/function call/function result/hosted file/vector store/reasoning/code interpreter and durable state wrappers. | `message.Message`, `Content`, text/data/error/function call/function result/hosted file/vector store/reasoning/URI/usage/approval/code interpreter content. | Aligned | Type names and serialization are not interchangeable. Go has its own content model rather than using MEAI. |
| Annotations/citations | MEAI annotation/content support through `AIContent`. | `message.Annotation`, citation annotations, annotated text spans. | Aligned | No direct binary compatibility; mapping is provider-specific. |
| Function tools | `AIFunction`, `AITool`, function tools, plugins, dynamic function tools, tool argument matching in evals. | `tool.Tool`, `tool.FuncTool`, `functool.New`, typed input/output schemas. | Partial | Go has typed function tools but no first-class plugin or dynamic tool sample equivalent to .NET steps 12 and 20. |
| Shell tool and environment context | `Microsoft.Agents.AI.Tools.Shell`: `LocalShellExecutor`, `ShellPolicy` (allow/deny-list), `ShellResult`, stateless and persistent shell execution modes, approval-in-the-loop gate, head-tail output truncation, `ShellEnvironmentProvider`, `ShellEnvironmentSnapshot`, shell-family instructions, common CLI probing. | `tool/shelltool.NewLocal`, `shelltool.LocalConfig` (mode, timeout, max output, policy, acknowledge unsafe), `shelltool.Policy`, `shelltool.Result.FormatForModel`, `shelltool.Executor`, `shelltool.NewEnvironmentProvider`, `EnvironmentProviderConfig`, `ShellEnvironmentSnapshot`, `DefaultShellEnvironmentInstructions`. | Aligned | Go mirrors the .NET design for local execution, policy allow/deny-list, approval-required by default, stateless/persistent modes, output truncation, environment snapshot probing, cached first-probe behavior, refresh, current snapshot access, shell-family prompt instructions, invalid/duplicate probe handling, stderr version fallback, caller cancellation, and probe timeout handling. Docker shell executor not ported (Go has no equivalent `DockerShellExecutor`). Go represents tool-version nullability with `ToolVersion{Found bool}` rather than nullable strings. |
| Tool auto-calling | Provider/tool-call loop, tool approval agent, and message injection during the function loop (`EnableMessageInjection` / `MessageInjectingChatClient`). | `agent/harness/toolautocall`, default provider middleware unless disabled. Message injection supported via `Config.EnableMessageInjection` and `toolautocall.MessageInjectorFromContext(ctx)`. | Aligned | Go implements auto-call as explicit middleware; .NET uses agent/tool abstractions and provider adapters. |
| Tool approval | Tool approval request/response content, tool approval agent and builder extensions, auto-approval rules (heuristics). | `message.ToolApprovalRequestContent`, `message.ToolApprovalResponseContent`, `tool.ApprovalRequiredFunc`, `agent/harness/toolautocall` approval flow, `agent/harness/toolapproval` middleware for standing-rule and auto-approval-rule approval management, AGUI HITL sample. | Aligned | API shape differs: .NET uses a `ToolApprovalAgent` delegating-agent wrapper with `ToolApprovalAgentOptions`; Go uses idiomatic middleware (`toolapproval.New(toolapproval.Config{AutoApprovalRules: ...})`). Standing approval rules, queued-request batching, `AlwaysApprove*` response content, and auto-approval rules (heuristics) are now present in both SDKs. |
| Hosted/server-side tools | Foundry/OpenAI samples for code interpreter, file search, web search, OpenAPI, Bing custom search, SharePoint, Microsoft Fabric, memory search, Toolbox, hosted MCP. | `tool/hostedtool` declarations for web search, file search, code interpreter, MCP server. | Partial | Go has declaration types but less provider/sample coverage and fewer service-specific hosted tool integrations. |
| Agent as function tool | Agents can be converted/bound as tools in samples and workflow builders. | `tool/agenttool.New` wraps an agent as a `FuncTool`. | Aligned | API shape differs; Go exposes a direct package. |
| Agent as MCP tool/server | .NET sample `Agent_Step07_AsMcpTool` and durable sample for agent as MCP tool. | `tool/mcptool.AddTool`, `examples/02-agents/mcp/agent_mcp_server`, `step10_as_mcp_tool`. | Aligned | Durable MCP hosting is .NET only. |
| MCP client tools | .NET hosted MCP and MCP declarative workflow packages/samples. | `mcptool.Connect`, `ListTools`, wrappers over MCP client sessions. | Partial | Go has the basic MCP client/tool bridge; .NET has more hosting/declarative samples. |
| Skills | File-based, inline/code-defined, class-based skills, resources, scripts, DI-backed skills. | `agent/skills`, `fsskills`, file-based, in-memory/code-defined, mixed skills, resources, scripts with script runners. | Partial | Go lacks class-based skill reflection and DI skill support. |
| OpenAI provider | OpenAI Chat Completions, Responses, Azure OpenAI, background responses, code interpreter file download samples. | `openaiagent.NewChatCompletions`, `NewResponses`, Azure OpenAI through the OpenAI Go Azure client, continuation/background response support. | Partial | Go covers chat/responses but has fewer samples for background responses and hosted tools. |
| Anthropic provider | Anthropic packages and reasoning/skills/function-tool samples. | `anthropicagent.New`, message params option. | Partial | Go has provider support, but sample coverage is smaller. |
| Gemini/provider ecosystem | Google Gemini sample through provider adapters. | `geminiagent.New`, generate content config option. | Aligned | .NET reaches more providers through generic `IChatClient` adapters; Go has a direct Gemini package. |
| A2A agent client | `Microsoft.Agents.AI.A2A`, card/client extensions, task/session support. | `agent/provider/a2aagent`, task ID option, task ID/session helpers. | Aligned | Go exposes task IDs through options/session helpers; .NET exposes extension methods over A2A clients/cards/resolvers. |
| A2A hosting | A2A hosting packages, ASP.NET Core hosting, samples. | `agent/hosting/a2ahosting`, JSON-RPC and JSON HTTP handlers, end-to-end client/server sample. | Partial | Go has HTTP handlers but not ASP.NET-style hosting/DI integration. |
| AGUI agent/client | AGUI chat client and shared conversions. | `agent/provider/aguiagent`, AGUI SSE client integration. | Aligned | Type models differ but feature categories line up. |
| AGUI hosting | ASP.NET Core AGUI hosting, end-to-end web chat samples. | `agent/hosting/aguihosting`, JSON HTTP handler, backend/frontend tools, HITL, state examples, reasoning event emission. | Partial | Go has handlers and examples, but no ASP.NET/Blazor-style end-to-end web app equivalent. |
| Azure AI Persistent agents | `Microsoft.Agents.AI.AzureAI.Persistent`, lifecycle and persistent conversation samples. | No equivalent package. | .NET only | Go currently uses OpenAI/Azure OpenAI clients, not Azure AI Persistent Agents. |
| Foundry agents and hosted agents | `Microsoft.Agents.AI.Foundry`, `Foundry.Hosting`, Foundry agent lifecycle and hosted agent samples. | No equivalent Foundry package. | .NET only | Go lacks Foundry hosted agent lifecycle, invocation, and server-side tool hosting integrations. |
| Copilot Studio | `Microsoft.Agents.AI.CopilotStudio`. | No equivalent package. | .NET only | No Go connector found. |
| GitHub Copilot | `Microsoft.Agents.AI.GitHub.Copilot`. | No equivalent package. | .NET only | No Go connector found. |
| Generic chat-client adapter | `IChatClient.AsAIAgent`, `ChatClientBuilder.BuildAIAgent`, any MEAI chat client including Ollama/ONNX/custom samples. | Custom providers can be built with `agent.ProviderConfig`, but no generic MEAI-style chat client ecosystem. | Partial | Go can implement custom providers but lacks a shared cross-provider chat-client abstraction comparable to MEAI. |
| Agent hosting base | `Microsoft.Agents.AI.Hosting` and service registration patterns. | A2A, AGUI, workflow hosting packages. | Partial | Go has targeted host adapters; .NET has broader hosting infrastructure. |
| OpenAI-compatible hosting | `Microsoft.Agents.AI.Hosting.OpenAI` for Chat Completions, Responses, Conversations models/converters/streaming. | No equivalent package. | .NET only | Go does not expose agents through OpenAI-compatible HTTP APIs. |
| Azure Functions hosting | `Microsoft.Agents.AI.Hosting.AzureFunctions`. | No equivalent package. | .NET only | Go has no Azure Functions hosting adapter. |
| ASP.NET Core hosting | A2A/AGUI/DevUI/authorization samples and endpoint extensions. | `net/http` handlers for A2A and AGUI. | Partial | Go provides handlers but no framework-specific web host integration. |
| DevUI | `Microsoft.Agents.AI.DevUI`, endpoint/service extensions, DevUI samples. | No DevUI package. | .NET only | Go README mentions DevUI at framework level, but no Go DevUI implementation was found. |
| Aspire integration | `Aspire.Hosting.AgentFramework.DevUI`, Aspire dashboard samples. | No equivalent package. | .NET only | No Go Aspire integration. |
| Dependency injection | Agent and skill samples using `Microsoft.Extensions.DependencyInjection`; service collection extensions in multiple packages. | Idiomatic construction/config examples, no DI framework package. | Partial | Go does not attempt to mirror .NET DI. |
| Agent middleware/delegation | `DelegatingAIAgent`, builder extensions, tool approval agent, chat client pipeline integration. | `agent.Middleware`, `MiddlewareFunc`, automatic run logging, automatic provider-backed structured output, `agent/opentelemetry`, `(*agent.ContextProvider).Middleware`, harness auto-call and tool-approval middleware. | Aligned | API shape differs: .NET exposes delegating agents and builder extensions; Go exposes direct run-pipeline middleware. |
| Logging | Microsoft.Extensions.Logging source-generated logs. | `slog` logger support through `agent.Config.Logger`, automatic agent run logs, and provider/middleware diagnostics. | Partial | Logging ecosystems differ. |
| OpenTelemetry for agents | Agent/workflow observability samples and OpenTelemetry workflow builder extension. | `agent/opentelemetry`, `workflow/observability/opentelemetry`, workflow builder instrumentation via `WithTelemetry`, trace context propagation in workflow context. | Aligned | API shape differs: Go passes a tracer from the OpenTelemetry adapter separately from `TelemetryOptions` and keeps workflow observability internals unexported. |
| Evaluation | Agent evaluation extensions, eval checks, local/function evaluators, conversation splitters, workflow evaluation samples, Foundry quality samples. | No evaluation package. | .NET only | No Go equivalent found. |
| Harness utilities | Agent mode, file access, file memory, file store, subagents, todo, tool approval harness providers. | `agent/harness/agentmode`, `agent/harness/todo`, `agent/harness/toolapproval`, `agent/harness/toolautocall`. | Partial | Go now has packaged harness support for agent mode, todo tracking, tool approval, and tool auto-call. It still lacks file access, file memory, file store, and subagent harness utilities. |
| RAG | Basic text RAG, custom vector store RAG, custom data source RAG, Foundry service RAG, Neo4j graph RAG samples. | No RAG package or sample found. | .NET only | Go has data/file/vector content types but no RAG workflow package or samples. |
| Purview | `Microsoft.Agents.AI.Purview` models and end-to-end sample. | No equivalent package. | .NET only | No Go governance/Purview integration. |
| Cosmos DB storage | Cosmos chat history provider and workflow checkpoint store. | No built-in Cosmos package. | .NET only | Go only exposes in-memory workflow checkpointing publicly. |
| Agent workflow builders | Sequential, concurrent, handoff, group chat builders. | `workflowhosting.BuildSequential`, `workflowhosting.BuildConcurrent`; manual builder plus `AddChain`, `AddSwitch`, direct/fan-out/fan-in edges; workflow-as-agent and agents-in-workflows examples. | Partial | Go now has first-class sequential and concurrent builders matching .NET's `AgentWorkflowBuilder.BuildSequential`/`BuildConcurrent`. Handoff and group chat builders are not yet implemented. |
| Workflow graph builder | `WorkflowBuilder`, direct edges, fan-out, fan-in barrier, labels, conditions, switch/case samples. | `workflow.Builder`, `AddEdge`, `AddDirectEdge`, `AddFanOutEdge`, `AddFanInBarrierEdge`, `WithEdgeLabel`, `WithEdgeAssigner`, `AddSwitch`. | Aligned | .NET has more overloads/extension methods; Go uses simpler methods and option functions. |
| Workflow executor model | Generic `Executor<TInput>` and `Executor<TInput,TOutput>`, function executors, aggregating executor, protocol builder. | `Executor`, `NewExecutor`, `Executor.Bind`, `Executor.Extend`, `RouteBuilder`, `StatefulExecutorCache`. | Partial | .NET has more overloads and an explicit `AggregatingExecutor`; Go mirrors .NET's executor-level cross-run declaration and binding-level concurrent-run gate, while route configuration and lifecycle hooks live on `Executor`. |
| Workflow protocol description | Accepts/yields/sends/catch-all protocol descriptor and chat protocol helpers. | `ProtocolDescriptor` exposes accepted, yielded, and sent types plus catch-all acceptance; `messageworkflow.Configure` contributes chat-message protocol metadata. | Aligned | Go now exposes the same protocol shape while keeping chat helpers in the Go-specific message workflow adapter. |
| Workflow execution modes | In-process OffThread, Concurrent, Lockstep; durable execution in separate package. | In-process OffThread, Concurrent, Lockstep; internal Subworkflow execution mode. | Partial | Durable execution is .NET only; Go has subworkflow internals without a full public binding helper. |
| Workflow streaming and runs | `Run`, `StreamingRun`, open/run/resume streaming, run to halt, status, events. | `inproc.Run`, `StreamingRun`, `Run`, `RunStreaming`, `Resume`, `ResumeStreaming`, `WatchStream`, `WatchUntilHalt`, status. | Aligned | Naming differs (`SessionId` in .NET vs `SessionID` in Go). |
| Workflow checkpointing | In-memory and JSON checkpoint managers, custom stores, Cosmos store, checkpoint restore, checkpoint hooks. | In-memory checkpoint manager (`checkpoint.NewInMemoryManager`), JSON+file checkpoint manager (`checkpoint.NewJSONManager` with `checkpoint.FileSystemJSONStore`), custom store interface (`checkpoint.Store[json.RawMessage]`), `WithCheckpointing`, checkpoint restore, checkpoint hooks, resume pending request republish, checkpoint-and-rehydrate example. | Partial | Go lacks a Cosmos store. Custom durable stores can be implemented via the public `checkpoint.Store[json.RawMessage]` interface. |
| Workflow state | Shared/private scoped state, state update lifecycle, stateful executors. | Scoped state, `ReadState`, `ReadOrInitState`, `ReadStateKeys`, `QueueStateUpdate`, `ScopeID`, `ScopeKey`, state checkpointing. | Aligned | API naming and state store extensibility differ. |
| Workflow external requests/HITL | Request ports, external requests/responses, human-in-the-loop samples, wrapped request support. | `RequestPort`, `RequestPort.Bind`, `ExternalRequest`, `ExternalResponse`, `PostRequest`, HITL sample, pending request republish. | Partial | Go supports the core flow but lacks .NET's broader wrapped-request/host integration surface. |
| Agent in workflow | `AIAgentBinding`, `AIAgentHostOptions`, response/update events, role reassignment, message forwarding, intercept user-input/function-call requests. | `agent/hosting/workflowhosting.New` with `Config`: update/response events, message forwarding toggle (`DisableForwardIncomingMessages`), role reassignment toggle (`DisableReassignOtherAgentsAsUsers`), `InterceptUserInputRequests`, `InterceptUnterminatedFunctionCalls`. | Aligned | API shape differs: .NET uses positive-boolean defaults (`ForwardIncomingMessages = true`, `ReassignOtherAgentsAsUsers = true`); Go uses opt-out booleans (`DisableForwardIncomingMessages`, `DisableReassignOtherAgentsAsUsers`). Feature coverage is equivalent. |
| Workflow as agent | Workflow host agent / `AsAIAgent`, sample. | `agent/provider/workflowprovider.New`. | Aligned | Go chooses in-process environment based on concurrency; .NET is integrated with `AIAgent` extensions. |
| Subworkflows | `ConfigureSubWorkflow`, `BindAsExecutor`, subworkflow sample. | Internal subworkflow execution mode; no public binding helper found. | .NET only | Go has implementation pieces but no comparable public feature. |
| Handoff orchestration | Handoff workflow builder with handoff instructions, tool-call filtering, return-to-previous, response/update events. | No first-class handoff builder. | .NET only | Could be modeled manually with tools/workflows, but no SDK feature. |
| Group chat orchestration | Group chat manager and group chat workflow builder. | No first-class group chat builder. | .NET only | Go has workflow primitives but not the managed group chat abstraction. |
| Declarative agents | YAML prompt/declarative agent packages, factories, PowerFx helpers. | No equivalent package. | .NET only | Go skills are prompt-like, but not declarative agents. |
| Declarative workflows | Declarative workflow packages, Foundry/MCP declarative integrations, samples for confirm input, HTTP, code, MCP, function tools, marketing, student/teacher, etc. | No equivalent package. | .NET only | Go workflows are code-first. |
| Workflow source generators | `Microsoft.Agents.AI.Workflows.Generators`. | No equivalent package. | .NET only | Go relies on generics/reflection without generator tooling. |
| Durable agents/workflows | Durable Task agents/workflows, Azure Functions and console samples, reliable streaming, long-running tools. | No durable package. | .NET only | Go checkpointing is in-process only. |
| Workflow visualization | Visualization sample and DevUI serialization extensions. | Reflectors for edges/executors/ports and edge labels; no visualization UI sample found. | Partial | Go can expose metadata but does not ship visualization tooling. |
| Message filters | Not a prominent standalone package in the scanned .NET public surface. | `message/messagefilter` with `And`, `Or`, `PassThrough`, `None`, source filters. | Go only | Go exposes message filtering as a small public package. |
| Message workflow adapter | No direct standalone package found. | `message/messageworkflow` configures a `workflow.ExecutorSpec` from message options. | Go only | This is Go-specific glue around the local message model. |
| Samples and tutorials | Very broad sample set: get started, providers, agents, shell environment, skills, Foundry, memory, RAG, AGUI, A2A, MCP, declarative, DevUI, evaluation, durable hosting, web chat, Purview, M365. | Focused sample set: get started, providers, agents, shell environment, A2A, AGUI, MCP server, skills, workflows (including checkpoint-and-resume and checkpoint-and-rehydrate), A2A end-to-end, chat CLI. | Partial | Go samples cover core parity areas but miss many .NET integration scenarios. |

## Notable Misalignments Inside Overlapping Features

### Agent Runtime

- .NET can adapt any `IChatClient` into an `AIAgent`, so provider coverage includes direct packages plus any MEAI-compatible chat client. Go can define any provider through `agent.ProviderConfig`, but there is no common external chat-client adapter layer.
- .NET agent responses include typed `AgentResponse<T>`; Go keeps structured output as run options backed by provider-declared response formatting and unmarshaling hooks.
- Go supports background/continuation tokens through agent options and OpenAI Responses handling, but .NET has more sample coverage around background responses and provider fallbacks.
- Both SDKs detect conflicts between local history providers and service-managed sessions, but the session state models and extension points are different.

### Tools and Hosted Tools

- Go has typed function tools, agent-as-tool, MCP tool wrapping, approval-required tools, hosted tool declaration structs, a shell execution tool with environment context (`tool/shelltool`), and harness middleware for tool auto-calling and approval management. .NET has all of those categories plus plugins, dynamic function tool samples, tool approval agent wrappers, Docker shell execution, and many server-side hosted tool samples.
- Go hosted tools should be treated as provider-dependent declarations. The .NET samples demonstrate more concrete hosted-tool scenarios, especially Foundry and OpenAI Responses server-side tools.

### Skills

- The file/inline skill model is similar: frontmatter, content, resources, scripts, sources/providers.
- .NET adds class-based skills, attribute-based resources/scripts, and DI-backed skill construction. Go has programmatic in-memory skills and script runners, but no class/DI reflection equivalent.
- Both Go and .NET support the three skill tools (`load_skill`, `read_skill_resource`, `run_skill_script`); in Go, they are registered when skills are discovered or provided, and when present the tools return descriptive errors if the requested resource or script is not found.

### Workflows

- Core graph primitives are close: direct, fan-out, fan-in barrier, edge labels, conditions/assigners, output executors, run events, run status, checkpointing, state, and human-in-the-loop request ports.
- .NET has more convenience builders: sequential/concurrent agent workflows, handoff workflows, group chat workflows, subworkflow binding, and declarative workflows. Go can manually assemble sequential/concurrent graphs, but handoff/group-chat/declarative/subworkflow are not first-class public features.
- .NET workflow protocol metadata exposes accepted, yielded, sent, and catch-all aspects. Go's public `ProtocolDescriptor` currently exposes accepted input types; output types exist for runtime validation but are not reflected through the same public descriptor.
- .NET checkpointing supports in-memory, JSON stores, and Cosmos DB. Go supports in-memory checkpointing and resume/restore, but the checkpoint manager interface lives under an internal package, so external custom checkpoint stores are not currently ergonomic as a public SDK feature.
- .NET Durable Task is a separate durable execution model. Go has no durable equivalent.

### Hosting and Developer Experience

- Go has useful protocol handlers for A2A and AGUI, but .NET goes further with ASP.NET Core endpoint extensions, Azure Functions hosting, OpenAI-compatible hosting, Foundry hosting, DevUI, and Aspire integration.
- Go has agent OpenTelemetry middleware and workflow trace context plumbing. .NET has richer observability samples and workflow builder instrumentation.
- .NET includes evaluation and a broader harness package set. Go now includes initial harness packages for agent mode, todo tracking, tool approval, and tool auto-call; other harness capabilities still require custom tools, context providers, or test helpers.

## .NET Feature Checklist

The following .NET source packages were present and accounted for in the matrix:

| .NET package/project | Feature coverage in this comparison |
| --- | --- |
| `Microsoft.Agents.AI.Abstractions` | Core agent, sessions, responses, context providers, chat history, content conversions. |
| `Microsoft.Agents.AI` | Chat client agent, compaction, evaluation, harness providers, memory, skills. |
| `Microsoft.Agents.AI.OpenAI` | OpenAI and Azure OpenAI agent adapters. |
| `Microsoft.Agents.AI.Anthropic` | Anthropic agent adapters. |
| `Microsoft.Agents.AI.Tools.Shell` | Local shell execution, shell policies, shell results, shell environment provider/snapshots, environment-aware sample parity. |
| `Microsoft.Agents.AI.A2A` | A2A agent client integration. |
| `Microsoft.Agents.AI.AGUI` | AGUI chat client and conversions. |
| `Microsoft.Agents.AI.AzureAI.Persistent` | Azure AI Persistent Agents. |
| `Microsoft.Agents.AI.Foundry` | Foundry agents and related integrations. |
| `Microsoft.Agents.AI.Foundry.Hosting` | Foundry hosted agents. |
| `Microsoft.Agents.AI.CopilotStudio` | Copilot Studio agent integration. |
| `Microsoft.Agents.AI.GitHub.Copilot` | GitHub Copilot provider integration. |
| `Microsoft.Agents.AI.Mem0` | Mem0 memory provider. |
| `Microsoft.Agents.AI.CosmosNoSql` | Cosmos chat history and checkpoint stores. |
| `Microsoft.Agents.AI.Purview` | Purview integration. |
| `Microsoft.Agents.AI.Declarative` | Declarative/prompt agents. |
| `Microsoft.Agents.AI.DevUI` | Developer UI endpoints and services. |
| `Aspire.Hosting.AgentFramework.DevUI` | Aspire DevUI integration. |
| `Microsoft.Agents.AI.Hosting` | Base hosting infrastructure. |
| `Microsoft.Agents.AI.Hosting.A2A` | A2A hosting. |
| `Microsoft.Agents.AI.Hosting.A2A.AspNetCore` | ASP.NET Core A2A hosting. |
| `Microsoft.Agents.AI.Hosting.AGUI.AspNetCore` | ASP.NET Core AGUI hosting. |
| `Microsoft.Agents.AI.Hosting.AzureFunctions` | Azure Functions hosting. |
| `Microsoft.Agents.AI.Hosting.OpenAI` | OpenAI-compatible hosting for chat completions, responses, conversations. |
| `Microsoft.Agents.AI.Workflows` | Code-first workflows, in-process execution, checkpointing, state, agents in workflows, workflow as agent, handoff/group chat builders. |
| `Microsoft.Agents.AI.Workflows.Declarative` | Declarative workflow runtime and object model. |
| `Microsoft.Agents.AI.Workflows.Declarative.Foundry` | Foundry declarative workflow integration. |
| `Microsoft.Agents.AI.Workflows.Declarative.Mcp` | MCP declarative workflow integration. |
| `Microsoft.Agents.AI.Workflows.Generators` | Workflow generator tooling. |
| `Microsoft.Agents.AI.DurableTask` | Durable agents and durable workflows. |

The following .NET sample categories were also accounted for: get started, A2A, AGUI, agent providers, OpenAI/Anthropic/Foundry provider flows, shell environment, memory, RAG, MCP, skills, DevUI, evaluation, harness, declarative agents, code-first workflows, checkpointing, concurrent workflows, conditional edges, human-in-the-loop, shared state, observability, orchestration/handoff, visualization, durable hosting, Foundry hosted agents, web chat, authorization, Purview, and M365.

## Go Feature Checklist

The following Go packages and sample groups were present and accounted for in the matrix:

| Go package/sample group | Feature coverage in this comparison |
| --- | --- |
| `agent` | Core agent runtime, options, sessions, history, context providers, middleware, responses. |
| `agent/compaction` | Compaction provider, triggers, strategies, message indexing. |
| `agent/format/jsonformat` | JSON response formats and schema helpers. |
| `agent/harness/agentmode` | Agent operating-mode context provider and mode-switching tools. |
| `agent/harness/todo` | Todo-list context provider and todo management tools. |
| `agent/harness/toolapproval` | Human-in-the-loop tool approval middleware with standing approval rules. |
| `agent/harness/toolautocall` | Function-tool auto-calling and approval handling. |
| `(*agent.ContextProvider).Middleware` | Context provider middleware adapter. |
| `agent.ProviderConfig` structured output hooks | Automatic structured output middleware when providers supply `Format` and `Unmarshal`. |
| `agent/opentelemetry` | Agent OpenTelemetry middleware. |
| `agent/provider/openaiagent` | OpenAI Chat Completions and Responses, including Azure OpenAI client usage. |
| `agent/provider/anthropicagent` | Anthropic provider. |
| `agent/provider/geminiagent` | Gemini provider. |
| `agent/provider/a2aagent` | A2A remote agent provider. |
| `agent/provider/aguiagent` | AGUI remote agent provider. |
| `agent/provider/workflowprovider` | Workflow as agent. |
| `agent/hosting/a2ahosting` | A2A agent hosting handlers/executor. |
| `agent/hosting/aguihosting` | AGUI agent hosting handler/events. |
| `agent/hosting/workflowhosting` | Agent as workflow executor. |
| `agent/skills` | Skill model, sources, provider, resources, scripts. |
| `agent/skills/fsskills` | File-system skills source. |
| `message` | Messages, content types, annotations, data URI handling, coalescing. |
| `message/messagefilter` | Message filter combinators and source filters. |
| `message/messageworkflow` | Message/workflow adapter options. |
| `tool` | Tool abstractions, tool modes, approval-required tools. |
| `tool/functool` | Typed function tools and JSON schemas. |
| `tool/agenttool` | Agent as function tool. |
| `tool/hostedtool` | Hosted web search, file search, code interpreter, MCP server declarations. |
| `tool/mcptool` | MCP tool bridge and MCP server/client helpers. |
| `tool/shelltool` | Local shell command execution tool with policy allow/deny-list, approval gate, output truncation, raw executor interface, shell environment provider, environment snapshots, shell-family instructions, and common CLI version probing. |
| `workflow` | Workflow graph builder, executor bindings, edge model, events, protocol, request ports, state context. |
| `workflow/inproc` | In-process run/streaming/resume/checkpoint execution environments. |
| `examples/01-get-started` | Hello agent, tools, multi-turn, memory, first workflow. |
| `examples/02-agents` | Running agents, tools, approvals, structured output, persisted conversation, third-party history, observability, DI-style construction, agent as MCP/tool, images, context providers, compaction, shell environment context, A2A, AGUI, providers, MCP server, skills. |
| `examples/03-workflows` | Streaming, agents in workflows, patterns, checkpoint/resume, concurrent, conditional edges, HITL, loop, shared state. |
| `examples/05-end-to-end` | A2A client/server. |
| `examples/demos/chat_cli` | Chat CLI demo. |

## Highest-Priority Go Parity Opportunities

1. Add Cosmos-like checkpoint/chat history storage integrations and examples.
2. Add DevUI or at least workflow visualization/export support that consumes existing reflection metadata.
3. Add evaluation primitives and samples, starting with local function checks and expected-output/tool-call assertions.
4. Add declarative agent/workflow support only if Go wants parity with .NET's YAML/PowerFx model; otherwise document the intentional code-first stance.
5. Add first-class handoff and group chat builders, or document recommended manual workflow patterns.
6. Expand provider integrations for Foundry, Azure AI Persistent Agents, Copilot Studio, GitHub Copilot, Mem0, Cosmos DB, and Purview if Go intends to match .NET's product surface.
7. Add OpenAI-compatible, Azure Functions, and richer web hosting adapters if Go should match .NET hosting scenarios.