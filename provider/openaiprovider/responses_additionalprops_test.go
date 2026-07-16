// Copyright (c) Microsoft. All rights reserved.

package openaiprovider_test

import (
	"testing"
)

// A non-streaming Responses result with more than one output message must carry
// response-level AdditionalProperties (e.g. EndUserId) on every message, not
// only the first. currentUpdate is reset to a fresh value for each message
// after the first, so the properties must be repopulated per message.
func TestResponses_NonStreaming_AllMessagesKeepAdditionalProperties(t *testing.T) {
	const input = `
		{
			"model":"gpt-4o-mini",
			"input": [{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"hello"}]
			}]
		}`
	const output = `
		{
			"id":"resp_multi",
			"object":"response",
			"created_at":1741891428,
			"status":"completed",
			"error":null,
			"incomplete_details":null,
			"model":"gpt-4o-mini",
			"user":"end-user-42",
			"output":[
				{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"first","annotations":[]}]},
				{"type":"message","id":"msg_2","status":"completed","role":"assistant","content":[{"type":"output_text","text":"second","annotations":[]}]}
			]
		}`

	server := newTestResponsesServer(t, input, output)
	defer server.Close()
	a := newTestResponsesClient(server, "gpt-4o-mini")

	var msgUpdates int
	for u, err := range a.RunText(t.Context(), "hello") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.MessageID == "" {
			continue
		}
		msgUpdates++
		if got, _ := u.AdditionalProperties["EndUserId"].(string); got != "end-user-42" {
			t.Errorf("message %q: EndUserId = %q, want %q", u.MessageID, got, "end-user-42")
		}
	}
	if msgUpdates != 2 {
		t.Fatalf("expected 2 message-bearing updates, got %d", msgUpdates)
	}
}
