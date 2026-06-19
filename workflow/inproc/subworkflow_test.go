// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func TestSubworkflowBinding_ForwardsOutputsAsParentOutputsAndMessages(t *testing.T) {
	childStart := workflow.NewExecutor("child-start", func(in textMessage) dataMessage {
		return dataMessage{Bytes: []byte(in.Text)}
	}).Bind()
	child, err := workflow.NewBuilder(childStart).
		WithOutputFrom(childStart).
		Build()
	if err != nil {
		t.Fatalf("Build child: %v", err)
	}

	host := inproc.BindSubworkflowAsExecutor(child, "child")
	var gotAtSink []dataMessage
	sink := workflow.NewExecutor("sink", func(msg dataMessage) {
		gotAtSink = append(gotAtSink, msg)
	}).Bind()
	parent, err := workflow.NewBuilder(host).
		AddEdge(host, sink).
		WithOutputFrom(host).
		Build()
	if err != nil {
		t.Fatalf("Build parent: %v", err)
	}

	run, err := inproc.Lockstep.Run(t.Context(), parent, textMessage{Text: "abc"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(gotAtSink) != 1 || string(gotAtSink[0].Bytes) != "abc" {
		t.Fatalf("sink messages = %#v, want one dataMessage abc", gotAtSink)
	}
	outputs := outputEvents(slicesCollect(run.OutgoingEvents()))
	if len(outputs) != 1 {
		t.Fatalf("output count = %d, want 1", len(outputs))
	}
	if outputs[0].ExecutorID != "child" {
		t.Fatalf("OutputEvent.ExecutorID = %q, want child", outputs[0].ExecutorID)
	}
	got, ok := outputs[0].Output.(dataMessage)
	if !ok || string(got.Bytes) != "abc" {
		t.Fatalf("OutputEvent.Output = %#v, want dataMessage abc", outputs[0].Output)
	}
}

func TestSubworkflowBinding_QualifiedRequestPortRoundTrip(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "ask",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}

	childStart := workflow.BindNewExecutorFunc("child-start", func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.YieldsOutputType(reflect.TypeFor[string]())
				pb.RouteBuilder.
					AddHandlerRaw(reflect.TypeFor[textMessage](), nil, func(ctx *workflow.Context, msg any) (any, error) {
						req, err := workflow.NewExternalRequest("", port, "question")
						if err != nil {
							return nil, err
						}
						return nil, ctx.PostRequest(req)
					}).
					AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(ctx *workflow.Context, msg any) (any, error) {
						resp := msg.(*workflow.ExternalResponse)
						value, _ := resp.Data.As(port.Response)
						return nil, ctx.YieldOutput(value)
					})
				return pb, nil
			},
		}, nil
	})
	child, err := workflow.NewBuilder(childStart).
		WithOutputFrom(childStart).
		Build()
	if err != nil {
		t.Fatalf("Build child: %v", err)
	}

	host := inproc.BindSubworkflowAsExecutor(child, "child")
	qualifiedPort := workflow.RequestPort{
		ID:       "child.ask",
		Request:  port.Request,
		Response: port.Response,
	}
	qualifiedPortBinding := qualifiedPort.Bind()
	parent, err := workflow.NewBuilder(host).
		AddDirectEdge(host, qualifiedPortBinding, false, externalRequestOnly).
		AddDirectEdge(qualifiedPortBinding, host, false, externalResponseOnly).
		WithOutputFrom(host).
		Build()
	if err != nil {
		t.Fatalf("Build parent: %v", err)
	}

	run, err := inproc.Lockstep.Run(t.Context(), parent, textMessage{Text: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	request := firstRequest(t, run.OutgoingEvents())
	if request.PortInfo.PortID != "child.ask" {
		t.Fatalf("request port = %q, want child.ask", request.PortInfo.PortID)
	}
	response, err := request.CreateResponse("answer")
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := run.Resume(t.Context(), response); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	outputs := outputEvents(slicesCollect(run.NewEvents()))
	if len(outputs) != 1 {
		t.Fatalf("output count = %d, want 1", len(outputs))
	}
	if outputs[0].Output != "answer" {
		t.Fatalf("OutputEvent.Output = %#v, want answer", outputs[0].Output)
	}
}

func TestCheckpoint_Resume_SubworkflowWithPendingRequests_RepublishesQualifiedRequestInfoEvents(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			wf := createCheckpointedSubworkflowRequestWorkflow(t)
			manager := checkpoint.NewInMemoryManager()
			ctx := t.Context()

			firstRun, err := env.env.WithCheckpointing(manager).RunStreaming(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("RunStreaming: %v", err)
			}
			pendingRequest, checkpointInfo := capturePendingRequestAndCheckpointFromStream(t, ctx, firstRun)
			if err := firstRun.Close(ctx); err != nil {
				t.Fatalf("Close first run: %v", err)
			}

			resumed, err := env.env.WithCheckpointing(manager).ResumeStreaming(ctx, wf, checkpointInfo)
			if err != nil {
				t.Fatalf("ResumeStreaming: %v", err)
			}
			defer func() { _ = resumed.Close(ctx) }()

			resumedEvents := readStreamToHalt(t, ctx, resumed)
			replayedRequests := requestsFromEvents(resumedEvents)
			if len(replayedRequests) != 1 {
				t.Fatalf("replayed request count = %d, want 1", len(replayedRequests))
			}
			if replayedRequests[0].RequestID != pendingRequest.RequestID {
				t.Fatalf("replayed request ID = %q, want %q", replayedRequests[0].RequestID, pendingRequest.RequestID)
			}
			if replayedRequests[0].PortInfo.PortID != pendingRequest.PortInfo.PortID {
				t.Fatalf("replayed port = %q, want %q", replayedRequests[0].PortInfo.PortID, pendingRequest.PortInfo.PortID)
			}

			response, err := replayedRequests[0].CreateResponse("World")
			if err != nil {
				t.Fatalf("CreateResponse: %v", err)
			}
			if err := resumed.SendResponse(ctx, response); err != nil {
				t.Fatalf("SendResponse: %v", err)
			}

			completionEvents := readStreamToHalt(t, ctx, resumed)
			if requests := requestsFromEvents(completionEvents); len(requests) != 0 {
				t.Fatalf("completion replayed requests = %d, ids = %v, want 0", len(requests), requestIDs(requests))
			}
			if hasErrorEvents(completionEvents) {
				t.Fatalf("completion events contain workflow errors: %#v", completionEvents)
			}
			status, err := resumed.GetStatus(ctx)
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if status != inproc.RunStatusIdle {
				t.Fatalf("status = %v, want Idle", status)
			}
		})
	}
}

