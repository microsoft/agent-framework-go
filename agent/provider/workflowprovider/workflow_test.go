// Copyright (c) Microsoft. All rights reserved.

package workflowprovider_test

import (
	"context"
	"iter"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/agent/provider/workflowprovider"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

// echoExecutorBinding builds a workflow executor that, on each TurnToken,
// emits a single ResponseUpdate echoing the last user message and yields a
// final aggregated message as workflow output.
func echoExecutorBinding(id string) *workflow.ExecutorBinding {
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
		Raw:          struct{}{},
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		ex := &workflow.Executor{
			ID: id,
			Config: []*workflow.ExecutorConfig{
				messageworkflow.NewExecutorConfig(&messageworkflow.Options{
					StateKey: "echo_msgs",
					TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
						var text string
						for _, m := range messages {
							if t := m.Contents.Text(); t != "" {
								text = t
							}
						}
						update := &message.ResponseUpdate{
							Role: message.RoleAssistant,
							Contents: []message.Content{
								&message.TextContent{Text: "echo:" + text},
							},
							AuthorID:   id,
							AuthorName: id,
						}
						if err := ctx.AddEvent(workflow.ResponseUpdateEvent{
							ExecutorID: id,
							Update:     update,
						}); err != nil {
							return err
						}
						out := &message.Message{
							Role: message.RoleAssistant,
							Contents: []message.Content{
								&message.TextContent{Text: "echo:" + text},
							},
							AuthorID:   id,
							AuthorName: id,
						}
						return ctx.YieldOutput(out)
					},
				}),
			},
		}
		return ex, nil
	}
	return binding
}

func TestNew_StreamsResponseUpdates(t *testing.T) {
	binding := echoExecutorBinding("echo")
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	ag, err := workflowprovider.New(wf, workflowprovider.Config{
		Config: agent.Config{Name: "Echo"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(context.Background(), "ping").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatalf("expected at least one response message, got 0")
	}
	if got, want := resp.Messages[0].Contents.Text(), "echo:ping"; got != want {
		t.Errorf("response text = %q, want %q", got, want)
	}
}

func TestNew_IncludeOutputsInResponse(t *testing.T) {
	binding := echoExecutorBinding("echo")
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	ag, err := workflowprovider.New(wf, workflowprovider.Config{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var updates int
	for range ag.RunText(context.Background(), "ping") {
		updates++
	}
	// One update from ResponseUpdateEvent, one from OutputEvent translation.
	if updates < 2 {
		t.Errorf("expected at least 2 updates with IncludeOutputsInResponse, got %d", updates)
	}
}

func TestNew_RejectsIncompatibleWorkflow(t *testing.T) {
	// A workflow whose start executor handles only string input.
	id := "string-handler"
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
		Raw:          struct{}{},
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		ex := &workflow.Executor{
			ID: id,
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[string](), nil, false,
						func(ctx *workflow.Context, msg any) (any, error) { return nil, nil }), nil
				},
			}},
		}
		return ex, nil
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	if _, err := workflowprovider.New(wf, workflowprovider.Config{}); err == nil {
		t.Fatalf("New should reject workflow that does not accept []*message.Message")
	}
}

func TestNew_NilWorkflow(t *testing.T) {
	if _, err := workflowprovider.New(nil, workflowprovider.Config{}); err == nil {
		t.Fatalf("New(nil) should return an error")
	}
}

// failingExecutor returns the given error from its handler on every TurnToken.
func failingExecutor(id string, retErr error) *workflow.ExecutorBinding {
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		ex := &workflow.Executor{
			ID: id,
			Config: []*workflow.ExecutorConfig{
				messageworkflow.NewExecutorConfig(&messageworkflow.Options{
					StateKey: "fail_msgs",
					TakeTurnHandler: func(_ *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
						return retErr
					},
				}),
			},
		}
		return ex, nil
	}
	return binding
}

func TestNew_ErrorEvent_DefaultMessage(t *testing.T) {
	binding := failingExecutor("fail", &simulatedAgentFailure{})
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := workflowprovider.New(wf, workflowprovider.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(context.Background(), "hi").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}

	const want = "an error occurred while executing the workflow"
	if !containsErrorContent(resp, want) {
		t.Errorf("expected ErrorContent with %q, response = %+v", want, resp)
	}
}

