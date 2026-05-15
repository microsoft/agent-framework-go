// Copyright (c) Microsoft. All rights reserved.

package execution_test

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
	runtimeobservability "github.com/microsoft/agent-framework-go/workflow/internal/observability"
	"github.com/microsoft/agent-framework-go/workflow/internal/workflowtest"
)

func TestPrepareDeliveryForDirectEdge(t *testing.T) {
	bools := []*bool{nil, ptr(true), ptr(false)}
	for _, conditionMatch := range bools {
		for _, targetMatch := range bools {
			name := fmt.Sprintf("condition=%v/target=%v", boolName(conditionMatch), boolName(targetMatch))
			t.Run(name, func(t *testing.T) {
				const messageVariant1 = "test"
				const messageVariant2 = "something else"

				var condition func(any) bool
				if conditionMatch != nil {
					condition = func(message any) bool {
						value, ok := message.(string)
						if *conditionMatch {
							return ok && value == messageVariant1
						}
						return ok && value == messageVariant2
					}
				}

				targetID := ""
				if targetMatch != nil {
					if *targetMatch {
						targetID = "executor2"
					} else {
						targetID = "executor1"
					}
				}

				edge := workflow.Edge{
					Connection: workflow.EdgeConnection{SourceIDs: []string{"executor1"}, SinkIDs: []string{"executor2"}},
					Condition:  condition,
				}
				runner := newTestEdgeRunner(edge)
				mapping, err := runner.PrepareDeliveryForEdge(context.Background(), edge, mustEnvelopeTarget(t, messageVariant1, "executor1", targetID))
				if err != nil {
					t.Fatalf("PrepareDeliveryForEdge: %v", err)
				}

				expectMessage := (conditionMatch == nil || *conditionMatch) && (targetMatch == nil || *targetMatch)
				if !expectMessage {
					requireNilMapping(t, mapping)
					return
				}
				requireMapping(t, mapping, []string{"executor2"}, []string{messageVariant1})
			})
		}
	}
}

func TestPrepareDeliveryForFanOutEdge(t *testing.T) {
	bools := []*bool{nil, ptr(true), ptr(false)}
	for _, assignerSelectsEmpty := range bools {
		for _, targetMatch := range bools {
			name := fmt.Sprintf("assignerEmpty=%v/target=%v", boolName(assignerSelectsEmpty), boolName(targetMatch))
			t.Run(name, func(t *testing.T) {
				var assigner func(int, any) iter.Seq[int]
				if assignerSelectsEmpty != nil {
					assigner = func(int, any) iter.Seq[int] {
						return func(yield func(int) bool) {
							if !*assignerSelectsEmpty {
								yield(0)
							}
						}
					}
				}

				targetID := ""
				if targetMatch != nil {
					if *targetMatch {
						targetID = "executor2"
					} else {
						targetID = "executor1"
					}
				}

				edge := workflow.Edge{
					Connection: workflow.EdgeConnection{SourceIDs: []string{"executor1"}, SinkIDs: []string{"executor2", "executor3"}},
					Assigner:   assigner,
				}
				runner := newTestEdgeRunner(edge)
				mapping, err := runner.PrepareDeliveryForEdge(context.Background(), edge, mustEnvelopeTarget(t, "test", "executor1", targetID))
				if err != nil {
					t.Fatalf("PrepareDeliveryForEdge: %v", err)
				}

				var expectedTargets []string
				if assignerSelectsEmpty == nil && targetMatch == nil {
					expectedTargets = []string{"executor2", "executor3"}
				} else if (assignerSelectsEmpty == nil || !*assignerSelectsEmpty) && (targetMatch == nil || *targetMatch) {
					expectedTargets = []string{"executor2"}
				}
				if len(expectedTargets) == 0 {
					requireNilMapping(t, mapping)
					return
				}
				requireMapping(t, mapping, expectedTargets, []string{"test"})
			})
		}
	}
}