func TestSubworkflowBinding_CheckpointedEchoSampleCanResumeTwice(t *testing.T) {
	for _, env := range subworkflowExecutionEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			echo := stringTransformBinding("echo", func(s string) string { return s })
			sub, err := workflow.NewBuilder(echo).
				WithOutputFrom(echo).
				Build()
			if err != nil {
				t.Fatalf("Build subworkflow: %v", err)
			}

			host := inproc.BindSubworkflowAsExecutor(sub, "EchoSubworkflow")
			parent, err := workflow.NewBuilder(host).
				WithOutputFrom(host).
				Build()
			if err != nil {
				t.Fatalf("Build parent: %v", err)
			}

			manager := checkpoint.NewInMemoryManager()
			var resumeFrom workflow.CheckpointInfo
			for step := 1; step <= 2; step++ {
				input := "[" + strconv.Itoa(step) + "] Hello, World!"
				checkpointInfo, outputs := runCheckpointedEchoSubworkflow(t, env.env.WithCheckpointing(manager), parent, input, resumeFrom)
				resumeFrom = checkpointInfo

				if len(outputs) != 1 {
					t.Fatalf("step %d output count = %d, want 1", step, len(outputs))
				}
				if outputs[0].ExecutorID != "EchoSubworkflow" {
					t.Fatalf("step %d output executor = %q, want EchoSubworkflow", step, outputs[0].ExecutorID)
				}
				if outputs[0].Output != input {
					t.Fatalf("step %d output = %#v, want %q", step, outputs[0].Output, input)
				}
			}
		})
	}
}

