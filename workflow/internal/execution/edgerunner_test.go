// Copyright (c) Microsoft. All rights reserved.

package execution_test

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

// edgeUnwrapOrder is a concrete message type used to verify that caller-supplied
// edge delegates (Condition, Assigner) receive the unwrapped value rather than a
// workflow.PortableValue wrapper.
type edgeUnwrapOrder struct{ Total int }

func edgeUnwrapExecutor(id string) *workflow.Executor {
	orderType := reflect.TypeFor[edgeUnwrapOrder]()
	return &workflow.Executor{
		ID: id,
		ConfigureProtocol: func(b *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			b.RouteBuilder.AddHandlerRaw(orderType, nil, func(*workflow.Context, any) (any, error) {
				return nil, nil
			})
			b.SendsMessageType(orderType)
			return b, nil
		},
	}
}

func edgeUnwrapBinding(id string) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "workflow_test.edgeUnwrapExecutor",
		NewExecutorFunc:  func(string) (*workflow.Executor, error) { return edgeUnwrapExecutor(id), nil },
	}
}

func newEdgeUnwrapRunner(t *testing.T, wf *workflow.Workflow) *execution.EdgeRunner {
	t.Helper()
	return execution.NewEdgeRunner(wf, nil, func(_ context.Context, id string, _ execution.StepTracer) (*workflow.Executor, error) {
		return edgeUnwrapExecutor(id), nil
	})
}

// A PortableValue message whose runtime type resolves must be unwrapped to its
// concrete type before the edge Condition is invoked, so a user predicate that
// type-asserts the concrete type routes the message instead of seeing (and
// rejecting) the PortableValue wrapper. Mirrors messageRouter.routeMessage and
// the .NET runtime, which unwraps PortableValue before invoking the delegate.
func TestPrepareDeliveryForEdge_UnwrapsPortableValueForCondition(t *testing.T) {
	source := edgeUnwrapBinding("source")
	sink := edgeUnwrapBinding("sink")

	builder := workflow.NewBuilder(source)
	var sawConcrete bool
	builder.AddDirectEdge(source, sink, false, func(m any) bool {
		_, ok := m.(edgeUnwrapOrder)
		sawConcrete = ok
		return ok
	})
	wf, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	runner := newEdgeUnwrapRunner(t, wf)
	envelope := &execution.MessageEnvelope{
		Message:  workflow.AnyPortableValue(edgeUnwrapOrder{Total: 150}),
		SourceID: "source",
	}

	mapping, err := runner.PrepareDeliveryForEdge(context.Background(), wf.Edges()["source"][0], envelope)
	if err != nil {
		t.Fatalf("PrepareDeliveryForEdge: %v", err)
	}
	if !sawConcrete {
		t.Fatal("Condition saw a PortableValue, want the concrete edgeUnwrapOrder")
	}
	if mapping == nil || len(mapping.Targets) != 1 || mapping.Targets[0].ID != "sink" {
		t.Fatalf("mapping = %+v, want delivery to sink", mapping)
	}
}

// The fan-out Assigner is also caller-supplied and must receive the unwrapped
// concrete message. Before unwrapping, an Assigner that type-asserts the
// concrete type yields no indices, dropping the delivery.
func TestPrepareDeliveryForEdge_UnwrapsPortableValueForAssigner(t *testing.T) {
	source := edgeUnwrapBinding("source")
	sinkA := edgeUnwrapBinding("sinkA")
	sinkB := edgeUnwrapBinding("sinkB")

	builder := workflow.NewBuilder(source)
	var sawConcrete bool
	assigner := func(_ int, m any) iter.Seq[int] {
		_, ok := m.(edgeUnwrapOrder)
		sawConcrete = ok
		return func(yield func(int) bool) {
			if ok {
				yield(0) // route to sinkA only
			}
		}
	}
	builder.AddFanOutEdge(source, []workflow.ExecutorBinding{sinkA, sinkB}, workflow.WithEdgeAssigner(assigner))
	wf, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	runner := newEdgeUnwrapRunner(t, wf)
	envelope := &execution.MessageEnvelope{
		Message:  workflow.AnyPortableValue(edgeUnwrapOrder{Total: 150}),
		SourceID: "source",
	}

	mapping, err := runner.PrepareDeliveryForEdge(context.Background(), wf.Edges()["source"][0], envelope)
	if err != nil {
		t.Fatalf("PrepareDeliveryForEdge: %v", err)
	}
	if !sawConcrete {
		t.Fatal("Assigner saw a PortableValue, want the concrete edgeUnwrapOrder")
	}
	if mapping == nil || len(mapping.Targets) != 1 || mapping.Targets[0].ID != "sinkA" {
		t.Fatalf("mapping = %+v, want delivery to sinkA", mapping)
	}
}

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
