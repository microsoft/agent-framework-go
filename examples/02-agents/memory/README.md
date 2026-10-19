# Memory

Ports of the [.NET memory samples](https://github.com/microsoft/agent-framework/tree/95511ff025d81cc546c44e653d4be65de5bd7f35/dotnet/samples/02-agents/AgentWithMemory).
Step numbers follow upstream.

| Example | Demonstrates |
| --- | --- |
| [Step 01: Chat history memory](./step01_chat_history_memory/) | Remembering a user's preferences across two independent sessions |
| [Step 05: Bounded chat history](./step05_bounded_chat_history/) | Keeping four recent messages and recalling older messages from vector-store overflow |

Both use `memory.HistoryProvider` and an in-memory vector store. Memories survive
new sessions, but not process restarts. The fixed `UID1` scope is for demonstration;
applications should use the authenticated user's identifier.

## Run

Sign in with a credential supported by `DefaultAzureCredential`, such as
`az login`, and configure deployed Foundry models:

```powershell
$env:FOUNDRY_PROJECT_ENDPOINT = "https://<resource>.services.ai.azure.com/api/projects/<project>"
$env:FOUNDRY_MODEL = "gpt-5.4-mini"
$env:FOUNDRY_EMBEDDING_MODEL = "text-embedding-3-large"

go run .\examples\02-agents\memory\step01_chat_history_memory
go run .\examples\02-agents\memory\step05_bounded_chat_history
```

These samples use **3072-dimensional embeddings**, matching the shared demo
default, `text-embedding-3-large`. If your embedding deployment produces a
different vector size, update the dimension argument to `memory.NewHistoryProvider`
in the sample. Runs invoke live services and may incur charges.

Step 05 preserves the first system message outside the four-message window.
Function-call and function-result messages are excluded from both retained history
and archived memory. Retrieved memories are prepended to recent history and are
not stored again.

## Local validation

```powershell
go build .\examples\02-agents\memory\...
go vet .\examples\02-agents\memory\...
```
