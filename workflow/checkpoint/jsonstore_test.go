// Copyright (c) Microsoft. All rights reserved.

package checkpoint_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
)

func newFileSystemJSONStore(t *testing.T, dir string) *checkpoint.FileSystemJSONStore {
	t.Helper()
	store, err := checkpoint.NewFileSystemJSONStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func checkpointFileName(sessionID string, info workflow.CheckpointInfo) string {
	protoPath := sessionID + "_" + info.CheckpointID + ".json"
	escaped := url.QueryEscape(protoPath)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	return strings.ReplaceAll(escaped, ".", "%2E")
}

func readIndexLines(t *testing.T, dir string) []struct {
	CheckpointInfo workflow.CheckpointInfo  `json:"checkpointInfo"`
	FileName       string                   `json:"fileName"`
	Parent         *workflow.CheckpointInfo `json:"parent,omitempty"`
} {
	t.Helper()

	file, err := os.Open(filepath.Join(dir, "index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	var entries []struct {
		CheckpointInfo workflow.CheckpointInfo  `json:"checkpointInfo"`
		FileName       string                   `json:"fileName"`
		Parent         *workflow.CheckpointInfo `json:"parent,omitempty"`
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry struct {
			CheckpointInfo workflow.CheckpointInfo  `json:"checkpointInfo"`
			FileName       string                   `json:"fileName"`
			Parent         *workflow.CheckpointInfo `json:"parent,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func pathIsUnder(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	for {
		if candidate == root {
			return true
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return false
		}
		candidate = parent
	}
}

func TestFileSystemJSONStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

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

func TestFileSystemJSONStore_RetrieveCheckpointReturnsPersistedData(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	ctx := context.Background()
	sessionID := "session-retrieve"
	data := json.RawMessage(`{"name":"test","value":42}`)

	info, err := store.CreateCheckpoint(ctx, sessionID, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	retrieved, err := store.RetrieveCheckpoint(ctx, sessionID, info)
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	if err := json.Unmarshal(retrieved, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "test" {
		t.Fatalf("name = %q, want %q", got.Name, "test")
	}
	if got.Value != 42 {
		t.Fatalf("value = %d, want %d", got.Value, 42)
	}
}

func TestFileSystemJSONStore_PersistsIndexImmediately(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	ctx := context.Background()
	sessionID := "session-index-flush"
	info, err := store.CreateCheckpoint(ctx, sessionID, json.RawMessage(`{"step":0}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	indexPath := filepath.Join(dir, "index.jsonl")
	indexStat, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if indexStat.Size() == 0 {
		t.Fatal("expected index.jsonl to be flushed before close")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	index := readIndexLines(t, dir)
	if len(index) != 1 {
		t.Fatalf("expected 1 index entry, got %d", len(index))
	}
	if index[0].CheckpointInfo != info {
		t.Fatalf("index entry = %+v, want %+v", index[0].CheckpointInfo, info)
	}
	if index[0].FileName != checkpointFileName(sessionID, info) {
		t.Fatalf("index file name = %q, want %q", index[0].FileName, checkpointFileName(sessionID, info))
	}
}

func TestFileSystemJSONStore_Index(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

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

func TestFileSystemJSONStore_PersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	ctx := context.Background()
	sessionID := "session-persist"
	data := json.RawMessage(`{"hello":"world"}`)
	info, err := store.CreateCheckpoint(ctx, sessionID, data, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Create a new store instance over the same directory.
	store2 := newFileSystemJSONStore(t, dir)

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

func TestFileSystemJSONStore_NotFound(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	_, err := store.RetrieveCheckpoint(context.Background(), "session-1", workflow.CheckpointInfo{
		SessionID:    "session-1",
		CheckpointID: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent checkpoint")
	}
}

func TestFileSystemJSONStore_EmptySessionID(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	_, err := store.CreateCheckpoint(context.Background(), "", json.RawMessage(`{}`), nil)
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
}

func TestFileSystemJSONStore_NonExistentDir(t *testing.T) {
	dir := t.TempDir()
	subdir := dir + "/nested/deep"
	store := newFileSystemJSONStore(t, subdir)

	_, err := store.CreateCheckpoint(context.Background(), "s1", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(subdir); os.IsNotExist(err) {
		t.Fatal("expected directory to be created")
	}
}

func TestFileSystemJSONStore_ParentTracking(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

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

func TestFileSystemJSONStore_CreateCheckpointRejectsMismatchedParentSession(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	parent := workflow.CheckpointInfo{SessionID: "other-session", CheckpointID: "parent"}
	_, err := store.CreateCheckpoint(context.Background(), "session", json.RawMessage(`{}`), &parent)
	if err == nil {
		t.Fatal("expected mismatched parent session error")
	}
}

func TestFileSystemJSONStore_RetrieveCheckpointRejectsMismatchedSession(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	info, err := store.CreateCheckpoint(context.Background(), "session", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RetrieveCheckpoint(context.Background(), "other-session", info)
	if err == nil {
		t.Fatal("expected mismatched checkpoint session error")
	}
}

func TestFileSystemJSONStore_RetrieveIndexRejectsMismatchedParentSession(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	parent := workflow.CheckpointInfo{SessionID: "other-session", CheckpointID: "parent"}
	_, err := store.RetrieveIndex(context.Background(), "session", &parent)
	if err == nil {
		t.Fatal("expected mismatched parent session error")
	}
}

func TestFileSystemJSONStore_RetrieveIndexAllowsZeroParentFilter(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	var parent workflow.CheckpointInfo
	index, err := store.RetrieveIndex(context.Background(), "session", &parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 0 {
		t.Fatalf("index length = %d, want 0", len(index))
	}
}

func runEscapeRootFolderTest(t *testing.T, escapingPath string) {
	t.Helper()

	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	naivePath := filepath.Join(dir, escapingPath)
	if pathIsUnder(dir, naivePath) {
		t.Fatalf("test setup invalid: naive path %q should be outside root %q", naivePath, dir)
	}

	info, err := store.CreateCheckpoint(context.Background(), escapingPath, json.RawMessage(`{"test":"data"}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	naivePathWithCheckpointID := filepath.Join(dir, escapingPath+"_"+info.CheckpointID+".json")
	if _, err := os.Stat(naivePathWithCheckpointID); err == nil {
		t.Fatalf("naive path exists: %s", naivePathWithCheckpointID)
	}

	actualPath := filepath.Join(dir, checkpointFileName(escapingPath, info))
	if !pathIsUnder(dir, actualPath) {
		t.Fatalf("actual path %q should be under root %q", actualPath, dir)
	}
	if _, err := os.Stat(actualPath); err != nil {
		t.Fatalf("expected escaped checkpoint file: %v", err)
	}
}

func TestFileSystemJSONStore_CreateCheckpointShouldNotEscapeRootFolder(t *testing.T) {
	runEscapeRootFolderTest(t, "../valid_suffix")
	if runtime.GOOS == "windows" {
		runEscapeRootFolderTest(t, "..\\valid_suffix")
	}
}

func TestFileSystemJSONStore_CreateCheckpointEscapesInvalidChars(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	const invalidPathCharsWin32 = `\/:*?"<>|`
	const invalidPathCharsUnix = `/`
	const invalidPathCharsMacOS = `/:`

	for _, invalidChars := range []string{invalidPathCharsWin32, invalidPathCharsUnix, invalidPathCharsMacOS} {
		runID := "prefix_" + invalidChars + "_suffix"
		info, err := store.CreateCheckpoint(context.Background(), runID, json.RawMessage(`{"test":"data"}`), nil)
		if err != nil {
			t.Errorf("CreateCheckpoint(%q): %v", runID, err)
		}
		actualPath := filepath.Join(dir, checkpointFileName(runID, info))
		if _, err := os.Stat(actualPath); err != nil {
			t.Errorf("expected escaped checkpoint file for session ID %q: %v", runID, err)
		}
	}
}

func TestFileSystemJSONStore_SameDirectoryAlreadyInUse(t *testing.T) {
	dir := t.TempDir()
	store := newFileSystemJSONStore(t, dir)

	if _, err := checkpoint.NewFileSystemJSONStore(dir); err == nil {
		t.Fatal("expected error for store directory already in use")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := checkpoint.NewFileSystemJSONStore(dir)
	if err != nil {
		t.Fatalf("expected store to reopen after close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}
