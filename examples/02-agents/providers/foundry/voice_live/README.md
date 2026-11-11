# MAF + Foundry Voice Live

A minimal browser voice demo: one Go server and one HTML page. Voice Live handles
speech; a Microsoft Agent Framework (MAF) agent answers store questions using
the Foundry Responses API.

```text
Browser <---- WebRTC audio ----> Foundry Voice Live
   |
   +---- WebSocket control ---> Go ----------> Foundry Voice Live
   |
   +---- HTTP tool calls -----> Go / agenttool / MAF Foundry agent
```

**Voice Live WebRTC is in preview.** It uses global standard deployments and
routes requests to the nearest region. Check the
[WebRTC prerequisites](https://learn.microsoft.com/en-us/azure/ai-services/speech-service/voice-live-webrtc)
before using it.

## Run

Use the Go version from the root `go.mod`, a modern WebRTC browser, and a Foundry
resource that supports Voice Live. Deploy a Responses-capable text model in a
project on that resource. Give your identity the required access, including
**Cognitive Services User** and **Foundry User**.

From the repository root, in PowerShell:

```powershell
az login
$env:FOUNDRY_PROJECT_ENDPOINT = "https://RESOURCE.services.ai.azure.com/api/projects/PROJECT"
$env:FOUNDRY_MODEL = "<your-responses-model-deployment>"
go run .\examples\02-agents\providers\foundry\voice_live
```

The demo uses the **same resource** for Voice Live and the MAF agent. It derives
the Voice Live endpoint from the project URL; no separate voice endpoint is needed.
Voice Live uses its built-in `gpt-realtime` model, not the text-model deployment.
The voice model, Azure voice, and fictional store facts are set in [main.go](main.go).

Open the localhost URL printed in the terminal, select **Connect**, and allow
microphone access. Ask **"Where is order DEMO-1001?"**, then **"Can I return it?"**.
The page shows MAF calls and their results. Headphones are recommended.
Click **Disconnect** to release the microphone and close the voice connection,
then stop the Go server with Ctrl+C.

## How it works

1. [index.html](index.html) captures the microphone, creates a WebRTC offer, and
   waits for ICE candidates.
2. A WebSocket to `/session` sends the offer to Go, which opens a Voice Live
   control WebSocket at
   `/voice-live/realtime/calls?api-version=2026-07-15&model=gpt-realtime`. Go adds
   the session settings and relays the SDP answer and subsequent control messages.
   `DefaultAzureCredential` authenticates with the `https://ai.azure.com/.default`
   scope. Entra tokens stay on the Go server.
3. Audio flows directly between the browser and Voice Live. Speech and response
   events arrive on the `voice-live-events` data channel. Commands, tool results,
   and service errors use the persistent control WebSocket, not the data channel.
4. The browser sends completed function-call arguments to `POST /tool`. Go invokes
   the MAF agent through `agenttool`, and the browser returns each result with its
   original call ID before requesting the next spoken reply.

Voice Live handles speech detection, interruption, and automatic history
truncation. The page schedules replies after committed user messages or tool
results. Each MAF invocation is independent and receives a self-contained query;
voice conversation history stays with Voice Live.

## Scope

This is a local example, not an SDK realtime provider or a service-hosted Foundry
agent. It has same-origin request protection but no application authentication:
do not expose the server publicly. In deployment, use a specific credential such
as managed identity instead of `DefaultAzureCredential`.

The store facts are fictional instructions, not a database or extra business
tools. Interrupting speech does not undo a running MAF call. Disconnecting cancels
MAF requests and closes both WebRTC and the control WebSocket. Failures are
displayed and end the connection. There is no reconnect, durable history, or
text-input mode.

Voice Live and MAF model calls incur costs. Responses storage is disabled for MAF;
the services' data-processing and retention policies still apply.

See the [Voice Live API reference](https://learn.microsoft.com/en-us/azure/ai-services/speech-service/voice-live-api-reference-2026-07-15)
for the service protocol.
