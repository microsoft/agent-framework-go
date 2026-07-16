// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

// A caller-supplied fan-out Assigner (WithEdgeAssigner) may yield an index that
// is out of range for the edge's SinkIDs. Selecting targets must ignore such
// indices rather than panic the workflow runtime with an out-of-range access.
func TestSelectedTargetIDs_AssignerOutOfRangeIndexIgnored(t *testing.T) {
	edge := workflow.Edge{
		Connection: workflow.EdgeConnection{
			SourceIDs: []string{"src"},
			SinkIDs:   []string{"a", "b"},
		},
		Assigner: func(_ int, _ any) iter.Seq[int] {
			return func(yield func(int) bool) {
				if !yield(0) { // valid -> "a"
					return
				}
				if !yield(5) { // out of range -> must be ignored (would panic)
					return
				}
				if !yield(-1) { // negative -> must be ignored
					return
				}
				yield(1) // valid -> "b"
			}
		},
	}

	got := selectedTargetIDs(edge, &MessageEnvelope{Message: "msg"}) // must not panic
	want := []string{"a", "b"}
	if !slices.Equal(got, want) {
		t.Fatalf("selectedTargetIDs = %v, want %v", got, want)
	}
}