func TestSubworkflowBinding_TextProcessingSample(t *testing.T) {
	for _, env := range subworkflowExecutionEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			uppercase := stringTransformBinding("uppercase", strings.ToUpper)
			reverse := stringTransformBinding("reverse", reverseString)
			appendSuffix := stringTransformBinding("append", func(s string) string { return s + " [PROCESSED]" })
			sub, err := workflow.NewBuilder(uppercase).
				AddEdge(uppercase, reverse).
				AddEdge(reverse, appendSuffix).
				WithOutputFrom(appendSuffix).
				Build()
			if err != nil {
				t.Fatalf("Build subworkflow: %v", err)
			}

			host := inproc.BindSubworkflowAsExecutor(sub, "TextProcessingSubWorkflow")
			prefix := stringTransformBinding("prefix", func(s string) string { return "INPUT: " + s })
			post := stringTransformBinding("post", func(s string) string { return "[FINAL] " + s + " [END]" })
			parent, err := workflow.NewBuilder(prefix).
				AddEdge(prefix, host).
				AddEdge(host, post).
				WithOutputFrom(post).
				Build()
			if err != nil {
				t.Fatalf("Build parent: %v", err)
			}

			events := runWorkflowToHalt(t, env.env, parent, "hello")
			outputs := outputEvents(events)
			if len(outputs) != 1 {
				t.Fatalf("output count = %d, want 1", len(outputs))
			}
			want := "[FINAL] OLLEH :TUPNI [PROCESSED] [END]"
			if outputs[0].Output != want {
				t.Fatalf("output = %#v, want %q", outputs[0].Output, want)
			}
		})
	}
}

func TestSubworkflowBinding_NestedOrderProcessingSample(t *testing.T) {
	for _, env := range subworkflowExecutionEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			analyzePatterns := orderAnalyzePatternsBinding("AnalyzePatterns", 2)
			calculateRiskScore := orderCalculateRiskScoreBinding("CalculateRiskScore")
			fraudCheck, err := workflow.NewBuilder(analyzePatterns).
				AddEdge(analyzePatterns, calculateRiskScore).
				WithOutputFrom(calculateRiskScore).
				Build()
			if err != nil {
				t.Fatalf("Build fraud check subworkflow: %v", err)
			}

			validatePayment := orderTransformBinding("ValidatePayment", func(order sampleOrderInfo) sampleOrderInfo { return order })
			fraudCheckHost := inproc.BindSubworkflowAsExecutor(fraudCheck, "FraudCheck")
			chargePayment := orderTransformBinding("ChargePayment", func(order sampleOrderInfo) sampleOrderInfo {
				order.PaymentTransactionID = "TXN-ORD-001"
				return order
			})
			payment, err := workflow.NewBuilder(validatePayment).
				AddEdge(validatePayment, fraudCheckHost).
				AddEdge(fraudCheckHost, chargePayment).
				WithOutputFrom(chargePayment).
				Build()
			if err != nil {
				t.Fatalf("Build payment subworkflow: %v", err)
			}

			selectCarrier := orderSelectCarrierBinding("SelectCarrier")
			createShipment := orderTransformBinding("CreateShipment", func(order sampleOrderInfo) sampleOrderInfo {
				order.TrackingNumber = "TRACK-ORD-001"
				return order
			})
			shipping, err := workflow.NewBuilder(selectCarrier).
				AddEdge(selectCarrier, createShipment).
				WithOutputFrom(createShipment).
				Build()
			if err != nil {
				t.Fatalf("Build shipping subworkflow: %v", err)
			}

			orderReceived := orderReceivedBinding("OrderReceived")
			paymentHost := inproc.BindSubworkflowAsExecutor(payment, "Payment")
			shippingHost := inproc.BindSubworkflowAsExecutor(shipping, "Shipping")
			orderCompleted := orderCompletedBinding("OrderCompleted")
			parent, err := workflow.NewBuilder(orderReceived).
				AddEdge(orderReceived, paymentHost).
				AddEdge(paymentHost, shippingHost).
				AddEdge(shippingHost, orderCompleted).
				WithOutputFrom(orderCompleted).
				Build()
			if err != nil {
				t.Fatalf("Build order processing workflow: %v", err)
			}

			events := runWorkflowToHalt(t, env.env, parent, "ORD-001")
			if hasErrorEvents(events) {
				t.Fatalf("events contain workflow errors: %#v", errorEvents(events))
			}

			var riskEvents []sampleFraudRiskAssessedEvent
			for _, event := range events {
				if riskEvent, ok := event.(sampleFraudRiskAssessedEvent); ok {
					riskEvents = append(riskEvents, riskEvent)
				}
			}
			if len(riskEvents) != 1 {
				t.Fatalf("risk event count = %d, want 1", len(riskEvents))
			}
			if riskEvents[0].RiskScore != 53 {
				t.Fatalf("risk score = %d, want 53", riskEvents[0].RiskScore)
			}

			outputs := outputEvents(events)
			if len(outputs) != 1 {
				t.Fatalf("output count = %d, want 1", len(outputs))
			}
			want := "Order ORD-001 completed. Tracking: TRACK-ORD-001"
			if outputs[0].Output != want {
				t.Fatalf("output = %#v, want %q", outputs[0].Output, want)
			}
		})
	}
}

