# Agent harness

The [harness package](../agent/harness/harness.go) configures an ordinary
provider-backed agent with Todo, agent modes, file memory, skills, approvals,
history, hosted web search, message injection, and tracing. It does not introduce
another agent type or tool-execution loop.

Use a normal provider constructor if you want to configure those components
individually. Use the harness when you want them assembled with the defaults below.

## Configure an agent

Pass harness options and common agent settings to `harness.AgentConfig`, then use
the result with the normal provider constructor. There is no separate harness
object to keep open or close. The following function accepts an existing OpenAI
client and makes one request:

```go
package example

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
)

func ask(ctx context.Context, client openai.Client, model, prompt string) (string, error) {
	cfg, err := harness.AgentConfig(harness.Config{}, agent.Config{
		Name: "Assistant",
	})
	if err != nil {
		return "", err
	}
	a := openaiprovider.NewAgent(client, openaiprovider.AgentConfig{
		Config:             cfg,
		Model:              model,
		Instructions:       "Help with software engineering tasks.",
		DisableStoreOutput: true,
	})
	session, err := a.CreateSession(ctx)
	if err != nil {
		return "", err
	}
	response, err := a.RunText(ctx, prompt, agent.WithSession(session)).Collect()
	if err != nil {
		return "", err
	}
	return response.String(), nil
}
```

The same setup works through the Foundry model-deployment, Anthropic, and Gemini
constructors, which embed `agent.Config` in their configuration. The model must
support the requested tools. Hosted web search is service-specific; disable it
when the selected API or deployment does not support it.

The [Foundry example](../examples/02-agents/harness/step04_composite_harness/main.go)
also handles tool-approval requests and demonstrates memory isolation between
sessions. From the repository root, authenticate with `az login`, set
`FOUNDRY_PROJECT_ENDPOINT` and `FOUNDRY_MODEL`, and run:

```bash
go run ./examples/02-agents/harness/step04_composite_harness
```

## Defaults and overrides

All component-disable flags default to `false`.

| Component | Default | Override |
| --- | --- | --- |
| Harness instructions | `harness.DefaultInstructions`, before caller instructions | Set `harness.Config.Instructions`. Nil uses the default; an empty or whitespace-only value omits harness instructions. Provider instructions and `agent.WithInstructions` remain independent. |
| Todo | Session-backed Todo tools and instructions | Set `DisableTodo`. |
| Agent mode | Built-in plan/execute modes, initially plan | Set `AgentMode` to an `agentmode.Config`, or set `DisableAgentMode`. |
| File memory | Persistent files below `<working-directory>/agent-file-memory`, with a unique working folder per session | Set `WorkingDirectory` or `DisableFileMemory`. Session restoration retains its original working folder. |
| Skills | File-backed discovery from the working directory, excluding the reserved `agent-file-memory` subtree, with the existing approval requirements | Set `Skills` to `skills.ContextProviderOptions`, or set `DisableSkills`. Non-nil `Skills.Skills` or `Skills.Sources` replaces default file discovery, including an explicitly empty collection. Caller sources retain their own access policy. |
| Hosted web search | One default `hostedtool.WebSearch`, unless already supplied through `Tools` or `RunOptions` | Set `DisableWebSearch`. Existing caller tools and their settings are retained. |
| Tool-approval middleware | Standing approvals and auto-approval rules enabled | Set `ToolApproval` to `toolapproval.Config`, or set `DisableToolApproval`. Disabling this middleware does not remove approval requirements from tools. |
| History | In-memory history, persisted per model call | Supply `agent.Config.HistoryProvider`. The harness enables `RequirePerServiceCallHistoryPersistence`. |
| History conflicts | Warning and throwing disabled; the core agent's history-clearing default is unchanged | Explicit `WarnOnHistoryProviderConflict`, `ThrowOnHistoryProviderConflict`, and `ClearOnHistoryProviderConflict` settings are retained. |
| Message injection | A new `agent.MessageInjector` per configured agent | Supply `agent.Config.MessageInjector`. Enqueue messages through the injector on the returned configuration. |
| Tracing | Agent, tool, and supported model-adapter spans using the global OpenTelemetry providers | Set `OpenTelemetry.SourceName`, set `OpenTelemetry.DisableModelTracing` for agent/tool-only tracing, or set `DisableOpenTelemetry` to omit harness tracing entirely. |

