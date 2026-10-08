# Feature-usage telemetry

The SDK accumulates a process-wide, concurrency-safe 128-bit feature mask. Each
bit records whether a framework-owned capability has participated in an operation
at least once. Bits are idempotent, never reset during production, and do not
represent invocation counts. Construction alone does not mark a feature.

The mask contains no identifiers, prompts, arguments, payloads, or user data.
Tracking is independent of the provider; a feature used with a third-party
provider can appear in a later request to an approved Azure destination.
Specialized workflow builders mark only their orchestration bit; bit 2 is
reserved for custom workflow graphs.

## Encoding and emission

The version-1 registry uses the .NET index numbers for equivalent Go
capabilities. Only supported capabilities registered in .NET are tracked;
Go-only capabilities do not receive dedicated bits. Python uses different
indexes.

The mask is emitted as a User-Agent comment:

```text
agent-framework-go/v0.1.0 OpenAI/Go ... (feat=v1.15)
```

`v1` identifies the aligned registry version. The unsigned mask is lowercase hexadecimal,
without a `0x` prefix or leading zeros, and contains at most 32 digits. An empty
mask produces no comment. Bit `index` is set when `mask & (1 << index)` is nonzero.
Existing valid feature comments are replaced rather than duplicated; unrelated
comments and header content are preserved.

Only the OpenAI/Foundry Chat Completions and Responses adapters emit the comment,
including streaming, tool follow-up, and continuation requests. The actual
request URL must use HTTPS and its hostname must equal, or be a subdomain of,
a reviewed suffix for that adapter family.

OpenAI adapters approve:

- `cognitiveservices.azure.com`
- `openai.azure.com`
- `services.ai.azure.com`

Foundry adapters approve:

- `services.ai.azure.com`
- `inference.ai.azure.com`

Direct OpenAI, other third-party providers, custom gateways, HTTP destinations,
and lookalike hostnames do not receive the mask. Valid inherited feature comments
are removed on unapproved requests. Credentials and provider names do not approve
a destination.

The OpenAI SDK's request-origin and redirect-origin guards remain in place:
cross-origin redirects are rejected before reaching the transport. The framework
does not replace caller clients, transports, redirect callbacks, or SDK retry
behavior. Bespoke HTTP clients remain responsible for the SDK's documented
same-origin contract.

The feature mask is not added to OpenTelemetry. Foundry memory operations mark
their capability but do not add a feature comment to the Projects client's
requests; the accumulated bit can be emitted by a later eligible adapter request.
Provider hooks mark memory participation even when there are no messages to
search or store. Foundry streaming does not mark features until enumeration
starts, and server-agent execution marks both the agent and chat-client bits
before the current request is sent.

## Opt-out

Set environment variables before the corresponding telemetry mechanism is first
used. Their values are cached for the process.

- `AGENT_FRAMEWORK_FEATURE_MASK_DISABLED=true|1` disables accumulation and
  emission of the feature mask, retaining the base SDK User-Agent.
- `AGENT_FRAMEWORK_USER_AGENT_DISABLED=true|1` suppresses the framework's
  User-Agent contribution, including the feature mask.

`true` is case-insensitive. Other values leave the mechanism enabled. Valid
existing feature comments are also removed when emission is disabled.
These switches do not configure OpenTelemetry or change service telemetry.

## Supported .NET version-1 indexes

| Index | Feature | Activation |
| --- | --- | --- |
| 0 | `core.agent` | Agent invocation |
| 2 | `core.workflow` | Successful workflow build |
| 3 | `core.tool_approval` | Tool-approval middleware participation |
| 8 | `core.skills_provider` | Skills context provider participation |
| 9 | `core.compaction_provider` | Compaction context/history provider participation |
| 10 | `core.todo_provider` | Todo context provider participation |
| 11 | `core.agent_mode_provider` | Agent-mode context provider participation |
| 13 | `core.in_memory_history_provider` | History retrieval or persistence |
| 14 | `core.mcp` | MCP connection, discovery, or tool invocation |
| 15 | `core.file_skills_source` | File-backed skill discovery |
| 16 | `core.in_memory_skills_source` | In-memory skill retrieval |
| 32 | `orchestration.sequential` | Successful sequential workflow build |
| 33 | `orchestration.concurrent` | Successful concurrent workflow build |
| 34 | `orchestration.group_chat` | Successful group-chat workflow build |
| 48 | `foundry.chat_client` | Foundry model-deployment agent invocation |
| 49 | `foundry.agent` | Foundry server-agent invocation |
| 50 | `foundry.memory` | Foundry memory provider participation, provisioning, or deletion |
| 54 | `openai` | OpenAI adapter invocation, including continuation |
| 55 | `anthropic` | Anthropic adapter invocation |
| 57 | `github_copilot` | GitHub Copilot adapter invocation |
| 62 | `a2a` | A2A client adapter invocation |
| 63 | `hosting.ag_ui` | AG-UI HTTP request handling |
| 69 | `tools.shell` | Shell invocation, persistent initialization, or environment-provider operation |
| 73 | `hosting.a2a` | A2A request handling or executor operation |

Other indexes are not emitted by this SDK and must not be repurposed for Go-only
features. New counterparts must use their .NET registry index and be documented
here. The source allocation is the [.NET version-1 registry](https://github.com/microsoft/agent-framework/blob/0c9944cc9f577d51277ac7c55dbc388b60a577af/docs/specs/feature-usage-bit-registry.md#index-table--net-agent-framework-dotnet-version-1).
