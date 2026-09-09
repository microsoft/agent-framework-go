// Copyright (c) Microsoft. All rights reserved.

package execution_test

import (
	"context"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

// A caller-supplied fan-out Assigner (WithEdgeAssigner) may yield an index that
// is out of range for the edge's SinkIDs. Target selection must ignore such
// indices rather than panic the workflow runtime with an out-of-range access.
func TestPrepareDeliveryForFanOutEdge_AssignerOutOfRangeIndexIgnored(t *testing.T) {
	edge := workflow.Edge{
		Connection: workflow.EdgeConnection{SourceIDs: []string{"executor1"}, SinkIDs: []string{"executor2", "executor3"}},
		Assigner: func(int, any) iter.Seq[int] {
			return func(yield func(int) bool) {
				if !yield(0) { // valid -> executor2
					return
				}
				if !yield(5) { // out of range -> ignored (would panic)
					return
				}
				if !yield(-1) { // negative -> ignored
					return
				}
				yield(1) // valid -> executor3
			}
		},
	}

	runner, builtEdges := newTestEdgeRunner(t, edge)
	mapping, err := runner.PrepareDeliveryForEdge(context.Background(), builtEdges[0], mustEnvelopeTarget(t, "test", "executor1", ""))
	if err != nil {
		t.Fatalf("PrepareDeliveryForEdge: %v", err)
	}
	requireMapping(t, mapping, []string{"executor2", "executor3"}, []string{"test"})
}

// An Assigner that returns a nil iter.Seq must be tolerated: ranging over a nil
// function value panics, so target selection must guard against it.
func TestPrepareDeliveryForFanOutEdge_AssignerReturningNilSeqIsTolerated(t *testing.T) {
	edge := workflow.Edge{
		Connection: workflow.EdgeConnection{SourceIDs: []string{"executor1"}, SinkIDs: []string{"executor2", "executor3"}},
		Assigner:   func(int, any) iter.Seq[int] { return nil },
	}

	runner, builtEdges := newTestEdgeRunner(t, edge)
	mapping, err := runner.PrepareDeliveryForEdge(context.Background(), builtEdges[0], mustEnvelopeTarget(t, "test", "executor1", ""))
	if err != nil {
		t.Fatalf("PrepareDeliveryForEdge: %v", err)
	}
	requireNilMapping(t, mapping)
}