func TestNew_ErrorEvent_IncludeDetails(t *testing.T) {
	binding := failingExecutor("fail", &simulatedAgentFailure{})
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := workflowprovider.New(wf, workflowprovider.Config{IncludeErrorDetails: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(context.Background(), "hi").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}

	const want = "Simulated agent failure."
	if !containsErrorContent(resp, want) {
		t.Errorf("expected ErrorContent with %q, response = %+v", want, resp)
	}
}

type simulatedAgentFailure struct{}

func (*simulatedAgentFailure) Error() string { return "Simulated agent failure." }

func containsErrorContent(resp *message.Response, msg string) bool {
	for _, m := range resp.Messages {
		for _, c := range m.Contents {
			if ec, ok := c.(*message.ErrorContent); ok && strings.Contains(ec.Message, msg) {
				return true
			}
		}
	}
	return false
}

// callRequestExecutorBinding builds a workflow executor that, on its first
// turn, posts a *FunctionCallContent as an ExternalRequest via a static port,
// then on receipt of a *FunctionResultContent (delivered via the matching
// ExternalResponse) yields a final text reflecting the result.
func callRequestExecutorBinding(t *testing.T, id string) *workflow.ExecutorBinding {
	t.Helper()
	port := workflow.RequestPort{
		ID:       id + "_FunctionCall",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[*message.FunctionResultContent](),
	}
	step := 0
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
		Ports:        []workflow.RequestPort{port},
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Config: []*workflow.ExecutorConfig{
				messageworkflow.NewExecutorConfig(&messageworkflow.Options{
					StateKey: "call_msgs",
					TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
						defer func() { step++ }()
						if step == 0 {
							call := &message.FunctionCallContent{CallID: "abc", Name: "do"}
							req, err := workflow.NewExternalRequest(port.ID+":abc", port, call)
							if err != nil {
								return err
							}
							return ctx.PostRequest(req)
						}
						return nil
					},
				}),
				{
					ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
						return rb.AddHandler(reflect.TypeFor[*workflow.ExternalResponse](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
							resp := msg.(*workflow.ExternalResponse)
							v, ok := resp.Data.As(port.Response)
							if !ok {
								return nil, nil
							}
							r := v.(*message.FunctionResultContent)
							out := &message.Message{
								Role:     message.RoleAssistant,
								Contents: []message.Content{&message.TextContent{Text: "got:" + r.Result.(string)}},
							}
							return nil, ctx.YieldOutput(out)
						}), nil
					},
				},
			},
		}, nil
	}
	return binding
}

// TestNew_SurfacesRequestInfoAndAcceptsResponse verifies the multi-turn
// external-request round trip:
//
//  1. The agent's first run yields a ResponseUpdate carrying the workflow's
//     FunctionCallContent (raised via PostRequest).
//  2. A second agent run with a matching FunctionResultContent in its input
//     resumes the workflow; its output is surfaced as a final ResponseUpdate.
func TestNew_SurfacesRequestInfoAndAcceptsResponse(t *testing.T) {
	binding := callRequestExecutorBinding(t, "fcall")
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ag, err := workflowprovider.New(wf, workflowprovider.Config{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	session, err := ag.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// First turn.
	first, err := ag.RunText(ctx, "hi", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	var sawCall *message.FunctionCallContent
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				sawCall = fc
			}
		}
	}
	if sawCall == nil {
		t.Fatalf("first run: expected a FunctionCallContent, got %+v", first)
	}
	if sawCall.CallID != "abc" {
		t.Errorf("FunctionCallContent.CallID = %q, want %q", sawCall.CallID, "abc")
	}

	// Second turn: provide the matching FunctionResultContent.
	resumeMsg := []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: "abc", Result: "42"},
		},
	}}
	second, err := ag.Run(ctx, resumeMsg, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	var finalText string
	for _, m := range second.Messages {
		if t := m.Contents.Text(); strings.HasPrefix(t, "got:") {
			finalText = t
		}
	}
	if finalText != "got:42" {
		t.Errorf("final response text = %q, want %q", finalText, "got:42")
	}
}

