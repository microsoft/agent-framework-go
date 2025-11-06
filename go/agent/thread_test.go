// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/microsoft/agent-framework/go/agent"
)

// Test JSON marshaling of InMemoryThread
func TestInMemoryThread_MarshalJSON(t *testing.T) {
	thread := &agent.InMemoryThread{}
	thread.Add(context.Background(), agent.NewTextMessage("test"))

	data, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("expected no error marshaling thread, got: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty JSON data")
	}

	// Verify it's valid JSON array (thread marshals as array of messages)
	var result []interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Errorf("expected valid JSON array, got error: %v", err)
	}
}
