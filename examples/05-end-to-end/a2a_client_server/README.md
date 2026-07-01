# A2A Client/Server (End-to-End)

This sample ports the .NET `samples/05-end-to-end/A2AClientServer` scenario to Go.

It demonstrates:

1. Running multiple A2A servers, each exposing one specialized agent.
2. Running an A2A client that discovers those agents and uses them as tools from a host agent.

## Prerequisites

- Go 1.24+
- Microsoft Foundry project environment and authentication:

```powershell
$env:FOUNDRY_PROJECT_ENDPOINT="https://<your-foundry-service>.services.ai.azure.com/api/projects/<your-project>"
$env:FOUNDRY_MODEL="gpt-4o-mini" # optional, defaults to gpt-4o-mini
az login
```

## Run servers

Start one process per agent type:

```powershell
cd examples/05-end-to-end/a2a_client_server/a2a_server

go run . --agentType invoice --port 5000
# in another terminal

go run . --agentType policy --port 5001
# in another terminal

go run . --agentType logistics --port 5002
```

Each server exposes:

- JSON-RPC A2A endpoint at `/`
- agent card at `/.well-known/agent-card.json`

## Run client

```powershell
cd examples/05-end-to-end/a2a_client_server/a2a_client
$env:A2A_AGENT_URLS="http://localhost:5000;http://localhost:5001;http://localhost:5002"
go run .
```

Ask questions like:

- `Customer is disputing transaction TICKET-XYZ987 as they claim they received fewer t-shirts than ordered.`
- `Show me all invoices for Contoso.`