func TestSubworkflowBinding_SharedStateWorksWithinSubworkflow(t *testing.T) {
	for _, env := range subworkflowStateEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			text := "    Lorem ipsum dolor sit amet, consectetur adipiscing elit.  "
			textRead := textReadBinding("text-read")
			textTrim := textTrimBinding("text-trim")
			charCount := charCountBinding("char-count")
			sub, err := workflow.NewBuilder(textRead).
				AddEdge(textRead, textTrim).
				AddEdge(textTrim, charCount).
				WithOutputFrom(charCount).
				Build()
			if err != nil {
				t.Fatalf("Build subworkflow: %v", err)
			}

			host := inproc.BindSubworkflowAsExecutor(sub, "internalStateSubworkflow")
			parent, err := workflow.NewBuilder(host).
				WithOutputFrom(host).
				Build()
			if err != nil {
				t.Fatalf("Build parent: %v", err)
			}

			events := runWorkflowToHalt(t, env.env, parent, text)
			outputs := outputEvents(events)
			if len(outputs) != 1 {
				t.Fatalf("output count = %d, want 1", len(outputs))
			}
			want := len(strings.TrimSpace(text))
			if outputs[0].Output != want {
				t.Fatalf("output = %#v, want %d", outputs[0].Output, want)
			}
		})
	}
}

func TestSubworkflowBinding_SharedStateIsIsolatedAcrossSubworkflowBoundary(t *testing.T) {
	for _, env := range subworkflowStateEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			textTrim := textTrimBinding("text-trim")
			sub, err := workflow.NewBuilder(textTrim).
				WithOutputFrom(textTrim).
				Build()
			if err != nil {
				t.Fatalf("Build subworkflow: %v", err)
			}

			textRead := textReadBinding("text-read")
			host := inproc.BindSubworkflowAsExecutor(sub, "textTrimSubworkflow")
			charCount := charCountBinding("char-count")
			parent, err := workflow.NewBuilder(textRead).
				AddEdge(textRead, host).
				AddEdge(host, charCount).
				WithOutputFrom(charCount).
				Build()
			if err != nil {
				t.Fatalf("Build parent: %v", err)
			}

			events := runWorkflowToHalt(t, env.env, parent, "    Lorem ipsum  ")
			errors := errorEvents(events)
			if len(errors) == 0 {
				t.Fatal("expected workflow error from isolated subworkflow state, got none")
			}
			if errors[0].SubWorkflowID != "textTrimSubworkflow" {
				t.Fatalf("SubWorkflowID = %q, want textTrimSubworkflow", errors[0].SubWorkflowID)
			}
		})
	}
}

func slicesCollect(seq func(func(workflow.Event) bool)) []workflow.Event {
	var events []workflow.Event
	for event := range seq {
		events = append(events, event)
	}
	return events
}

