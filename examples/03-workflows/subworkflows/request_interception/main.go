// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Sub-Workflow Request Interception",
	"This sample lets a parent workflow answer a nested subworkflow's request locally, only escalating large amounts to the user.",
)

// autoApproveThreshold is the largest expense the parent will approve on its own
// without surfacing a top-level request to the user.
const autoApproveThreshold = 1000

// ExpenseReport is the payload a nested approval subworkflow asks about.
type ExpenseReport struct {
	ID     string
	Amount int
}

func main() {
	// A small expense is answered by the parent's interceptor: the child's
	// request never becomes a top-level RequestInfoEvent.
	runScenario(ExpenseReport{ID: "EXP-1", Amount: 250})

	// A large expense is forwarded to a qualified request port, surfacing as a
	// top-level RequestInfoEvent that a human answers via SendResponse.
	runScenario(ExpenseReport{ID: "EXP-2", Amount: 5000})
}

func runScenario(report ExpenseReport) {
	wf := buildWorkflow()

	ctx := context.Background()
	demo.Assistantf("Submitting %s for $%d (parent auto-approves <= $%d)", report.ID, report.Amount, autoApproveThreshold)

	run, err := inproc.Default.RunStreaming(ctx, wf, report)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	escalated := false
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.RequestInfoEvent:
			// Only large expenses reach the user; auto-approved ones are
			// answered inside the parent before they ever get here.
			escalated = true
			demo.Assistantf("  [User] Approval requested on port %q; approving", e.Request.PortInfo.PortID)
			response, err := e.Request.CreateResponse(true)
			if err != nil {
				demo.Panic(err)
			}
			if err := run.SendResponse(ctx, response); err != nil {
				demo.Panic(err)
			}
		case workflow.OutputEvent:
			demo.Assistantf("  Child decision for %s: approved=%v", report.ID, e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}

	if escalated {
		demo.Assistantf("Path: ESCALATED - %s produced a top-level request answered by the user\n", report.ID)
	} else {
		demo.Assistantf("Path: AUTO-APPROVED - %s answered locally by the parent, no top-level request\n", report.ID)
	}
}

// buildWorkflow constructs a fresh parent workflow around a nested approval
// subworkflow. Everything is rebuilt per run because binding a subworkflow takes
// ownership of it, so the child cannot be shared across parent builds.
func buildWorkflow() *workflow.Workflow {
	approvePort := workflow.RequestPort{
		ID:       "approve",
		Request:  reflect.TypeFor[ExpenseReport](),
		Response: reflect.TypeFor[bool](),
	}
	child := buildChild(approvePort)

	const hostID = "ApprovalSubworkflow"
	host := inproc.BindSubworkflowAsExecutor(child, hostID)

	// A nested request port is surfaced to the parent under a qualified ID of the
	// form "<subworkflowID>.<portID>", so the escalation port must match it.
	escalationPort := workflow.RequestPort{
		ID:       hostID + "." + approvePort.ID,
		Request:  approvePort.Request,
		Response: approvePort.Response,
	}
	escalation := escalationPort.Bind()
	interceptor := approvalInterceptor("PolicyGate", hostID, escalationPort.ID)

	// Wiring:
	//   host -> interceptor        (child's requests reach the parent's policy gate)
	//   interceptor -> host        (locally-created responses go straight back)
	//   interceptor -> escalation  (large requests are forwarded to a top-level port)
	//   escalation -> host         (the user's answer flows back to the child)
	parent, err := workflow.NewBuilder(host).
		AddDirectEdge(host, interceptor, false, externalRequestOnly).
		AddDirectEdge(interceptor, host, false, externalResponseOnly).
		AddDirectEdge(interceptor, escalation, false, externalRequestOnly).
		AddDirectEdge(escalation, host, false, externalResponseOnly).
		WithOutputFrom(host).
		Build()
	if err != nil {
		demo.Panic(err)
	}
	return parent
}

func buildChild(port workflow.RequestPort) *workflow.Workflow {
	submitter := expenseSubmitter("SubmitExpense", port)
	child, err := workflow.NewBuilder(submitter).
		WithOutputFrom(submitter).
		Build()
	if err != nil {
		demo.Panic(err)
	}
	return child
}

// expenseSubmitter is the child's start executor. It raises an approval request
// via ctx.PostRequest and yields the boolean decision once the response arrives.
func expenseSubmitter(id string, port workflow.RequestPort) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.YieldsOutputType(reflect.TypeFor[bool]())
				pb.RouteBuilder.
					AddHandlerRaw(reflect.TypeFor[ExpenseReport](), nil, func(ctx *workflow.Context, msg any) (any, error) {
						req, err := workflow.NewExternalRequest("", port, msg.(ExpenseReport))
						if err != nil {
							return nil, err
						}
						return nil, ctx.PostRequest(req)
					}).
					AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(ctx *workflow.Context, msg any) (any, error) {
						data := msg.(*workflow.ExternalResponse).Data
						approved, ok := workflow.PortableValueAs[bool](data)
						if !ok {
							return nil, fmt.Errorf("expenseSubmitter: expected bool approval response, got %v", data)
						}
						return nil, ctx.YieldOutput(approved)
					})
				return pb, nil
			},
		}, nil
	})
}

// approvalInterceptor sits between the subworkflow host and the outside world.
// Small expenses are answered in-process; large ones are forwarded to a
// top-level request port so the user can decide.
func approvalInterceptor(id, hostID, escalationPortID string) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			DisableAutoSendMessageHandlerResultObject: true,
			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.SendsMessageType(
					reflect.TypeFor[*workflow.ExternalResponse](),
					reflect.TypeFor[*workflow.ExternalRequest](),
				)
				// A catch-all keeps this edge type-compatible with the host, which
				// declares that it sends the child's output type. The
				// externalRequestOnly edge condition guarantees only forwarded
				// requests actually reach this handler at runtime.
				pb.RouteBuilder.AddCatchAll(func(ctx *workflow.Context, msg workflow.PortableValue) (any, error) {
					req, ok := workflow.PortableValueAs[*workflow.ExternalRequest](msg)
					if !ok {
						return nil, nil
					}
					report, ok := workflow.PortableValueAs[ExpenseReport](req.Data)
					if !ok {
						return nil, fmt.Errorf("interceptor: unexpected request payload %v", req.PortInfo.RequestType)
					}
					if report.Amount <= autoApproveThreshold {
						demo.Assistantf("  [PolicyGate] $%d <= $%d: approving %s locally", report.Amount, autoApproveThreshold, report.ID)
						response, err := req.CreateResponse(true)
						if err != nil {
							return nil, err
						}
						return nil, ctx.SendMessage(hostID, response)
					}
					demo.Assistantf("  [PolicyGate] $%d > $%d: escalating %s to the user", report.Amount, autoApproveThreshold, report.ID)
					return nil, ctx.SendMessage(escalationPortID, req)
				})
				return pb, nil
			},
		}, nil
	})
}

func externalRequestOnly(msg any) bool {
	_, ok := msg.(*workflow.ExternalRequest)
	return ok
}

func externalResponseOnly(msg any) bool {
	_, ok := msg.(*workflow.ExternalResponse)
	return ok
}