// approvalRequestExecutorBinding builds a workflow executor that, on its
// first turn, posts a *FunctionApprovalRequestContent as an ExternalRequest
// via a static port; on receipt of a *FunctionApprovalResponseContent it
// yields a final text reflecting whether the call was approved.
func approvalRequestExecutorBinding(t *testing.T, id string) *workflow.ExecutorBinding {
	t.Helper()
	port := workflow.RequestPort{
		ID:       id + "_UserInput",
		Request:  reflect.TypeFor[*message.FunctionApprovalRequestContent](),
		Response: reflect.TypeFor[*message.FunctionApprovalResponseContent](),
	}
	step := 0
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
		Ports:        []workflow.RequestPort{port},
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Config: []*workflow.ExecutorConfig{
				messageworkflow.NewExecutorConfig(&messageworkflow.Options{
					StateKey: "approval_msgs",
					TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
						defer func() { step++ }()
						if step == 0 {
							call := &message.FunctionCallContent{CallID: "abc", Name: "do"}
							ar := &message.FunctionApprovalRequestContent{ID: "req-1", FunctionCall: call}
							req, err := workflow.NewExternalRequest(port.ID+":req-1", port, ar)
							if err != nil {
								return err
							}
							return ctx.PostRequest(req)
						}
						return nil
					},
				}),
				{
					ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
						return rb.AddHandler(reflect.TypeFor[*workflow.ExternalResponse](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
							resp := msg.(*workflow.ExternalResponse)
							v, ok := resp.Data.As(port.Response)
							if !ok {
								return nil, nil
							}
							r := v.(*message.FunctionApprovalResponseContent)
							text := "denied"
							if r.Approved {
								text = "approved"
							}
							out := &message.Message{
								Role:     message.RoleAssistant,
								Contents: []message.Content{&message.TextContent{Text: text}},
							}
							return nil, ctx.YieldOutput(out)
						}), nil
					},
				},
			},
		}, nil
	}
	return binding
}

// TestNew_PreservesRequestInfoContent verifies that a workflow's emitted
// FunctionCallContent reaches the agent caller with its CallID and Name
// preserved in the surfaced ResponseUpdate.
func TestNew_PreservesRequestInfoContent(t *testing.T) {
	binding := callRequestExecutorBinding(t, "preserve")
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := workflowprovider.New(wf, workflowprovider.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(context.Background(), "hi").Collect()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got *message.FunctionCallContent
	for _, m := range resp.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				got = fc
			}
		}
	}
	if got == nil {
		t.Fatalf("expected FunctionCallContent in response, got %+v", resp)
	}
	if got.CallID != "abc" {
		t.Errorf("CallID = %q, want %q", got.CallID, "abc")
	}
	if got.Name != "do" {
		t.Errorf("Name = %q, want %q", got.Name, "do")
	}
}

// The workflow raises a FunctionApprovalRequestContent, the caller resumes with the
// matching FunctionApprovalResponseContent, and the workflow yields a final
// text reflecting the approval.
func TestNew_ApprovalRoundtrip_ResponseIsProcessed(t *testing.T) {
	binding := approvalRequestExecutorBinding(t, "approval")
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := workflowprovider.New(wf, workflowprovider.Config{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	session, err := ag.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := ag.RunText(ctx, "hi", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	var req *message.FunctionApprovalRequestContent
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if r, ok := c.(*message.FunctionApprovalRequestContent); ok {
				req = r
			}
		}
	}
	if req == nil {
		t.Fatalf("expected an approval request, got %+v", first)
	}
	if req.ID != "req-1" {
		t.Errorf("ID = %q, want %q", req.ID, "req-1")
	}

	resumeMsg := []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{req.Response(true)},
	}}
	second, err := ag.Run(ctx, resumeMsg, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	var finalText string
	for _, m := range second.Messages {
		if t := m.Contents.Text(); t == "approved" || t == "denied" {
			finalText = t
		}
	}
	if finalText != "approved" {
		t.Errorf("final text = %q, want %q", finalText, "approved")
	}
}