func runWorkflowToHalt(t *testing.T, env *inproc.ExecutionEnvironment, wf *workflow.Workflow, input any) []workflow.Event {
	t.Helper()
	ctx := t.Context()
	run, err := env.RunStreaming(ctx, wf, input)
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	defer func() { _ = run.Close(ctx) }()
	return readStreamToHalt(t, ctx, run)
}

func runCheckpointedEchoSubworkflow(t *testing.T, env *inproc.ExecutionEnvironment, wf *workflow.Workflow, input string, resumeFrom workflow.CheckpointInfo) (workflow.CheckpointInfo, []workflow.OutputEvent) {
	t.Helper()
	ctx := t.Context()
	var (
		run *inproc.StreamingRun
		err error
	)
	if resumeFrom == (workflow.CheckpointInfo{}) {
		run, err = env.RunStreaming(ctx, wf, input)
	} else {
		run, err = env.ResumeStreaming(ctx, wf, resumeFrom)
		if err == nil {
			err = run.SendMessage(ctx, input)
		}
	}
	if err != nil {
		t.Fatalf("start checkpointed echo run: %v", err)
	}
	defer func() { _ = run.Close(ctx) }()

	events := readStreamToHalt(t, ctx, run)
	checkpointInfo, ok := run.LastCheckpoint()
	if !ok {
		t.Fatal("expected checkpoint")
	}
	return checkpointInfo, outputEvents(events)
}

func createCheckpointedSubworkflowRequestWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	innerPort := workflow.RequestPort{
		ID:       "InnerTestPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}
	innerProcessor := stringTransformBinding("InnerProcessor", func(s string) string { return s })
	child, err := workflow.NewBuilder(innerPort.Bind()).
		AddEdge(innerPort.Bind(), innerProcessor).
		Build()
	if err != nil {
		t.Fatalf("Build child request workflow: %v", err)
	}

	host := inproc.BindSubworkflowAsExecutor(child, "Subworkflow")
	qualifiedPort := workflow.RequestPort{
		ID:       "ForwardedSubworkflowRequest",
		Request:  innerPort.Request,
		Response: innerPort.Response,
	}
	parent, err := workflow.NewBuilder(host).
		AddDirectEdge(host, qualifiedPort.Bind(), false, externalRequestOnly).
		AddDirectEdge(qualifiedPort.Bind(), host, false, externalResponseOnly).
		Build()
	if err != nil {
		t.Fatalf("Build parent request workflow: %v", err)
	}
	return parent
}

type subworkflowTestEnvironment struct {
	name string
	env  *inproc.ExecutionEnvironment
}

func subworkflowExecutionEnvironments() []subworkflowTestEnvironment {
	return []subworkflowTestEnvironment{
		{name: "lockstep", env: inproc.Lockstep},
		{name: "off_thread", env: inproc.OffThread},
		{name: "concurrent", env: inproc.Concurrent},
	}
}

func subworkflowStateEnvironments() []subworkflowTestEnvironment {
	return []subworkflowTestEnvironment{
		{name: "lockstep", env: inproc.Lockstep},
		{name: "off_thread", env: inproc.OffThread},
	}
}

func stringTransformBinding(id string, transform func(string) string) workflow.ExecutorBinding {
	binding := workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[string](), func(_ *workflow.Context, msg any) (any, error) {
					return transform(msg.(string)), nil
				})
				return pb, nil
			},
		}, nil
	})
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func externalRequestOnly(msg any) bool {
	_, ok := msg.(*workflow.ExternalRequest)
	return ok
}

func externalResponseOnly(msg any) bool {
	_, ok := msg.(*workflow.ExternalResponse)
	return ok
}

const wordStateScope = "WordStateScope"

func textReadBinding(id string) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.SendsMessageType(reflect.TypeFor[string]())
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					key := uuid.NewString()
					if err := ctx.QueueStateUpdate(key, wordStateScope, msg.(string)); err != nil {
						return nil, err
					}
					return nil, ctx.SendMessage("", key)
				})
				return pb, nil
			},
		}, nil
	})
}

