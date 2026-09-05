// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

type typedEdgeMessage struct {
	Value string
}

func typedEdgeExecutor(id string) *workflow.Executor {
	messageType := reflect.TypeFor[typedEdgeMessage]()
	return &workflow.Executor{
		ID: id,
		ConfigureProtocol: func(builder *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			builder.RouteBuilder.AddHandlerRaw(messageType, nil, func(*workflow.Context, any) (any, error) {
				return nil, nil
			})
			builder.SendsMessageType(messageType)
			return builder, nil
		},
	}
}

func typedEdgeBinding(id string) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "workflow_test.typedEdgeExecutor",
		NewExecutorFunc:  func(string) (*workflow.Executor, error) { return typedEdgeExecutor(id), nil },
	}
}

func TestWithEdgeCondition_DecodesPortableValue(t *testing.T) {
	source := typedEdgeBinding("source")
	target := typedEdgeBinding("target")
	want := typedEdgeMessage{Value: "decoded"}
	var got typedEdgeMessage

	wf, err := workflow.NewBuilder(source).
		AddEdge(source, target, workflow.WithEdgeCondition(func(message typedEdgeMessage) bool {
			got = message
			return true
		})).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	message := delayedPortableValue(t, workflow.AnyPortableValue(want))
	mapping := prepareEdgeDelivery(t, wf, source.ID, message)
	if mapping == nil || len(mapping.Targets) != 1 || mapping.Targets[0].ID != target.ID {
		t.Fatalf("mapping = %+v, want delivery to %q", mapping, target.ID)
	}
	if got != want {
		t.Fatalf("Condition message = %+v, want %+v", got, want)
	}
}

func TestWithEdgeCondition_AnyPreservesPortableValue(t *testing.T) {
	source := typedEdgeBinding("source")
	target := typedEdgeBinding("target")
	var got any

	wf, err := workflow.NewBuilder(source).
		AddEdge(source, target, workflow.WithEdgeCondition(func(message any) bool {
			got = message
			return true
		})).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	message := delayedPortableValue(t, workflow.AnyPortableValue(typedEdgeMessage{Value: "wrapped"}))
	prepareEdgeDelivery(t, wf, source.ID, message)
	if _, ok := got.(workflow.PortableValue); !ok {
		t.Fatalf("Condition message type = %T, want workflow.PortableValue", got)
	}
}

func TestWithEdgeCondition_FailedConversionPassesZeroValue(t *testing.T) {
	source := typedEdgeBinding("source")
	target := typedEdgeBinding("target")
	got := typedEdgeMessage{Value: "not zero"}

	wf, err := workflow.NewBuilder(source).
		AddEdge(source, target, workflow.WithEdgeCondition(func(message typedEdgeMessage) bool {
			got = message
			return true
		})).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	message := delayedPortableValue(t, workflow.AnyPortableValue("wrong type"))
	prepareEdgeDelivery(t, wf, source.ID, message)
	if got != (typedEdgeMessage{}) {
		t.Fatalf("Condition message = %+v, want zero value", got)
	}
}

func TestWithEdgeCondition0_InvokesWithoutMessage(t *testing.T) {
	source := typedEdgeBinding("source")
	target := typedEdgeBinding("target")
	invocations := 0

	wf, err := workflow.NewBuilder(source).
		AddEdge(source, target, workflow.WithEdgeCondition0(func() bool {
			invocations++
			return true
		})).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	message := delayedPortableValue(t, workflow.AnyPortableValue(typedEdgeMessage{Value: "ignored"}))
	mapping := prepareEdgeDelivery(t, wf, source.ID, message)
	if mapping == nil || len(mapping.Targets) != 1 || mapping.Targets[0].ID != target.ID {
		t.Fatalf("mapping = %+v, want delivery to %q", mapping, target.ID)
	}
	if invocations != 1 {
		t.Fatalf("condition invocations = %d, want 1", invocations)
	}
	if info := wf.ReflectEdges()[source.ID][0]; !info.HasCondition {
		t.Fatal("edge metadata HasCondition = false, want true")
	}
}

