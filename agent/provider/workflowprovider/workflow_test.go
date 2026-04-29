// Copyright (c) Microsoft. All rights reserved.

package workflowprovider_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
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
