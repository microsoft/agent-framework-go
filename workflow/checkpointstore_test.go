// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func TestInMemoryCheckpointStore_RoundTrip(t *testing.T) {
	store := workflow.NewInMemoryCheckpointStore()
	sessionID := "session-1"
	data := json.RawMessage(`{"stepNumber":1}`)

	info, err := store.CreateCheckpoint(context.Background(), sessionID, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID != sessionID {
		t.Errorf("expected session ID %q, got %q", sessionID, info.SessionID)
	}
	if info.CheckpointID == "" {
		t.Fatal("expected non-empty checkpoint ID")
	}

	got, err := store.RetrieveCheckpoint(context.Background(), sessionID, info)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("expected data %s, got %s", data, got)
	}
}

func TestInMemoryCheckpointStore_RetrieveIndex(t *testing.T) {
	store := workflow.NewInMemoryCheckpointStore()
	sessionID := "session-1"

	_, err := store.CreateCheckpoint(context.Background(), sessionID, json.RawMessage(`{"step":0}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateCheckpoint(context.Background(), sessionID, json.RawMessage(`{"step":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	index, err := store.RetrieveIndex(context.Background(), sessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(index))
	}
	if index[0].CheckpointID == index[1].CheckpointID {
		t.Error("expected distinct checkpoint IDs")
	}
}

func TestInMemoryCheckpointStore_EmptySession(t *testing.T) {
	store := workflow.NewInMemoryCheckpointStore()

	index, err := store.RetrieveIndex(context.Background(), "nonexistent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if index != nil {
		t.Errorf("expected nil index for nonexistent session, got %v", index)
	}
}

func TestInMemoryCheckpointStore_RetrieveNotFound(t *testing.T) {
	store := workflow.NewInMemoryCheckpointStore()

	_, err := store.RetrieveCheckpoint(context.Background(), "session-1", workflow.CheckpointInfo{
		SessionID:    "session-1",
		CheckpointID: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent checkpoint")
	}
}

func TestInMemoryCheckpointStore_EmptySessionID(t *testing.T) {
	store := workflow.NewInMemoryCheckpointStore()

	_, err := store.CreateCheckpoint(context.Background(), "", json.RawMessage(`{}`), nil)
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
}

func TestCheckpointManager_Creation(t *testing.T) {
	store := workflow.NewInMemoryCheckpointStore()
	mgr := workflow.NewCheckpointManager(store)
	if mgr.Store() != store {
		t.Error("expected Store() to return the original store")
	}
}

func TestNewInMemoryCheckpointManager(t *testing.T) {
	mgr := workflow.NewInMemoryCheckpointManager()
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.Store() == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestCheckpointManager_NilStorePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil store")
		}
	}()
	workflow.NewCheckpointManager(nil)
}

func TestInMemoryCheckpointStore_ParentTracking(t *testing.T) {
	store := workflow.NewInMemoryCheckpointStore()
	ctx := context.Background()
	sessionID := "session-parent"

	info1, err := store.CreateCheckpoint(ctx, sessionID, json.RawMessage(`{"step":0}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	info2, err := store.CreateCheckpoint(ctx, sessionID, json.RawMessage(`{"step":1}`), &info1)
	if err != nil {
		t.Fatal(err)
	}

	// All checkpoints
	all, err := store.RetrieveIndex(ctx, sessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}

	// Filter by parent
	children, err := store.RetrieveIndex(ctx, sessionID, &info1)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	if children[0].CheckpointID != info2.CheckpointID {
		t.Errorf("expected child %q, got %q", info2.CheckpointID, children[0].CheckpointID)
	}
}
