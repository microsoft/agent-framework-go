// Copyright (c) Microsoft. All rights reserved.

package aguiprovider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	aguiEvents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
)

// TestAGUIAgentRun_EarlyStreamStop_CancelsUpstreamRequest verifies that when a
// consumer stops ranging over the streaming response early, the provider tears
// down the SSE request rather than leaking the client's reader goroutine and
// HTTP response body.
//
// The server sends one event then keeps the stream open and waits for the
// client to disconnect. With the fix, the early stop cancels the request and
// the server observes the disconnect promptly; without it the request lingers
// until the client's ReadTimeout and the disconnect never arrives in time.
func TestAGUIAgentRun_EarlyStreamStop_CancelsUpstreamRequest(t *testing.T) {
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-1"))
		writeSSE(t, w, aguiEvents.NewTextMessageStartEvent("msg-1", aguiEvents.WithRole("assistant")))
		writeSSE(t, w, aguiEvents.NewTextMessageContentEvent("msg-1", "Hello"))
		// Deliberately do not finish the run: keep the stream open and wait for
		// the client to disconnect. The safety timeout lets the handler return
		// (so the test server can close) if the disconnect never comes.
		select {
		case <-r.Context().Done():
			close(disconnected)
		case <-time.After(10 * time.Second):
		}
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})

	// Consume a single update, then stop. Breaking out of the range makes the
	// downstream yield return false, which must propagate to the provider and
	// cancel the upstream SSE request.
	for _, err := range a.RunText(context.Background(), "hi", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error before first update: %v", err)
		}
		break
	}

	select {
	case <-disconnected:
		// Fixed: the early stop cancelled the SSE request.
	case <-time.After(3 * time.Second):
		t.Fatal("server did not observe client disconnect after early stream termination: " +
			"the SSE reader goroutine and HTTP response body leaked")
	}
}
