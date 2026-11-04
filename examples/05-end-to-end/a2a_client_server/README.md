# A2A Policy Client/Server (End-to-End)

This sample ports the .NET `samples/05-end-to-end/A2AClientServer` policy-agent scenario to Go.

It demonstrates:

1. Hosting a policy agent through both A2A HTTP+JSON and JSON-RPC.
2. Discovering that agent and sending interactive requests in a shared A2A session.

## Prerequisites

- Go 1.26+
- Microsoft Foundry project environment and authentication for the server:

```powershell
$env:FOUNDRY_PROJECT_ENDPOINT="https://<your-foundry-service>.services.ai.azure.com/api/projects/<your-project>"
$env:FOUNDRY_MODEL="gpt-5.4-mini" # optional, defaults to gpt-5.4-mini
az login
```

The client connects directly to the A2A server and does not require Foundry credentials.

## Run server

```powershell
cd examples/05-end-to-end/a2a_client_server/a2a_server

go run . --port 5000
```

The server listens on localhost and exposes:

- JSON-RPC A2A endpoint at `/`
- HTTP+JSON A2A operations such as `/message:send`
- agent card at `/.well-known/agent-card.json`

`A2A_AGENT_URL` optionally overrides the URL published in the card. When using a different port, set the client URL to match it.

## Run client

```powershell
cd examples/05-end-to-end/a2a_client_server/a2a_client
$env:A2A_AGENT_URL="http://localhost:5000/"
go run .
```

Ask questions like:

- `What is the policy for short shipments?`
- `When should the customer be notified?`

The server explicitly uses an in-memory session store for multi-turn requests. This local sample does not authenticate callers. Before exposing it to untrusted clients, authenticate requests and isolate session and task storage by caller; context and task IDs are not authorization boundaries.

## Related examples

Set `A2A_AGENT_HOST` to a trusted server URL for the examples under `examples/02-agents/a2a` and `examples/02-agents/providers/a2a`.

- `as_function_tools` wraps the complete remote agent as one function tool for a Foundry-backed host agent.
- `protocol_selection` demonstrates an explicit HTTP+JSON or JSON-RPC preference.
- `polling_for_task_completion` requires a task-capable remote agent and polls its continuation token.
- `stream_reconnection` interrupts after the first update and reconnects using its continuation token; the remote agent must support streaming tasks.

The policy server uses the default message mode, so it does not issue task continuation tokens. The task-oriented examples require a server configured with `a2aprovider.ReturnTask()` rather than `ReturnMessage()`, with streaming advertised for reconnection.
