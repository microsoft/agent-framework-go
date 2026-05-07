// Copyright (c) Microsoft. All rights reserved.

package jsonstore_test

import (
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

	sessionID := "session-1"
	data := json.RawMessage(`{"stepNumber":42,"workflowInfo":{}}`)

	info, err := store.CreateCheckpoint(sessionID, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID != sessionID {
		t.Errorf("expected session %q, got %q", sessionID, info.SessionID)
	}

	got, err := store.RetrieveCheckpoint(sessionID, info)
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

	sessionID := "session-1"
	info1, err := store.CreateCheckpoint(sessionID, json.RawMessage(`{"step":0}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	info2, err := store.CreateCheckpoint(sessionID, json.RawMessage(`{"step":1}`), &info1)
	if err != nil {
		t.Fatal(err)
	}

	index, err := store.RetrieveIndex(sessionID, nil)
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

	sessionID := "session-persist"
	data := json.RawMessage(`{"hello":"world"}`)
	info, err := store.CreateCheckpoint(sessionID, data, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a new store instance over the same directory.
	store2, err := jsonstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store2.RetrieveCheckpoint(sessionID, info)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("data mismatch after reload:\n  want: %s\n  got:  %s", data, got)
	}

	index, err := store2.RetrieveIndex(sessionID, nil)
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

	_, err = store.RetrieveCheckpoint("session-1", workflow.CheckpointInfo{
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

	_, err = store.CreateCheckpoint("", json.RawMessage(`{}`), nil)
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

	_, err = store.CreateCheckpoint("s1", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(subdir); os.IsNotExist(err) {
		t.Fatal("expected directory to be created")
	}
}