func textTrimBinding(id string) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.SendsMessageType(reflect.TypeFor[string]()).YieldsOutputType(reflect.TypeFor[string]())
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					key := msg.(string)
					value, err := ctx.ReadState(key, wordStateScope)
					if err != nil {
						return nil, err
					}
					if value == nil {
						return nil, &stateNotFoundError{key: key}
					}
					trimmed := strings.TrimSpace(value.(string))
					if err := ctx.QueueStateUpdate(key, wordStateScope, trimmed); err != nil {
						return nil, err
					}
					return nil, ctx.SendMessage("", key)
				})
				return pb, nil
			},
		}, nil
	})
}

func charCountBinding(id string) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.YieldsOutputType(reflect.TypeFor[int]())
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					value, err := ctx.ReadState(msg.(string), wordStateScope)
					if err != nil {
						return nil, err
					}
					if value == nil {
						return nil, ctx.YieldOutput(0)
					}
					return nil, ctx.YieldOutput(len(value.(string)))
				})
				return pb, nil
			},
		}, nil
	})
}

type sampleOrderInfo struct {
	OrderID              string
	Amount               int
	PaymentTransactionID string
	TrackingNumber       string
	Carrier              string
}

type sampleFraudRiskAssessedEvent struct {
	RiskScore int
}

func (e sampleFraudRiskAssessedEvent) Data() any {
	return e.RiskScore
}

const sampleFraudStateScope = "FraudAnalysis"

func orderReceivedBinding(id string) workflow.ExecutorBinding {
	binding := workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[sampleOrderInfo](), func(_ *workflow.Context, msg any) (any, error) {
					return sampleOrderInfo{OrderID: msg.(string), Amount: 9999}, nil
				})
				return pb, nil
			},
		}, nil
	})
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func orderTransformBinding(id string, transform func(sampleOrderInfo) sampleOrderInfo) workflow.ExecutorBinding {
	binding := workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleOrderInfo](), reflect.TypeFor[sampleOrderInfo](), func(_ *workflow.Context, msg any) (any, error) {
					return transform(msg.(sampleOrderInfo)), nil
				})
				return pb, nil
			},
		}, nil
	})
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func orderAnalyzePatternsBinding(id string, patternsFound int) workflow.ExecutorBinding {
	binding := workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleOrderInfo](), reflect.TypeFor[sampleOrderInfo](), func(ctx *workflow.Context, msg any) (any, error) {
					if err := ctx.QueueStateUpdate("patternsFound", sampleFraudStateScope, patternsFound); err != nil {
						return nil, err
					}
					return msg.(sampleOrderInfo), nil
				})
				return pb, nil
			},
		}, nil
	})
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func orderCalculateRiskScoreBinding(id string) workflow.ExecutorBinding {
	binding := workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleOrderInfo](), reflect.TypeFor[sampleOrderInfo](), func(ctx *workflow.Context, msg any) (any, error) {
					value, err := ctx.ReadState("patternsFound", sampleFraudStateScope)
					if err != nil {
						return nil, err
					}
					patternsFound, ok := value.(int)
					if !ok {
						return nil, fmt.Errorf("patternsFound state has type %T, want int", value)
					}
					riskScore := patternsFound*20 + 13
					if err := ctx.AddEvent(sampleFraudRiskAssessedEvent{RiskScore: riskScore}); err != nil {
						return nil, err
					}
					return msg.(sampleOrderInfo), nil
				})
				return pb, nil
			},
		}, nil
	})
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func orderSelectCarrierBinding(id string) workflow.ExecutorBinding {
	binding := workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.SendsMessageType(reflect.TypeFor[sampleOrderInfo]())
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleOrderInfo](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					order := msg.(sampleOrderInfo)
					order.Carrier = "Express"
					return nil, ctx.SendMessage("", order)
				})
				return pb, nil
			},
		}, nil
	})
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func orderCompletedBinding(id string) workflow.ExecutorBinding {
	binding := workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleOrderInfo](), reflect.TypeFor[string](), func(_ *workflow.Context, msg any) (any, error) {
					order := msg.(sampleOrderInfo)
					return "Order " + order.OrderID + " completed. Tracking: " + order.TrackingNumber, nil
				})
				return pb, nil
			},
		}, nil
	})
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

type stateNotFoundError struct {
	key string
}

func (e *stateNotFoundError) Error() string {
	return "word state not found for key: " + e.key
}
