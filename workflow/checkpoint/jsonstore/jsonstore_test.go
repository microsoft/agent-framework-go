// Copyright (c) Microsoft. All rights reserved.

package jsonstore_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint/jsonstore"
)

func TestStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	sessionID := "session-1"
	data := json.RawMessage(`{"stepNumber":42,"workflowInfo":{}}`)

	info, err := store.CreateCheckpoint(ctx, sessionID, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID != sessionID {
		t.Errorf("expected session %q, got %q", sessionID, info.SessionID)
	}

	got, err := store.RetrieveCheckpoint(ctx, sessionID, info)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("data mismatch:\n  want: %s\n  got:  %s", data, got)
	}
}

func TestStore_Index(t *testing.T) {
	dir := t.TempDir()
	store, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	sessionID := "session-1"
	info1, err := store.CreateCheckpoint(ctx, sessionID, json.RawMessage(`{"step":0}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	info2, err := store.CreateCheckpoint(ctx, sessionID, json.RawMessage(`{"step":1}`), &info1)
	if err != nil {
		t.Fatal(err)
	}

	index, err := store.RetrieveIndex(ctx, sessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(index))
	}
	if index[0].CheckpointID != info1.CheckpointID {
		t.Errorf("index[0] = %q, want %q", index[0].CheckpointID, info1.CheckpointID)
	}
	if index[1].CheckpointID != info2.CheckpointID {
		t.Errorf("index[1] = %q, want %q", index[1].CheckpointID, info2.CheckpointID)
	}
}

func TestStore_PersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	store, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	sessionID := "session-persist"
	data := json.RawMessage(`{"hello":"world"}`)
	info, err := store.CreateCheckpoint(ctx, sessionID, data, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a new store instance over the same directory.
	store2, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store2.RetrieveCheckpoint(ctx, sessionID, info)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("data mismatch after reload:\n  want: %s\n  got:  %s", data, got)
	}

	index, err := store2.RetrieveIndex(ctx, sessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 1 {
		t.Fatalf("expected 1 entry after reload, got %d", len(index))
	}
}

func TestStore_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.RetrieveCheckpoint(context.Background(), "session-1", workflow.CheckpointInfo{
		SessionID:    "session-1",
		CheckpointID: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent checkpoint")
	}
}

func TestStore_EmptySessionID(t *testing.T) {
	dir := t.TempDir()
	store, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreateCheckpoint(context.Background(), "", json.RawMessage(`{}`), nil)
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
}

func TestStore_NonExistentDir(t *testing.T) {
	dir := t.TempDir()
	subdir := dir + "/nested/deep"
	store, err := jsonstore.New(subdir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreateCheckpoint(context.Background(), "s1", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(subdir); os.IsNotExist(err) {
		t.Fatal("expected directory to be created")
	}
}

func TestStore_ParentTracking(t *testing.T) {
	dir := t.TempDir()
	store, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

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

func TestStore_InvalidSessionID(t *testing.T) {
	dir := t.TempDir()
	store, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []string{"../escape", "a/b", "a\\b", "..", "."}
	for _, id := range tests {
		_, err := store.CreateCheckpoint(context.Background(), id, json.RawMessage(`{}`), nil)
		if err == nil {
			t.Errorf("expected error for session ID %q", id)
		}
	}
}
