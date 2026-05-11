// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Checkpoint and Rehydrate Workflow",
	"This sample demonstrates durable checkpointing: checkpoints are persisted to"+
		" disk so the workflow can be rehydrated in a new process after the original"+
		" process exits.",
)

// sessionIDFile persists the session ID between phases so the second phase can
// look up the checkpoint by session ID.
const sessionIDFile = "session_id.json"

func main() {
	// Build a workflow that halts waiting for an external approval, creating a
	// durable checkpoint on the way.
	approvalPort := workflow.RequestPort{
		ID:       "ApprovalPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}
	approval := workflow.BindRequestPort(approvalPort)
	finalize := workflow.BindFunc("FinalizeExecutor", true, func(response string) string {
		return "Workflow completed after rehydration: " + response
	})

	wf, err := workflow.NewBuilder(approval).
		AddEdge(approval, finalize).
		WithOutputFrom(finalize).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	// Use a temporary directory to simulate a durable store that survives process
	// restarts. In a real application this would be a persistent directory on disk.
	storeDir, err := os.MkdirTemp("", "checkpoint-rehydrate-*")
	if err != nil {
		demo.Panic(err)
	}
	defer func() {
		if err := os.RemoveAll(storeDir); err != nil {
			demo.Panic(err)
		}
	}()

	ctx := context.Background()

	// ── Phase 1: run the workflow until it halts at the approval request ─────

	demo.Assistantf("Phase 1: starting workflow with durable checkpoint store at %s", storeDir)

	store1, err := checkpoint.NewFileSystemJSONStore(storeDir)
	if err != nil {
		demo.Panic(err)
	}
	manager1 := checkpoint.NewJSONManager(store1)

	first, err := inproc.Default.WithCheckpointing(manager1).Run(ctx, wf, "Need deployment approval")
	if err != nil {
		demo.Panic(err)
	}

	checkpointInfo, ok := first.LastCheckpoint()
	if !ok {
		demo.Panic("expected checkpoint after first run")
	}
	demo.Assistantf("Checkpoint created: session=%s checkpoint=%s",
		checkpointInfo.SessionID, checkpointInfo.CheckpointID)

	request := firstRequest(first.OutgoingEvents())
	if request == nil {
		demo.Panic("expected pending approval request")
	}
	if err := first.Close(ctx); err != nil {
		demo.Panic(err)
	}

	// Persist the session ID so Phase 2 can locate the checkpoint.
	sessionJSON, err := json.Marshal(checkpointInfo.SessionID)
	if err != nil {
		demo.Panic(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, sessionIDFile), sessionJSON, 0o644); err != nil {
		demo.Panic(err)
	}

	// Close the store to release the file lock — simulates the process exiting.
	if err := store1.Close(); err != nil {
		demo.Panic(err)
	}
	demo.Assistant("Phase 1 complete. Store closed (simulating process exit).")

	// ── Phase 2: rehydrate the workflow in a "new process" ───────────────────

	demo.Assistant("Phase 2: rehydrating workflow from persisted checkpoint...")

	// Reopen the store — in a real process restart this would be the first thing
	// the new process does after reading the store directory from configuration.
	store2, err := checkpoint.NewFileSystemJSONStore(storeDir)
	if err != nil {
		demo.Panic(err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			demo.Panic(err)
		}
	}()
	manager2 := checkpoint.NewJSONManager(store2)

	// Read back the session ID that was persisted in Phase 1.
	sessionData, err := os.ReadFile(filepath.Join(storeDir, sessionIDFile))
	if err != nil {
		demo.Panic(err)
	}
	var sessionID string
	if err := json.Unmarshal(sessionData, &sessionID); err != nil {
		demo.Panic(err)
	}

	// List all checkpoints for the session and take the most recent one.
	checkpoints, err := store2.RetrieveIndex(ctx, sessionID, nil)
	if err != nil {
		demo.Panic(err)
	}
	if len(checkpoints) == 0 {
		demo.Panic("no checkpoints found in the store")
	}
	rehydrateFrom := checkpoints[len(checkpoints)-1]
	demo.Assistantf("Rehydrating from checkpoint: %s", rehydrateFrom.CheckpointID)

	resumed, err := inproc.Default.WithCheckpointing(manager2).Resume(ctx, wf, rehydrateFrom)
	if err != nil {
		demo.Panic(err)
	}

	// Deliver the approval response to the rehydrated run.
	response, err := request.NewResponse("approved-after-rehydration")
	if err != nil {
		demo.Panic(err)
	}
	if _, err := resumed.Resume(ctx, response); err != nil {
		demo.Panic(err)
	}
	for evt := range resumed.NewEvents() {
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}

func firstRequest(events func(func(workflow.Event) bool)) *workflow.ExternalRequest {
	for evt := range events {
		if req, ok := evt.(workflow.RequestInfoEvent); ok {
			return req.Request
		}
	}
	return nil
}
