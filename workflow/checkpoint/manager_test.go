// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	icheckpoint "github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

type managerTestStore struct {
	createInfo workflow.CheckpointInfo
	index      []workflow.CheckpointInfo
}

func (s *managerTestStore) CreateCheckpoint(context.Context, string, json.RawMessage, *workflow.CheckpointInfo) (workflow.CheckpointInfo, error) {
	return s.createInfo, nil
}

func (*managerTestStore) RetrieveCheckpoint(context.Context, string, workflow.CheckpointInfo) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (s *managerTestStore) RetrieveIndex(context.Context, string, *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error) {
	return s.index, nil
}

func TestNewJSONManagerNilStorePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()

	_ = NewJSONManager(nil)
}

func TestJSONManager_ValidatesInputs(t *testing.T) {
	manager := NewJSONManager(&managerTestStore{}).(*jsonManager)
	checkpoint := &icheckpoint.Checkpoint{}

	if _, err := manager.Commit(t.Context(), "", checkpoint); err == nil {
		t.Fatal("Commit() error = nil for empty session ID")
	}
	checkpoint.Parent = &workflow.CheckpointInfo{SessionID: "other", CheckpointID: "parent"}
	if _, err := manager.Commit(t.Context(), "session", checkpoint); err == nil {
		t.Fatal("Commit() error = nil for mismatched parent session")
	}
	if _, err := manager.Lookup(t.Context(), "session", workflow.CheckpointInfo{SessionID: "session"}); err == nil {
		t.Fatal("Lookup() error = nil for empty checkpoint ID")
	} else if !strings.Contains(err.Error(), "checkpoint: invalid checkpoint info: checkpointID cannot be empty") {
		t.Fatalf("Lookup() error = %v, want scoped checkpoint info error", err)
	}
	if _, err := manager.RetrieveIndex(t.Context(), "", nil); err == nil {
		t.Fatal("RetrieveIndex() error = nil for empty session ID")
	}
}

func TestJSONManager_RejectsInvalidStoreMetadata(t *testing.T) {
	tests := []struct {
		name string
		info workflow.CheckpointInfo
	}{
		{name: "empty info", info: workflow.CheckpointInfo{}},
		{name: "wrong session", info: workflow.CheckpointInfo{SessionID: "other", CheckpointID: "checkpoint"}},
		{name: "empty checkpoint ID", info: workflow.CheckpointInfo{SessionID: "session"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &managerTestStore{createInfo: test.info, index: []workflow.CheckpointInfo{test.info}}
			manager := NewJSONManager(store).(*jsonManager)
			if _, err := manager.Commit(t.Context(), "session", &icheckpoint.Checkpoint{}); err == nil {
				t.Fatal("Commit() error = nil for invalid store metadata")
			}
			if _, err := manager.RetrieveIndex(t.Context(), "session", nil); err == nil {
				t.Fatal("RetrieveIndex() error = nil for invalid store metadata")
			}
		})
	}
}

// The in-memory manager is keyed by session ID, so a single manager can be
// shared across concurrent workflow runs (distinct sessions). Its Store map and
// per-session caches must be synchronized: without a lock, concurrent Commit
// calls race on the map and can crash with "fatal: concurrent map writes".
// Run with -race.
func TestInMemoryManager_ConcurrentSessions_NoRace(t *testing.T) {
	mgr := &inMemoryManager{}
	ctx := context.Background()

	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("session-%d", i)
			if _, err := mgr.Commit(ctx, sid, &icheckpoint.Checkpoint{}); err != nil {
				t.Errorf("Commit(%s): %v", sid, err)
			}
			if _, err := mgr.RetrieveIndex(ctx, sid, nil); err != nil {
				t.Errorf("RetrieveIndex(%s): %v", sid, err)
			}
		}(i)
	}
	wg.Wait()
}
