// Copyright (c) Microsoft. All rights reserved.

package geminiprovider_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
)

// Gemini streams usageMetadata cumulatively across chunks, with the final
// chunk's totals authoritative. The provider must report that final total once,
// not sum the running totals from every chunk.
func TestUsageContent_Streaming_CumulativeUsageNotSummed(t *testing.T) {
	chunk := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return "data:" + string(b) + "\n\n"
	}
	stream := chunk(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": "Hi"}}},
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 2, "totalTokenCount": 12},
	}) + chunk(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": " there"}}},
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
	})

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "text/event-stream", stream))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "hello", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	usage := resp.Usage()
	if usage.TotalTokenCount != 15 {
		t.Errorf("TotalTokenCount = %d, want 15 (final cumulative total, not the sum of per-chunk usage)", usage.TotalTokenCount)
	}
	if usage.InputTokenCount != 10 {
		t.Errorf("InputTokenCount = %d, want 10", usage.InputTokenCount)
	}
	if usage.OutputTokenCount != 5 {
		t.Errorf("OutputTokenCount = %d, want 5", usage.OutputTokenCount)
	}
}