func TestPrepareDeliveryForFanInEdge(t *testing.T) {
	edge := workflow.Edge{Index: 42, Connection: workflow.EdgeConnection{SourceIDs: []string{"executor1", "executor2"}, SinkIDs: []string{"executor3"}}}
	runner := newTestEdgeRunner(edge)

	for iteration := range 2 {
		t.Run(fmt.Sprintf("iteration-%d", iteration), func(t *testing.T) {
			requireNilMapping(t, prepareDelivery(t, runner, edge, mustEnvelopeTarget(t, "part1", "executor1", "")))
			requireNilMapping(t, prepareDelivery(t, runner, edge, mustEnvelopeTarget(t, "part-for-1", "executor2", "executor1")))
			requireNilMapping(t, prepareDelivery(t, runner, edge, mustEnvelopeTarget(t, "part2", "executor1", "executor3")))
			requireMapping(t, prepareDelivery(t, runner, edge, mustEnvelopeTarget(t, "final part", "executor2", "")), []string{"executor3"}, []string{"part1", "part2", "final part"})
		})
	}
}

func TestPrepareDeliveryForFanInEdgeConcurrentProcessing(t *testing.T) {
	const sourceCount = 4
	const iterations = 50
	sourceIDs := make([]string, 0, sourceCount)
	for index := range sourceCount {
		sourceIDs = append(sourceIDs, fmt.Sprintf("source%d", index))
	}
	edge := workflow.Edge{Index: 7, Connection: workflow.EdgeConnection{SourceIDs: sourceIDs, SinkIDs: []string{"sink"}}}
	runner := newTestEdgeRunner(edge)

	for iteration := range iterations {
		start := make(chan struct{})
		results := make(chan deliveryResult, sourceCount)
		var wg sync.WaitGroup
		for _, sourceID := range sourceIDs {
			envelope := mustEnvelopeTarget(t, "msg-from-"+sourceID, sourceID, "")
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				mapping, err := runner.PrepareDeliveryForEdge(context.Background(), edge, envelope)
				results <- deliveryResult{mapping: mapping, err: err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		var delivered []*execution.DeliveryMapping
		for result := range results {
			if result.err != nil {
				t.Fatalf("iteration %d PrepareDeliveryForEdge: %v", iteration, result.err)
			}
			if result.mapping != nil {
				delivered = append(delivered, result.mapping)
			}
		}
		if len(delivered) != 1 {
			t.Fatalf("iteration %d delivered mapping count = %d, want 1", iteration, len(delivered))
		}
		expectedMessages := make([]string, 0, sourceCount)
		for _, sourceID := range sourceIDs {
			expectedMessages = append(expectedMessages, "msg-from-"+sourceID)
		}
		requireMapping(t, delivered[0], []string{"sink"}, expectedMessages)
	}
}

func TestPrepareDeliveryForEdgeRecordsEdgeGroupMetadata(t *testing.T) {
	tests := []struct {
		name       string
		edge       workflow.Edge
		envelopes  []*execution.MessageEnvelope
		wantType   string
		wantSource string
		wantTarget string
	}{
		{
			name: "direct",
			edge: workflow.Edge{Connection: workflow.EdgeConnection{
				SourceIDs: []string{"source"},
				SinkIDs:   []string{"target"},
			}},
			envelopes: []*execution.MessageEnvelope{
				mustEnvelope(t, "message", "source"),
			},
			wantType:   "DirectEdgeRunner",
			wantSource: "source",
			wantTarget: "target",
		},
		{
			name: "fan out",
			edge: workflow.Edge{Connection: workflow.EdgeConnection{
				SourceIDs: []string{"source"},
				SinkIDs:   []string{"left", "right"},
			}},
			envelopes: []*execution.MessageEnvelope{
				mustEnvelope(t, "message", "source"),
			},
			wantType:   "FanOutEdgeRunner",
			wantSource: "source",
		},
		{
			name: "fan in",
			edge: workflow.Edge{Index: 42, Connection: workflow.EdgeConnection{
				SourceIDs: []string{"left", "right"},
				SinkIDs:   []string{"target"},
			}},
			envelopes: []*execution.MessageEnvelope{
				mustEnvelope(t, "left message", "left"),
				mustEnvelope(t, "right message", "right"),
			},
			wantType:   "FanInEdgeRunner",
			wantTarget: "target",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tracer := workflowtest.NewRecordingTracer()
			ctx := runtimeobservability.ContextWithTelemetry(context.Background(), runtimeobservability.New(runtimeobservability.Options{Tracer: tracer}))
			runner := execution.NewEdgeRunner(&workflow.Workflow{
				StartExecutorID: "start",
				Edges:           map[string][]workflow.Edge{"source": {testCase.edge}},
			}, nil, func(_ context.Context, targetID string, _ execution.StepTracer) (*workflow.Executor, error) {
				return stringExecutor(targetID), nil
			})

			for _, envelope := range testCase.envelopes {
				if _, err := runner.PrepareDeliveryForEdge(ctx, testCase.edge, envelope); err != nil {
					t.Fatalf("PrepareDeliveryForEdge returned error: %v", err)
				}
			}

			span := tracer.LastSpan(t)
			span.RequireAttributeValue(t, "edge_group.type", testCase.wantType)
			requireOptionalAttribute(t, span, "message.source_id", testCase.wantSource)
			requireOptionalAttribute(t, span, "message.target_id", testCase.wantTarget)
		})
	}
}

func mustEnvelope(t *testing.T, message string, sourceID string) *execution.MessageEnvelope {
	t.Helper()
	return mustEnvelopeTarget(t, message, sourceID, "")
}

func mustEnvelopeTarget(t *testing.T, message string, sourceID string, targetID string) *execution.MessageEnvelope {
	t.Helper()
	envelope, err := execution.NewMessageEnvelope(message, nil, sourceID, targetID)
	if err != nil {
		t.Fatalf("NewMessageEnvelope returned error: %v", err)
	}
	return envelope
}

func newTestEdgeRunner(edges ...workflow.Edge) *execution.EdgeRunner {
	return execution.NewEdgeRunner(&workflow.Workflow{
		StartExecutorID: "start",
		Edges:           map[string][]workflow.Edge{"source": edges},
	}, nil, func(_ context.Context, targetID string, _ execution.StepTracer) (*workflow.Executor, error) {
		return stringExecutor(targetID), nil
	})
}

func prepareDelivery(t *testing.T, runner *execution.EdgeRunner, edge workflow.Edge, envelope *execution.MessageEnvelope) *execution.DeliveryMapping {
	t.Helper()
	mapping, err := runner.PrepareDeliveryForEdge(context.Background(), edge, envelope)
	if err != nil {
		t.Fatalf("PrepareDeliveryForEdge: %v", err)
	}
	return mapping
}

func requireNilMapping(t *testing.T, mapping *execution.DeliveryMapping) {
	t.Helper()
	if mapping != nil {
		t.Fatalf("mapping = %+v, want nil", mapping)
	}
}

func requireMapping(t *testing.T, mapping *execution.DeliveryMapping, wantTargetIDs []string, wantMessages []string) {
	t.Helper()
	if mapping == nil {
		t.Fatal("mapping = nil, want delivery mapping")
	}
	gotTargetIDs := make([]string, 0, len(mapping.Targets))
	for _, target := range mapping.Targets {
		gotTargetIDs = append(gotTargetIDs, target.ID)
	}
	if !sameStringSet(gotTargetIDs, wantTargetIDs) {
		t.Fatalf("target IDs = %v, want %v", gotTargetIDs, wantTargetIDs)
	}
	gotMessages := make([]string, 0, len(mapping.Envelopes))
	for _, envelope := range mapping.Envelopes {
		message, ok := envelope.Message.(string)
		if !ok {
			t.Fatalf("message = %T, want string", envelope.Message)
		}
		gotMessages = append(gotMessages, message)
	}
	if !sameStringSet(gotMessages, wantMessages) {
		t.Fatalf("messages = %v, want %v", gotMessages, wantMessages)
	}
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}

func ptr(value bool) *bool {
	return &value
}

func boolName(value *bool) string {
	if value == nil {
		return "nil"
	}
	return fmt.Sprint(*value)
}

type deliveryResult struct {
	mapping *execution.DeliveryMapping
	err     error
}

func stringExecutor(id string) *workflow.Executor {
	return &workflow.Executor{
		ID: id,
		Spec: workflow.ExecutorSpec{
			ConfigureRoutes: func(builder *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
				return builder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*workflow.Context, any) (any, error) {
					return nil, nil
				}), nil
			},
		},
	}
}

func requireOptionalAttribute(t *testing.T, span *workflowtest.RecordingSpan, key string, want string) {
	t.Helper()
	if want == "" {
		span.RequireOmittedAttribute(t, key)
		return
	}
	span.RequireAttributeValue(t, key, want)
}
