// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func twoPortCorrelationRunner(t *testing.T) (*runner, workflow.RequestPort, workflow.RequestPort) {
	t.Helper()
	portA := workflow.RequestPort{ID: "portA", Request: reflect.TypeFor[string](), Response: reflect.TypeFor[int]()}
	portB := workflow.RequestPort{ID: "portB", Request: reflect.TypeFor[string](), Response: reflect.TypeFor[int]()}
	sink := workflow.NewExecutor("sink", func(value int) int { return value }).Bind()
	wf, err := workflow.NewBuilder(portA.Bind()).AddEdge(portA.Bind(), sink).AddEdge(portB.Bind(), sink).
		BuildWithOptions(workflow.BuildOptions{DisableOrphanValidation: true})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := createTopLevelRunner(wf, nil, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runner.RequestEndRun(t.Context()); err != nil {
			t.Error(err)
		}
	})
	return runner, portA, portB
}

func postCorrelationRequest(t *testing.T, runner *runner, port workflow.RequestPort) *workflow.ExternalRequest {
	t.Helper()
	pending, err := workflow.NewExternalRequest("", port, "data")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.runContext.post(t.Context(), port.ID, pending); err != nil {
		t.Fatal(err)
	}
	return pending
}

func TestExternalResponseCorrelation_RejectsForgedPortID(t *testing.T) {
	runner, portA, portB := twoPortCorrelationRunner(t)
	pending := postCorrelationRequest(t, runner, portA)
	forged := &workflow.ExternalResponse{PortInfo: workflow.NewRequestPortInfo(portB), RequestID: pending.RequestID, Data: workflow.AnyPortableValue(42)}
	if err := runner.runContext.addExternalResponse(forged); err != nil {
		t.Fatal(err)
	}
	_, err := runner.runContext.advance(t.Context())
	if err == nil || !strings.Contains(err.Error(), `"portB"`) ||
		!strings.Contains(err.Error(), pending.RequestID) || strings.Contains(err.Error(), `"portA"`) {
		t.Fatalf("forged response error = %v", err)
	}
	if !runner.HasUnservicedRequests() {
		t.Fatal("forged response consumed the pending request")
	}
}

func TestExternalResponseCorrelation_LegitimateResponseAfterForgery(t *testing.T) {
	runner, portA, portB := twoPortCorrelationRunner(t)
	pending := postCorrelationRequest(t, runner, portA)
	forged := &workflow.ExternalResponse{PortInfo: workflow.NewRequestPortInfo(portB), RequestID: pending.RequestID, Data: workflow.AnyPortableValue(42)}
	if err := runner.runContext.addExternalResponse(forged); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.runContext.advance(t.Context()); err == nil {
		t.Fatal("expected forged response rejection")
	}
	legitimate, err := pending.CreateResponse(42)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.runContext.addExternalResponse(legitimate); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.runContext.advance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runner.HasUnservicedRequests() {
		t.Fatal("legitimate response did not consume the pending request")
	}
}

func TestExternalResponseCorrelation_AllowsMatchingPortID(t *testing.T) {
	runner, portA, _ := twoPortCorrelationRunner(t)
	pending := postCorrelationRequest(t, runner, portA)
	legitimate, err := pending.CreateResponse(42)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.runContext.addExternalResponse(legitimate); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.runContext.advance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runner.HasUnservicedRequests() {
		t.Fatal("matching response did not consume the pending request")
	}
}

func TestExternalResponseCorrelation_RejectsUnknownRequestID(t *testing.T) {
	runner, portA, _ := twoPortCorrelationRunner(t)
	stray := &workflow.ExternalResponse{PortInfo: workflow.NewRequestPortInfo(portA), RequestID: "no-such-request", Data: workflow.AnyPortableValue(42)}
	if err := runner.runContext.addExternalResponse(stray); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.runContext.advance(t.Context()); err == nil || !strings.Contains(err.Error(), "no pending request with ID no-such-request") {
		t.Fatalf("unknown request error = %v", err)
	}
}