func TestWithEdgeCondition0_NilRemainsNil(t *testing.T) {
	source := typedEdgeBinding("source")
	target := typedEdgeBinding("target")

	wf, err := workflow.NewBuilder(source).
		AddEdge(source, target, workflow.WithEdgeCondition0(nil)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wf.Edges()[source.ID][0].Condition != nil {
		t.Fatal("Condition is non-nil, want nil")
	}
}

func TestWithEdgeAssigner_DecodesPortableValue(t *testing.T) {
	source := typedEdgeBinding("source")
	targetA := typedEdgeBinding("targetA")
	targetB := typedEdgeBinding("targetB")
	want := typedEdgeMessage{Value: "decoded"}
	var got typedEdgeMessage

	wf, err := workflow.NewBuilder(source).
		AddFanOutEdge(source, []workflow.ExecutorBinding{targetA, targetB}, workflow.WithEdgeAssigner(
			func(_ int, message typedEdgeMessage) iter.Seq[int] {
				got = message
				return slices.Values([]int{0})
			},
		)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	message := delayedPortableValue(t, workflow.AnyPortableValue(want))
	mapping := prepareEdgeDelivery(t, wf, source.ID, message)
	if mapping == nil || len(mapping.Targets) != 1 || mapping.Targets[0].ID != targetA.ID {
		t.Fatalf("mapping = %+v, want delivery to %q", mapping, targetA.ID)
	}
	if got != want {
		t.Fatalf("Assigner message = %+v, want %+v", got, want)
	}
}

func TestWithEdgeAssigner_AnyPreservesPortableValue(t *testing.T) {
	source := typedEdgeBinding("source")
	target := typedEdgeBinding("target")
	var got any

	wf, err := workflow.NewBuilder(source).
		AddFanOutEdge(source, []workflow.ExecutorBinding{target}, workflow.WithEdgeAssigner(
			func(_ int, message any) iter.Seq[int] {
				got = message
				return nil
			},
		)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	message := delayedPortableValue(t, workflow.AnyPortableValue(typedEdgeMessage{Value: "wrapped"}))
	prepareEdgeDelivery(t, wf, source.ID, message)
	if _, ok := got.(workflow.PortableValue); !ok {
		t.Fatalf("Assigner message type = %T, want workflow.PortableValue", got)
	}
}

func TestWithEdgeAssigner_FailedConversionPassesZeroValue(t *testing.T) {
	source := typedEdgeBinding("source")
	target := typedEdgeBinding("target")
	got := typedEdgeMessage{Value: "not zero"}

	wf, err := workflow.NewBuilder(source).
		AddFanOutEdge(source, []workflow.ExecutorBinding{target}, workflow.WithEdgeAssigner(
			func(_ int, message typedEdgeMessage) iter.Seq[int] {
				got = message
				return nil
			},
		)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	message := delayedPortableValue(t, workflow.AnyPortableValue("wrong type"))
	prepareEdgeDelivery(t, wf, source.ID, message)
	if got != (typedEdgeMessage{}) {
		t.Fatalf("Assigner message = %+v, want zero value", got)
	}
}

func TestTypedEdgeOptions_NilDelegatesRemainNil(t *testing.T) {
	source := typedEdgeBinding("source")
	directTarget := typedEdgeBinding("direct")
	fanOutTarget := typedEdgeBinding("fanout")

	wf, err := workflow.NewBuilder(source).
		AddEdge(source, directTarget, workflow.WithEdgeCondition[typedEdgeMessage](nil)).
		AddFanOutEdge(source, []workflow.ExecutorBinding{fanOutTarget}, workflow.WithEdgeAssigner[typedEdgeMessage](nil)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	edges := wf.Edges()[source.ID]
	if edges[0].Condition != nil {
		t.Fatal("Condition is non-nil, want nil")
	}
	if edges[1].Assigner != nil {
		t.Fatal("Assigner is non-nil, want nil")
	}
}

func prepareEdgeDelivery(t *testing.T, wf *workflow.Workflow, sourceID string, message any) *execution.DeliveryMapping {
	t.Helper()
	runner := execution.NewEdgeRunner(wf, nil, func(_ context.Context, executorID string, _ execution.StepTracer) (*workflow.Executor, error) {
		binding, ok := wf.ExecutorBinding(executorID)
		if !ok {
			return nil, fmt.Errorf("executor binding %q not found", executorID)
		}
		return binding.NewExecutorFunc("")
	})
	mapping, err := runner.PrepareDeliveryForEdge(context.Background(), wf.Edges()[sourceID][0], &execution.MessageEnvelope{
		Message:  message,
		SourceID: sourceID,
	})
	if err != nil {
		t.Fatalf("PrepareDeliveryForEdge: %v", err)
	}
	return mapping
}