// A single resume message containing both the matching response content and
// additional regular content must dispatch the response and forward the
// regular content into the workflow.
func TestNew_MixedResponseAndRegularMessage_BothProcessed(t *testing.T) {
	id := "mixed"
	port := workflow.RequestPort{
		ID:       id + "_FunctionCall",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[*message.FunctionResultContent](),
	}
	step := 0
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
		Ports:        []workflow.RequestPort{port},
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Config: []*workflow.ExecutorConfig{
				messageworkflow.NewExecutorConfig(&messageworkflow.Options{
					StateKey: "mixed_msgs",
					TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, msgs []*message.Message) error {
						defer func() { step++ }()
						if step == 0 {
							call := &message.FunctionCallContent{CallID: "abc", Name: "do"}
							req, err := workflow.NewExternalRequest(port.ID+":abc", port, call)
							if err != nil {
								return err
							}
							return ctx.PostRequest(req)
						}
						// On the resume turn, summarize all observed text
						// from the regular workflow input and yield it.
						var collected []string
						for _, m := range msgs {
							if t := m.Contents.Text(); t != "" {
								collected = append(collected, t)
							}
						}
						summary := "regular:" + strings.Join(collected, ",")
						out := &message.Message{
							Role:     message.RoleAssistant,
							Contents: []message.Content{&message.TextContent{Text: summary}},
						}
						return ctx.YieldOutput(out)
					},
				}),
				{
					ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
						return rb.AddHandler(reflect.TypeFor[*workflow.ExternalResponse](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
							resp := msg.(*workflow.ExternalResponse)
							v, ok := resp.Data.As(port.Response)
							if !ok {
								return nil, nil
							}
							r := v.(*message.FunctionResultContent)
							out := &message.Message{
								Role:     message.RoleAssistant,
								Contents: []message.Content{&message.TextContent{Text: "result:" + r.Result.(string)}},
							}
							return nil, ctx.YieldOutput(out)
						}), nil
					},
				},
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := workflowprovider.New(wf, workflowprovider.Config{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	session, err := ag.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := ag.RunText(ctx, "kick", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("first: %v", err)
	}

	// Resume message: function result + extra regular text in same batch.
	resumeMsg := []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: "abc", Result: "42"},
			&message.TextContent{Text: "extra"},
		},
	}}
	second, err := ag.Run(ctx, resumeMsg, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	var sawResult, sawSummary bool
	for _, m := range second.Messages {
		text := m.Contents.Text()
		if strings.Contains(text, "result:42") {
			sawResult = true
		}
		if strings.Contains(text, "regular:") && strings.Contains(text, "extra") {
			sawSummary = true
		}
	}
	if !sawResult {
		t.Errorf("expected response handler to produce 'result:42'; got %+v", second)
	}
	if !sawSummary {
		t.Errorf("expected regular content to be forwarded into the workflow; got %+v", second)
	}
}

// requestEmittingAgent always emits the same FunctionCallContent, regardless
// of the messages it receives. Mirrors .NET's
// `RequestEmittingAgent(completeOnResponse: false)`.
func requestEmittingAgent(callID, name string) *agent.Agent {
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: callID, Name: name},
				},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "request-emitting", Run: run},
		agent.Config{ID: "rep-id", Name: "rep-name", DisableFuncAutoCall: true},
	)
}

// TestNew_MatchingResponse_DoesNotCauseExtraTurn mirrors .NET's
// Test_AsAgent_MatchingResponse_DoesNotCauseExtraTurnAsync: a workflow whose
// start executor is a hosted agent that emits a request on every turn must
// produce exactly one new request when resumed with a matching
// ExternalResponse. Sending an extra TurnToken alongside the response would
// cause the hosted agent to be invoked twice (once driven by the response
// handler's auto-emitted TurnToken, once by the explicit one), producing
// two FunctionCallContents in the second-run output.
func TestNew_MatchingResponse_DoesNotCauseExtraTurn(t *testing.T) {
	host := workflowhosting.New(
		requestEmittingAgent("matching-response-call-id", "matchingResponseFunction"),
		workflowhosting.Config{EmitUpdateEvents: true},
	)
	wf, err := workflow.NewBuilder(host).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := workflowprovider.New(wf, workflowprovider.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	session, err := ag.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := ag.RunText(ctx, "Start", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	var emitted *message.FunctionCallContent
	firstCount := 0
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				if emitted == nil {
					emitted = fc
				}
				firstCount++
			}
		}
	}
	if emitted == nil {
		t.Fatalf("first run: expected a FunctionCallContent in response, got %+v", first)
	}

	resumeMsg := []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: emitted.CallID, Result: "tool output"},
		},
	}}
	second, err := ag.Run(ctx, resumeMsg, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	secondCount := 0
	for _, m := range second.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok && fc.CallID == emitted.CallID {
				secondCount++
			}
		}
	}
	// One agent turn yields the same FCC via both ResponseUpdateEvent and
	// RequestInfoEvent, so first-turn count is the baseline. An extra
	// TurnToken-driven turn would double the second-turn count.
	if secondCount != firstCount {
		t.Errorf("FunctionCallContent count: first=%d, second=%d (extra TurnToken-driven turn detected)", firstCount, secondCount)
	}
}
