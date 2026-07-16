// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

// A conditional edge on a source→target pair must not populate the
// conditionless-edge dedup set: adding a legitimate conditionless edge on the
// same pair afterwards should succeed, not be rejected as a duplicate.
func TestBuilder_ConditionalEdgeDoesNotBlockConditionlessEdge(t *testing.T) {
	start := newNoOpExecutor("start")
	target := newNoOpExecutor("target")

	_, err := workflow.NewBuilder(start).
		AddDirectEdge(start, target, false, func(any) bool { return true }).
		AddEdge(start, target).
		Build()
	if err != nil {
		t.Fatalf("conditionless edge after a conditional edge on the same pair should be allowed, got error: %v", err)
	}
}

// The idempotent path (AddChain / idempotent=true) must likewise not silently
// drop a conditionless edge just because a conditional edge preceded it.
func TestBuilder_ConditionalEdgeDoesNotDropIdempotentConditionlessEdge(t *testing.T) {
	start := newNoOpExecutor("start")
	target := newNoOpExecutor("target")

	wf, err := workflow.NewBuilder(start).
		AddDirectEdge(start, target, false, func(any) bool { return true }).
		AddDirectEdge(start, target, true, nil). // idempotent conditionless edge
		Build()
	if err != nil {
		t.Fatalf("unexpected build error: %v", err)
	}
	if wf == nil {
		t.Fatal("expected a workflow")
	}
}