`WorkingDirectory` defaults to the current directory and is resolved when
`harness.AgentConfig` runs. Memory directories are created as needed. A session's
memory files remain on disk when its provider is cleaned up and can be reopened
at the same root. Different sessions receive different UUID folders. Do not
substitute user-controlled paths for the working directory
without applying your application's access policy.

Default skill discovery also denies direct resource access to the memory subtree,
including when the working-directory root contains a skill. Session memory must
not become a shared skill or resource after an agent is recreated.

File-backed skill scripts require a runner. The default harness discovers skills
but does not install a script runner. To enable script execution, pass a source
created with `fsskills.NewSourceOptions` and a `ScriptRunner` through
`Skills.Sources`. This follows the underlying skills provider's explicit runner
and approval contracts.

### Tool invocation stays with the provider

The normal model-provider constructors already install automatic tool invocation.
Do not add a second `toolautocall` middleware around the harness.

Use the provider configuration's `ToolAutoCall` field for an optional
[`toolautocall.Config`](../agent/harness/toolautocall/autocall.go):

- `MaximumIterationsPerRequest` defaults to 40 when nil. In Go, an explicit zero
  disables automatic invocation.
- `DisableApprovalResponseBinding` defaults to `false`.
- `DisableApprovalNotRequiredFunctionBypassing` defaults to `false`.

The harness does not override those settings. Approval-required tools can still
pause a run when `DisableToolApproval` is true; the application must return the
approval response through the same session. The runnable example shows that flow.

### Provider-managed history

`AgentConfig` does not change provider request-storage settings. The usage example
sets `DisableStoreOutput: true` so OpenAI Responses uses local session history.
If the service manages history instead, the core agent's existing conflict and
continuation rules apply. Foundry server agents own their model and instruction
configuration; they are not equivalent to a locally composed model-client harness.

## Ordering and ownership

Call `AgentConfig` once on each unconfigured `agent.Config`. It preserves caller
identity, logger, function middleware, tools, history, and explicit conflict
settings. It does not mutate the caller's input slices.

- Caller agent middleware remains outermost. Harness usage tracking, approval,
  and tracing middleware run inside it.
- Built-in context providers run in Todo, mode, FileMemory, and skills order,
  followed by caller context providers.
- Context providers supply tools before the provider's tool-invocation
  middleware runs. History and message injection use the existing agent lifecycle.

File-memory handles are released through `runtime.AddCleanup` when their provider
and tools become unreachable. Cleanup is asynchronous and is not guaranteed to
run before program exit. `filememory.Provider.Close` is an optional optimization
for releasing a handle early after all runs and tool calls finish. It does not
delete memory files. Harness setup never closes caller-supplied components or
shuts down global telemetry providers. Cancellation or early stream termination
releases the active provider call, not the configured components.
Filesystem setup failures return an error. Invalid component configuration retains
the corresponding component constructor's panic contract.

## Tracing and usage tracking

Configure the global OpenTelemetry tracer and meter providers in the application.
The harness does not install exporters.

OpenAI Chat Completions/Responses, Foundry Responses, Anthropic, and Gemini emit
client spans with operation `chat`. A model span covers one adapter invocation,
including any SDK retries or remote server-side orchestration. Model and tool
spans are separate children of the agent span. Model spans include request and
response model identifiers, response IDs, reported usage, duration, and errors,
but not prompt contents or tool arguments/results. Other adapters retain the
agent-level tracing supported by their existing implementations.

`otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})` instruments supported
model calls by default for ordinary agents as well as the harness, matching the
stable .NET tracing entry point. This adds model spans and per-call metrics to the
previous agent/tool-only default. Set `DisableModelTracing: true` to retain
agent/tool-only output. The harness passes `OpenTelemetry` settings through without
overriding this opt-out.

Agents without this middleware do not gain framework model instrumentation.
Nested agents use their own middleware settings and instrumentation scope.
`DisableOpenTelemetry` suppresses the harness's tracing middleware, not tracing
independently installed by the application or a native model SDK.

[Feature-usage tracking](./feature-usage-telemetry.md) is separate from tracing.
A consumed harness-configured run marks `core.harness_agent`, index 1, in the
existing feature mask. Construction, configuration, and unconsumed streams do not
mark it. Existing feature-mask and User-Agent opt-outs still apply.

## Stable .NET defaults assessment

The composition was checked against
[`HarnessAgent`](https://github.com/microsoft/agent-framework/blob/ebcf4db7f9462ef6c377761e0b3159c4c5a73027/dotnet/src/Microsoft.Agents.AI.Harness/HarnessAgent.cs),
[`HarnessAgentOptions`](https://github.com/microsoft/agent-framework/blob/ebcf4db7f9462ef6c377761e0b3159c4c5a73027/dotnet/src/Microsoft.Agents.AI.Harness/HarnessAgentOptions.cs),
and
[`AsHarnessAgent`](https://github.com/microsoft/agent-framework/blob/ebcf4db7f9462ef6c377761e0b3159c4c5a73027/dotnet/src/Microsoft.Agents.AI.Harness/ChatClientHarnessExtensions.cs)
at revision `ebcf4db7f9462ef6c377761e0b3159c4c5a73027`.

All 21 non-experimental options have a Go configuration counterpart. Declaration
assessments live in the [symbol catalog](../_catalog/dotnet-go-sdk-symbol-mapping.json),
not in a second mapping inventory. These harness declarations were reviewed from
source; their separate .NET assembly is not in the catalog's current binary
extraction scope. Existing extraction hashes and package metadata are unchanged.

The stable composition retains enabled components, provider ordering,
instruction opt-out, approval binding/bypassing defaults, per-call history,
session-isolated memory, and agent/model tracing. Go adaptations are explicit:

- A top-level `AgentConfig` function replaces a C# delegating agent and extension
  method. It returns filesystem setup errors explicitly. The caller selects the
  provider and supplies dependencies directly rather than through `IServiceProvider`.
- File-memory resources have automatic runtime cleanup, with optional early
  release through the provider's `Close` method instead of a harness lifetime object.
- Harness instructions give the same tool-use and progress guidance without
  promising identical prompt wording or instruction separators.
- Memory folders use UUIDs without the .NET timestamp prefix. Memory remains
  session-isolated and persistent, and storage stays SDK-internal.
- Tracing uses the Go SDK instrumentation scope, and model spans describe
  adapter calls rather than exposing individual remote server-agent model calls.
- Tool-loop controls remain in provider configuration. Go additionally permits
  a zero iteration limit to disable invocation.

Eleven experimental options are intentionally excluded from this composition:
compaction strategy and token-budget settings, loop evaluators/options, custom
FileMemory storage injection, FileAccess storage/options, and background-agent
configuration. This does not remove independently available Go components.

The [harness tests](../agent/harness/harness_test.go),
[model-tracing tests](../provider/otelprovider/otel_test.go), and
[activation tests](../internal/telemetry/featureusage_activation_test.go) verify
defaults and opt-outs, caller configuration preservation, approvals after session
restoration, exact history persistence, memory reopening/isolation, tracing
hierarchy and usage, cancellation, early termination, and tracking opt-out.
These checks use fake providers or local HTTP fixtures. They do not establish
bit-for-bit prompt, serialization, or service behavior parity, and no live
Foundry execution was performed.
