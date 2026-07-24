// Copyright (c) Microsoft. All rights reserved.

package agentworkflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
)

func newMessageExecutor(id string, options *messageworkflow.Options) *workflow.Executor {
	executor := workflow.Executor{ID: id}
	messageworkflow.Configure(&executor, options)
	executor.Extend(&workflow.Executor{
		ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			return pb.YieldsOutputType(
				reflect.TypeFor[*message.Message](),
				reflect.TypeFor[*agent.Response](),
			), nil
		},
	})
	return &executor
}

// echoExecutorBinding builds a workflow executor that, on each TurnToken,
// emits a single response update output echoing the last user message and
// yields a final aggregated message as workflow output.
func echoExecutorBinding(id string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		RawValue:         struct{}{},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		ex := newMessageExecutor(id, &messageworkflow.Options{
			StateKey: "echo_msgs",
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
				var text string
				for _, m := range messages {
					if t := m.Contents.Text(); t != "" {
						text = t
					}
				}
				update := &agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "echo:" + text},
					},
					AgentID:    id,
					AuthorName: id,
				}
				if err := ctx.AddEvent(workflow.OutputEvent{
					ExecutorID: id,
					Output:     update,
				}); err != nil {
					return err
				}
				out := &message.Message{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "echo:" + text},
					},
					AuthorName: id,
				}
				return ctx.YieldOutput(out)
			},
		})
		return ex, nil
	}
	return binding
}

func fixedTextAgent(id, name, text string) *agent.Agent {
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: name,
				MessageID:  id + "-message",
				Contents: []message.Content{
					&message.TextContent{Text: text},
				},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "fixed-text", Run: run},
		agent.Config{
			ID:                  id,
			Name:                name,
			DisableFuncAutoCall: true,
		},
	)
}

func uppercaseLatestTextBinding(id string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		RawValue:         struct{}{},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return newMessageExecutor(id, &messageworkflow.Options{
			StateKey: id + "_msgs",
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
				var latest string
				for _, current := range messages {
					if current == nil {
						continue
					}
					if text := strings.TrimSpace(current.Contents.Text()); text != "" {
						latest = text
					}
				}
				if latest == "" {
					return nil
				}
				return ctx.YieldOutput(&message.Message{
					Role:       message.RoleAssistant,
					AuthorName: id,
					Contents: []message.Content{
						&message.TextContent{Text: strings.ToUpper(latest)},
					},
				})
			},
		}), nil
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

	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		Config: agent.Config{Name: "Echo"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "ping").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatalf("expected at least one response message, got 0")
	}
	var sawEcho bool
	for _, msg := range resp.Messages {
		if msg.Contents.Text() == "echo:ping" {
			sawEcho = true
		}
	}
	if !sawEcho {
		t.Errorf("expected response text %q, got %+v", "echo:ping", resp)
	}
}

func TestNew_SerializedSessionResumesFromCheckpoint(t *testing.T) {
	wf1 := newCallRequestWorkflow(t, "persisted")
	ag1, err := agentworkflow.NewAgent(wf1, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New first agent: %v", err)
	}
	session, err := ag1.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := ag1.RunText(t.Context(), "hi", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	var requestID string
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				requestID = fc.CallID
			}
		}
	}
	if requestID != "persisted_FunctionCall:abc" {
		t.Fatalf("first run request ID = %q, want %q", requestID, "persisted_FunctionCall:abc")
	}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("Marshal session: %v", err)
	}
	if !strings.Contains(string(data), "checkpointStore") || !strings.Contains(string(data), "lastCheckpoint") {
		t.Fatalf("serialized session did not include workflow checkpoint state: %s", string(data))
	}
	if !strings.Contains(string(data), "\"pending\"") || !strings.Contains(string(data), "\"RequestID\"") || !strings.Contains(string(data), "workflowSessionID") {
		t.Fatalf("serialized session missing pending ExternalRequest/workflowSessionID: %s", string(data))
	}
	if strings.Contains(string(data), "\"requestContent\"") {
		t.Fatalf("serialized session should store pending ExternalRequest directly without request content cache: %s", string(data))
	}

	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal session: %v", err)
	}
	wf2 := newCallRequestWorkflow(t, "persisted")
	ag2, err := agentworkflow.NewAgent(wf2, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New restored agent: %v", err)
	}
	resumeMsg := []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: requestID, Result: "42"},
		},
	}}
	second, err := ag2.Run(t.Context(), resumeMsg, agent.WithSession(&restored)).Collect()
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
		t.Fatalf("final response text = %q, want %q", finalText, "got:42")
	}
}

func newApprovalRequestWorkflow(t *testing.T, id string) *workflow.Workflow {
	t.Helper()
	binding := approvalRequestExecutorBinding(t, id)
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return wf
}

// TestNew_SerializedSessionResumesApprovalRequest verifies that a pending
// ToolApprovalRequestContent that survives a session JSON round-trip resolves
// to the same concrete type as a live request. After the round-trip the
// pending request's Data is a delayed-deserialized PortableValue, whose JSON
// form also unmarshals cleanly into an empty FunctionCallContent. Before the
// TypeID disambiguation in requestDataContent, the restored request resolved to
// that empty FunctionCallContent, so the matching ToolApprovalResponseContent
// was not re-keyed to the original request ID ("req-1") and the workflow
// observed the external request ID instead. The executor asserts the delivered
// ID equals "req-1", so this fails before the fix and passes after.
func TestNew_SerializedSessionResumesApprovalRequest(t *testing.T) {
	wf1 := newApprovalRequestWorkflow(t, "approval-persisted")
	ag1, err := agentworkflow.NewAgent(wf1, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New first agent: %v", err)
	}
	session, err := ag1.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := ag1.RunText(t.Context(), "hi", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	var req *message.ToolApprovalRequestContent
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				req = r
			}
		}
	}
	if req == nil {
		t.Fatalf("expected an approval request, got %+v", first)
	}
	if req.RequestID != "approval-persisted_UserInput:req-1" {
		t.Fatalf("first run request ID = %q, want %q", req.RequestID, "approval-persisted_UserInput:req-1")
	}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("Marshal session: %v", err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal session: %v", err)
	}

	wf2 := newApprovalRequestWorkflow(t, "approval-persisted")
	ag2, err := agentworkflow.NewAgent(wf2, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New restored agent: %v", err)
	}
	resumeMsg := []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{req.CreateResponse(true, "")},
	}}
	second, err := ag2.Run(t.Context(), resumeMsg, agent.WithSession(&restored)).Collect()
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	var finalText string
	for _, m := range second.Messages {
		if txt := m.Contents.Text(); txt == "approved" || txt == "denied" {
			finalText = txt
		}
	}
	if finalText != "approved" {
		t.Fatalf("final response text = %q, want %q", finalText, "approved")
	}
}

func TestNew_StreamsUpdatesForUnhandledWorkflowEvents(t *testing.T) {
	binding := echoExecutorBinding("echo")
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var sawUnhandled bool
	for update, err := range ag.RunText(t.Context(), "ping", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("RunText: %v", err)
		}
		if update.RawRepresentation == nil {
			continue
		}
		switch update.RawRepresentation.(type) {
		case workflow.StartedEvent, workflow.SuperStepStartedEvent, workflow.ExecutorInvokedEvent:
			if len(update.Contents) == 0 {
				sawUnhandled = true
			}
		}
	}
	if !sawUnhandled {
		t.Fatalf("expected a raw-only update for an unhandled workflow event")
	}
}

func TestNew_CollectDropsRawOnlyWorkflowEventMessages(t *testing.T) {
	binding := echoExecutorBinding("echo")
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "ping").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	for index, msg := range resp.Messages {
		if len(msg.Contents) == 0 {
			t.Fatalf("message %d has no contents after collect: %+v", index, msg)
		}
	}
}

func TestNew_StampsEverySurfacedUpdate(t *testing.T) {
	binding := echoExecutorBinding("echo")
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	updates := 0
	for update, err := range ag.RunText(t.Context(), "ping") {
		if err != nil {
			t.Fatalf("RunText: %v", err)
		}
		updates++
		if update.MessageID == "" {
			t.Fatalf("update %d MessageID is empty: %+v", updates, update)
		}
		if update.ResponseID == "" {
			t.Fatalf("update %d ResponseID is empty: %+v", updates, update)
		}
		if update.CreatedAt.IsZero() {
			t.Fatalf("update %d CreatedAt is zero: %+v", updates, update)
		}
	}
	if updates == 0 {
		t.Fatalf("expected at least one update")
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

	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var updates int
	for range ag.RunText(t.Context(), "ping", agent.Stream(true)) {
		updates++
	}
	// Streaming keeps both the hosted update and the translated workflow output visible.
	if updates < 2 {
		t.Errorf("expected at least 2 updates with IncludeOutputsInResponse, got %d", updates)
	}
}

func TestNew_DoesNotIncludeGenericMessageOutputsByDefault(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID:               "message-yielder",
		ImplementationID: "*workflow.Executor",
		RawValue:         struct{}{},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return newMessageExecutor(binding.ID, &messageworkflow.Options{
			StateKey: "message_yielder_msgs",
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
				return ctx.YieldOutput(&message.Message{
					Role:     message.RoleAssistant,
					Contents: message.Contents{&message.TextContent{Text: "generic-output"}},
				})
			},
		}), nil
	}
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "ping").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if strings.Contains(resp.String(), "generic-output") {
		t.Fatalf("generic message output surfaced with IncludeOutputsInResponse=false: %+v", resp)
	}
}

func TestNew_GatesHostedAgentResponseOutputsByDefault(t *testing.T) {
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: message.Contents{&message.TextContent{Text: "hosted-response"}},
			}, nil)
		}
	}
	host := agentworkflow.New(
		agent.New(
			agent.ProviderConfig{ProviderName: "hosted-response", Run: run},
			agent.Config{ID: "hosted-id", Name: "hosted-name", DisableFuncAutoCall: true},
		),
		agentworkflow.Config{EmitResponseEvents: true},
	)
	wf, err := workflow.NewBuilder(host).
		WithOutputFrom(host).
		Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for update, err := range ag.RunText(t.Context(), "ping") {
		if err != nil {
			t.Fatalf("RunText: %v", err)
		}
		if raw, ok := update.RawRepresentation.(workflow.OutputEvent); ok {
			if _, isResponse := raw.Output.(*agent.Response); isResponse {
				t.Fatalf("aggregated hosted agent response output was forwarded by default: %+v", update)
			}
		}
	}

	included, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{IncludeOutputsInResponse: true})
	if err != nil {
		t.Fatalf("New included: %v", err)
	}
	var sawResponseOutput bool
	for update, err := range included.RunText(t.Context(), "ping") {
		if err != nil {
			t.Fatalf("RunText included: %v", err)
		}
		if raw, ok := update.RawRepresentation.(workflow.OutputEvent); ok {
			if _, isResponse := raw.Output.(*agent.Response); isResponse {
				sawResponseOutput = true
			}
		}
	}
	if !sawResponseOutput {
		t.Fatalf("aggregated hosted agent response output was not forwarded when included")
	}
}

func TestNew_CollectPrefersTerminalWorkflowOutputOverIntermediateHostedAgentUpdates(t *testing.T) {
	first := agentworkflow.New(
		fixedTextAgent("first-agent", "First Agent", "first answer"),
		agentworkflow.Config{
			DisableForwardIncomingMessages: true,
			EmitUpdateEvents:               true,
		},
	)
	second := agentworkflow.New(
		fixedTextAgent("second-agent", "Second Agent", "second answer"),
		agentworkflow.Config{
			DisableForwardIncomingMessages: true,
			EmitUpdateEvents:               true,
		},
	)
	uppercase := uppercaseLatestTextBinding("uppercase")

	wf, err := workflow.NewBuilder(first).
		AddEdge(first, second).
		AddEdge(second, uppercase).
		WithIntermediateOutputFrom(first, second).
		WithOutputFrom(uppercase).
		Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "hello").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}

	if got, want := responseTexts(resp), []string{"SECOND ANSWER"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("response texts = %v, want %v; response = %+v", got, want, resp)
	}
}

func TestNew_ConvertsResponseOutputAsMessageUpdates(t *testing.T) {
	createdAt := time.Date(2024, 11, 10, 9, 20, 0, 0, time.UTC)
	binding := workflow.ExecutorBinding{
		ID:               "response-yielder",
		ImplementationID: "*workflow.Executor",
		RawValue:         struct{}{},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return newMessageExecutor(binding.ID, &messageworkflow.Options{
			StateKey: "response_yielder_msgs",
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
				return ctx.YieldOutput(&agent.Response{
					AgentID:              "inner-agent",
					ID:                   "inner-response",
					CreatedAt:            createdAt,
					AdditionalProperties: map[string]any{"trace": "kept"},
					Messages: []*message.Message{
						{
							Role:       message.RoleAssistant,
							ID:         "inner-message",
							AuthorName: "inner-author",
							Contents: message.Contents{
								&message.TextContent{Text: "from-response"},
							},
						},
					},
				})
			},
		}), nil
	}
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		Config:                   agent.Config{ID: "workflow-agent", Name: "Workflow Agent"},
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var sawMessage bool
	for update, err := range ag.RunText(t.Context(), "ping") {
		if err != nil {
			t.Fatalf("RunText: %v", err)
		}
		if update.String() == "from-response" {
			sawMessage = true
			if update.AgentID != "workflow-agent" {
				t.Errorf("expected workflow agent ID, got %q", update.AgentID)
			}
			if update.ResponseID == "" || update.ResponseID == "inner-response" {
				t.Errorf("expected workflow response ID, got %q", update.ResponseID)
			}
			if update.MessageID != "inner-message" {
				t.Errorf("expected MessageID inner-message, got %q", update.MessageID)
			}
			if update.AuthorName != "Workflow Agent" {
				t.Errorf("expected workflow agent AuthorName, got %q", update.AuthorName)
			}
			if update.CreatedAt.IsZero() || update.CreatedAt.Equal(createdAt) {
				t.Errorf("expected workflow-created timestamp, got %v", update.CreatedAt)
			}
			if update.AdditionalProperties != nil {
				t.Errorf("expected no response-level additional properties, got %v", update.AdditionalProperties)
			}
		}
	}
	if !sawMessage {
		t.Fatalf("expected response message update")
	}
}

func TestNew_RejectsIncompatibleWorkflow(t *testing.T) {
	// A workflow whose start executor handles only string input.
	id := "string-handler"
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		RawValue:         struct{}{},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		ex := &workflow.Executor{
			ID: id,

			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil,
					func(ctx *workflow.Context, msg any) (any, error) { return nil, nil })
				return rb, nil
			},
		}
		return ex, nil
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	if _, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{}); err == nil {
		t.Fatalf("New should reject workflow that does not accept []*message.Message")
	}
}

func TestNew_NilWorkflow(t *testing.T) {
	if _, err := agentworkflow.NewAgent(nil, agentworkflow.AgentConfig{}); err == nil {
		t.Fatalf("NewAgent(nil) should return an error")
	}
}

func errorContentExecutorBinding(id string, messageText string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return newMessageExecutor(id, &messageworkflow.Options{
			StateKey: "error_content_msgs",
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
				return ctx.AddEvent(workflow.OutputEvent{
					ExecutorID: id,
					Output: &agent.ResponseUpdate{
						Role:     message.RoleAssistant,
						Contents: []message.Content{&message.ErrorContent{Message: messageText}},
					},
				})
			},
		}), nil
	}
	return binding
}

func TestNew_ErrorContentOutputStreamedOut(t *testing.T) {
	const want = "Simulated agent failure."

	for _, includeDetails := range []bool{false, true} {
		t.Run(fmt.Sprintf("includeDetails=%v", includeDetails), func(t *testing.T) {
			binding := errorContentExecutorBinding("error-content", want)
			wf, err := workflow.NewBuilder(binding).Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{IncludeErrorDetails: includeDetails})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			resp, err := ag.RunText(t.Context(), "hi").Collect()
			if err != nil {
				t.Fatalf("RunText: %v", err)
			}
			if !containsErrorContent(resp, want) {
				t.Errorf("expected ErrorContent with %q, response = %+v", want, resp)
			}
		})
	}
}

// failingExecutor returns the given error from its handler on every TurnToken.
func failingExecutor(id string, retErr error) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		ex := newMessageExecutor(id, &messageworkflow.Options{
			StateKey: "fail_msgs",
			TakeTurnHandler: func(_ *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
				return retErr
			},
		})
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
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "hi").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}

	const want = "An error occurred while executing the workflow."
	if !containsErrorContent(resp, want) {
		t.Errorf("expected ErrorContent with %q, response = %+v", want, resp)
	}
}

func TestNew_ExecutorFailedEvent_DefaultMessage(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID:               "executor-failed",
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return newMessageExecutor(binding.ID, &messageworkflow.Options{
			StateKey: "executor_failed_msgs",
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
				return ctx.AddEvent(workflow.ExecutorFailedEvent{
					ExecutorID: "secret-executor",
					Error:      fmt.Errorf("secret failure details"),
				})
			},
		}), nil
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "hi").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}

	const want = "An error occurred while executing the workflow."
	if !containsErrorContent(resp, want) {
		t.Errorf("expected ErrorContent with %q, response = %+v", want, resp)
	}
	if containsErrorContent(resp, "secret failure details") || containsErrorContent(resp, "secret-executor") {
		t.Errorf("default executor failure response exposed details: %+v", resp)
	}
}

func TestNew_ErrorEvent_IncludeDetails(t *testing.T) {
	binding := failingExecutor("fail", &simulatedAgentFailure{})
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{IncludeErrorDetails: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "hi").Collect()
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

func containsErrorContent(resp *agent.Response, msg string) bool {
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
func callRequestExecutorBinding(t *testing.T, id string) workflow.ExecutorBinding {
	t.Helper()
	port := workflow.RequestPort{
		ID:       id + "_FunctionCall",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[*message.FunctionResultContent](),
	}
	step := 0
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		Ports:            []workflow.RequestPort{port},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		executor := newMessageExecutor(id, &messageworkflow.Options{
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
		})
		executor.Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					resp := msg.(*workflow.ExternalResponse)
					v, ok := resp.Data.As(port.Response)
					if !ok {
						return nil, nil
					}
					r := v.(*message.FunctionResultContent)
					if r.CallID != "abc" {
						t.Errorf("FunctionResultContent.CallID delivered to workflow = %q, want %q", r.CallID, "abc")
					}
					out := &message.Message{
						Role:     message.RoleAssistant,
						Contents: []message.Content{&message.TextContent{Text: "got:" + r.Result.(string)}},
					}
					return nil, ctx.YieldOutput(out)
				})
				return rb, nil
			},
		})
		return executor, nil
	}
	return binding
}

func newCallRequestWorkflow(t *testing.T, id string) *workflow.Workflow {
	t.Helper()
	binding := callRequestExecutorBinding(t, id)
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return wf
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

	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// First turn.
	first, err := ag.RunText(t.Context(), "hi", agent.WithSession(session)).Collect()
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
	if sawCall.CallID != "fcall_FunctionCall:abc" {
		t.Errorf("FunctionCallContent.CallID = %q, want %q", sawCall.CallID, "fcall_FunctionCall:abc")
	}

	// Second turn: provide the matching FunctionResultContent.
	resumeMsg := []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: sawCall.CallID, Result: "42"},
		},
	}}
	second, err := ag.Run(t.Context(), resumeMsg, agent.WithSession(session)).Collect()
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

func TestNew_RequestInfoFallbackMarshalErrorIsObservable(t *testing.T) {
	const id = "bad-request"
	port := workflow.RequestPort{
		ID:       id + "_Any",
		Request:  reflect.TypeFor[any](),
		Response: reflect.TypeFor[string](),
	}
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		Ports:            []workflow.RequestPort{port},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return newMessageExecutor(id, &messageworkflow.Options{
			StateKey: "bad_request_msgs",
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
				req, err := workflow.NewExternalRequest(port.ID+":chan", port, make(chan int))
				if err != nil {
					return err
				}
				return ctx.PostRequest(req)
			},
		}), nil
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{IncludeErrorDetails: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "hi").Collect()
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}

	if !containsErrorContent(resp, "failed to surface external request") {
		t.Fatalf("expected request surfacing error in response, got %+v", resp)
	}
	if !containsErrorContent(resp, "unsupported type: chan int") {
		t.Fatalf("expected JSON marshal error details in response, got %+v", resp)
	}
}

func TestNew_ResponsePortInterfaceTypeIsValidatedPolymorphically(t *testing.T) {
	const id = "poly-response"
	port := workflow.RequestPort{
		ID:       id + "_FunctionCall",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[message.Content](),
	}
	step := 0
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		Ports:            []workflow.RequestPort{port},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		executor := newMessageExecutor(id, &messageworkflow.Options{
			StateKey: "poly_response_msgs",
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
		})
		executor.Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					resp := msg.(*workflow.ExternalResponse)
					data, ok := resp.Data.As(reflect.TypeFor[*message.FunctionResultContent]())
					if !ok {
						return nil, nil
					}
					result := data.(*message.FunctionResultContent)
					out := &message.Message{
						Role:     message.RoleAssistant,
						Contents: []message.Content{&message.TextContent{Text: "poly:" + result.Result.(string)}},
					}
					return nil, ctx.YieldOutput(out)
				})
				return rb, nil
			},
		})
		return executor, nil
	}
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{IncludeOutputsInResponse: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first, err := ag.RunText(t.Context(), "hi", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	var requestID string
	for _, msg := range first.Messages {
		for _, content := range msg.Contents {
			if call, ok := content.(*message.FunctionCallContent); ok {
				requestID = call.CallID
			}
		}
	}
	if requestID != "poly-response_FunctionCall:abc" {
		t.Fatalf("request ID = %q, want %q", requestID, "poly-response_FunctionCall:abc")
	}
	second, err := ag.Run(t.Context(), []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: requestID, Result: "42"},
		},
	}}, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	var finalText string
	for _, msg := range second.Messages {
		if text := msg.Contents.Text(); strings.HasPrefix(text, "poly:") {
			finalText = text
		}
	}
	if finalText != "poly:42" {
		t.Fatalf("final text = %q, want %q", finalText, "poly:42")
	}
}

// approvalRequestExecutorBinding builds a workflow executor that, on its
// first turn, posts a *ToolApprovalRequestContent as an ExternalRequest
// via a static port; on receipt of a *ToolApprovalResponseContent it yields
// a final text reflecting whether the call was approved.
func approvalRequestExecutorBinding(t *testing.T, id string) workflow.ExecutorBinding {
	t.Helper()
	port := workflow.RequestPort{
		ID:       id + "_UserInput",
		Request:  reflect.TypeFor[*message.ToolApprovalRequestContent](),
		Response: reflect.TypeFor[*message.ToolApprovalResponseContent](),
	}
	step := 0
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		Ports:            []workflow.RequestPort{port},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		executor := newMessageExecutor(id, &messageworkflow.Options{
			StateKey: "approval_msgs",
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
				defer func() { step++ }()
				if step == 0 {
					call := &message.FunctionCallContent{CallID: "abc", Name: "do"}
					ar := &message.ToolApprovalRequestContent{RequestID: "req-1", ToolCall: call}
					req, err := workflow.NewExternalRequest(port.ID+":req-1", port, ar)
					if err != nil {
						return err
					}
					return ctx.PostRequest(req)
				}
				return nil
			},
		})
		executor.Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					resp := msg.(*workflow.ExternalResponse)
					v, ok := resp.Data.As(port.Response)
					if !ok {
						return nil, nil
					}
					r := v.(*message.ToolApprovalResponseContent)
					if r.RequestID != "req-1" {
						t.Errorf("ToolApprovalResponseContent.ID delivered to workflow = %q, want %q", r.RequestID, "req-1")
					}
					text := "denied"
					if r.Approved {
						text = "approved"
					}
					out := &message.Message{
						Role:     message.RoleAssistant,
						Contents: []message.Content{&message.TextContent{Text: text}},
					}
					return nil, ctx.YieldOutput(out)
				})
				return rb, nil
			},
		})
		return executor, nil
	}
	return binding
}

// TestNew_RequestInfoContentUsesExternalRequestID verifies that the provider
// exposes the workflow-facing external request ID while preserving the rest
// of the request content.
func TestNew_RequestInfoContentUsesExternalRequestID(t *testing.T) {
	binding := callRequestExecutorBinding(t, "preserve")
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "hi").Collect()
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
	if got.CallID != "preserve_FunctionCall:abc" {
		t.Errorf("CallID = %q, want %q", got.CallID, "preserve_FunctionCall:abc")
	}
	if got.Name != "do" {
		t.Errorf("Name = %q, want %q", got.Name, "do")
	}
}

func TestNew_ApprovalRequestInfoContentUsesExternalRequestID(t *testing.T) {
	binding := approvalRequestExecutorBinding(t, "approval-preserve")
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := ag.RunText(t.Context(), "hi").Collect()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got *message.ToolApprovalRequestContent
	for _, m := range resp.Messages {
		for _, c := range m.Contents {
			if req, ok := c.(*message.ToolApprovalRequestContent); ok {
				got = req
			}
		}
	}
	if got == nil {
		t.Fatalf("expected ToolApprovalRequestContent in response, got %+v", resp)
	}
	if got.RequestID != "approval-preserve_UserInput:req-1" {
		t.Errorf("RequestID = %q, want %q", got.RequestID, "approval-preserve_UserInput:req-1")
	}
	call, ok := got.ToolCall.(*message.FunctionCallContent)
	if !ok {
		t.Fatalf("ToolCall = %T, want *message.FunctionCallContent", got.ToolCall)
	}
	if call.CallID != "abc" {
		t.Errorf("ToolCall.CallID = %q, want %q", call.CallID, "abc")
	}
	if call.Name != "do" {
		t.Errorf("ToolCall.Name = %q, want %q", call.Name, "do")
	}
}

// The workflow raises a ToolApprovalRequestContent, the caller resumes with the
// matching ToolApprovalResponseContent, and the workflow yields a final
// text reflecting the approval.
func TestNew_ApprovalRoundtrip_ResponseIsProcessed(t *testing.T) {
	binding := approvalRequestExecutorBinding(t, "approval")
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := ag.RunText(t.Context(), "hi", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	var req *message.ToolApprovalRequestContent
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				req = r
			}
		}
	}
	if req == nil {
		t.Fatalf("expected an approval request, got %+v", first)
	}
	if req.RequestID != "approval_UserInput:req-1" {
		t.Errorf("ID = %q, want %q", req.RequestID, "approval_UserInput:req-1")
	}

	resumeMsg := []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{req.CreateResponse(true, "")},
	}}
	second, err := ag.Run(t.Context(), resumeMsg, agent.WithSession(session)).Collect()
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

// A single resume message containing both matching response content and
// additional regular content must dispatch the response without re-emitting
// the handled external request.
func TestNew_MixedResponseAndRegularMessage_ResponseProcessed(t *testing.T) {
	id := "mixed"
	port := workflow.RequestPort{
		ID:       id + "_FunctionCall",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[*message.FunctionResultContent](),
	}
	step := 0
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		Ports:            []workflow.RequestPort{port},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		executor := newMessageExecutor(id, &messageworkflow.Options{
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
		})
		executor.Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(ctx *workflow.Context, msg any) (any, error) {
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
				})
				return rb, nil
			},
		})
		return executor, nil
	}

	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := ag.RunText(t.Context(), "kick", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	var requestID string
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				requestID = fc.CallID
			}
		}
	}
	if requestID != "mixed_FunctionCall:abc" {
		t.Fatalf("first run request ID = %q, want %q", requestID, "mixed_FunctionCall:abc")
	}

	// Resume message: function result + extra regular text in same batch.
	resumeMsg := []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: requestID, Result: "42"},
			&message.TextContent{Text: "extra"},
		},
	}}
	second, err := ag.Run(t.Context(), resumeMsg, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	var sawResult, reemittedRequest bool
	var errors []string
	for _, m := range second.Messages {
		text := m.Contents.Text()
		if strings.Contains(text, "result:42") {
			sawResult = true
		}
		for _, c := range m.Contents {
			switch content := c.(type) {
			case *message.FunctionCallContent:
				if content.CallID == requestID {
					reemittedRequest = true
				}
			case *message.ErrorContent:
				errors = append(errors, content.Message)
			}
		}
	}
	if !sawResult {
		t.Errorf("expected response handler to produce 'result:42'; got %+v", second)
	}
	if reemittedRequest {
		t.Errorf("handled external request %q was re-emitted; got %+v", requestID, second)
	}
	if len(errors) > 0 {
		t.Errorf("expected no workflow errors; got %v", errors)
	}
}

func TestNew_MixedResponseAndRegularMessage_BothProcessed(t *testing.T) {
	id := "order"
	port := workflow.RequestPort{
		ID:       id + "_FunctionCall",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[*message.FunctionResultContent](),
	}
	var postedRequest bool
	var sawRegularMessage bool
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		Ports:            []workflow.RequestPort{port},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,

			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.YieldsOutputType(reflect.TypeFor[*message.Message]())
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[[]*message.Message](), nil, func(_ *workflow.Context, msg any) (any, error) {
					if !postedRequest {
						return nil, nil
					}
					for _, m := range msg.([]*message.Message) {
						if m.Contents.Text() == "extra" {
							sawRegularMessage = true
						}
					}
					return nil, nil
				})
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[workflow.TurnToken](), nil, func(ctx *workflow.Context, _ any) (any, error) {
					if postedRequest {
						return nil, nil
					}
					postedRequest = true
					call := &message.FunctionCallContent{CallID: "abc", Name: "do"}
					req, err := workflow.NewExternalRequest(port.ID+":abc", port, call)
					if err != nil {
						return nil, err
					}
					return nil, ctx.PostRequest(req)
				})
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					resp := msg.(*workflow.ExternalResponse)
					if _, ok := resp.Data.As(port.Response); !ok {
						return nil, nil
					}
					out := &message.Message{
						Role:     message.RoleAssistant,
						Contents: []message.Content{&message.TextContent{Text: "response-processed"}},
					}
					return nil, ctx.YieldOutput(out)
				})
				return pb, nil
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{IncludeOutputsInResponse: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first, err := ag.RunText(t.Context(), "kick", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	var requestID string
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				requestID = fc.CallID
			}
		}
	}
	if requestID != "order_FunctionCall:abc" {
		t.Fatalf("first run request ID = %q, want %q", requestID, "order_FunctionCall:abc")
	}

	second, err := ag.Run(t.Context(), []*message.Message{{
		Role: message.RoleUser,
		Contents: []message.Content{
			&message.TextContent{Text: "extra"},
			&message.FunctionResultContent{CallID: requestID, Result: "42"},
		},
	}}, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	var sawResponse, reemittedRequest bool
	var errors []string
	for _, m := range second.Messages {
		if strings.Contains(m.Contents.Text(), "response-processed") {
			sawResponse = true
		}
		for _, c := range m.Contents {
			switch content := c.(type) {
			case *message.FunctionCallContent:
				if content.CallID == requestID {
					reemittedRequest = true
				}
			case *message.ErrorContent:
				errors = append(errors, content.Message)
			}
		}
	}
	if !sawRegularMessage {
		t.Fatalf("expected regular content to be delivered to workflow; response = %+v", second)
	}
	if !sawResponse {
		t.Fatalf("expected response handler output; response = %+v", second)
	}
	if reemittedRequest {
		t.Fatalf("handled external request %q was re-emitted; response = %+v", requestID, second)
	}
	if len(errors) > 0 {
		t.Fatalf("expected no workflow errors; got %v", errors)
	}
}

func TestNew_DuplicateMatchedResponseContentIsDropped(t *testing.T) {
	id := "duplicate"
	port := workflow.RequestPort{
		ID:       id + "_FunctionCall",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[*message.FunctionResultContent](),
	}
	var postedRequest bool
	var duplicateForwardedAsRegular bool
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		Ports:            []workflow.RequestPort{port},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,

			ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.YieldsOutputType(reflect.TypeFor[*message.Message]())
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[[]*message.Message](), nil, func(_ *workflow.Context, msg any) (any, error) {
					if !postedRequest {
						return nil, nil
					}
					for _, m := range msg.([]*message.Message) {
						for _, c := range m.Contents {
							if r, ok := c.(*message.FunctionResultContent); ok && r.CallID == "duplicate_FunctionCall:abc" {
								duplicateForwardedAsRegular = true
							}
						}
					}
					return nil, nil
				})
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[workflow.TurnToken](), nil, func(ctx *workflow.Context, _ any) (any, error) {
					if postedRequest {
						return nil, nil
					}
					postedRequest = true
					call := &message.FunctionCallContent{CallID: "abc", Name: "do"}
					req, err := workflow.NewExternalRequest(port.ID+":abc", port, call)
					if err != nil {
						return nil, err
					}
					return nil, ctx.PostRequest(req)
				})
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					resp := msg.(*workflow.ExternalResponse)
					if _, ok := resp.Data.As(port.Response); !ok {
						return nil, nil
					}
					text := "duplicate-dropped"
					if duplicateForwardedAsRegular {
						text = "duplicate-forwarded"
					}
					out := &message.Message{
						Role:     message.RoleAssistant,
						Contents: []message.Content{&message.TextContent{Text: text}},
					}
					return nil, ctx.YieldOutput(out)
				})
				return pb, nil
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{IncludeOutputsInResponse: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first, err := ag.RunText(t.Context(), "kick", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	var requestID string
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				requestID = fc.CallID
			}
		}
	}
	if requestID != "duplicate_FunctionCall:abc" {
		t.Fatalf("first run request ID = %q, want %q", requestID, "duplicate_FunctionCall:abc")
	}

	second, err := ag.Run(t.Context(), []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: requestID, Result: "42"},
			&message.FunctionResultContent{CallID: requestID, Result: "42"},
		},
	}}, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	var finalText string
	for _, m := range second.Messages {
		if text := m.Contents.Text(); text == "duplicate-dropped" || text == "duplicate-forwarded" {
			finalText = text
		}
	}
	if finalText != "duplicate-dropped" {
		t.Fatalf("duplicate handling output = %q, want %q; response = %+v", finalText, "duplicate-dropped", second)
	}
}

// requestEmittingAgent always emits the same FunctionCallContent, regardless
// of the messages it receives. Mirrors .NET's
// `RequestEmittingAgent(completeOnResponse: false)`.
func requestEmittingAgent(callID, name string) *agent.Agent {
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
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

func requestCompletingAgent(callID, name string) *agent.Agent {
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if containsFunctionResult(messages) {
				yield(&agent.ResponseUpdate{
					Role:     message.RoleAssistant,
					Contents: []message.Content{&message.TextContent{Text: "Request processed"}},
				}, nil)
				return
			}
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: callID, Name: name},
				},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "request-completing", Run: run},
		agent.Config{ID: "complete-id", Name: "complete-name", DisableFuncAutoCall: true},
	)
}

func containsFunctionResult(messages []*message.Message) bool {
	for _, msg := range messages {
		for _, content := range msg.Contents {
			if _, ok := content.(*message.FunctionResultContent); ok {
				return true
			}
		}
	}
	return false
}

func kickoffOnStartExecutorBinding(id, downstreamExecutorID, kickoffInputText, kickoffMessageText, regularResumeText, regularProcessedText string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		RawValue:         struct{}{},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		executor := newMessageExecutor(id, &messageworkflow.Options{
			StateKey:                 id + "_msgs",
			DisableAutoSendTurnToken: true,
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
				if containsTextContent(messages, kickoffInputText) {
					kickoff := []*message.Message{{
						Role:     message.RoleUser,
						Contents: []message.Content{&message.TextContent{Text: kickoffMessageText}},
					}}
					if err := ctx.SendMessage(downstreamExecutorID, kickoff); err != nil {
						return err
					}
					if err := ctx.SendMessage(downstreamExecutorID, turnTokenWithEvents()); err != nil {
						return err
					}
				}
				if containsTextContent(messages, regularResumeText) {
					return emitTextUpdate(ctx, id, regularProcessedText)
				}
				return nil
			},
		})
		executor.Extend(sendMessagesAndTurnTokensExecutor())
		return executor, nil
	}
	return binding
}

func turnTrackingStartExecutorBinding(id, downstreamExecutorID, activatedMarker string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		RawValue:         struct{}{},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		executor := newMessageExecutor(id, &messageworkflow.Options{
			StateKey:                 id + "_msgs",
			DisableAutoSendTurnToken: true,
			TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
				if hasUserMessage(messages) {
					if err := ctx.SendMessage(downstreamExecutorID, messages); err != nil {
						return err
					}
					if err := ctx.SendMessage(downstreamExecutorID, turnTokenWithEvents()); err != nil {
						return err
					}
				}
				return emitTextUpdate(ctx, id, activatedMarker)
			},
		})
		executor.Extend(sendMessagesAndTurnTokensExecutor())
		return executor, nil
	}
	return binding
}

func sendMessagesAndTurnTokensExecutor() *workflow.Executor {
	return &workflow.Executor{
		ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			return pb.SendsMessageType(
				reflect.TypeFor[[]*message.Message](),
				reflect.TypeFor[workflow.TurnToken](),
			), nil
		},
	}
}

func containsTextContent(messages []*message.Message, text string) bool {
	for _, msg := range messages {
		for _, content := range msg.Contents {
			if textContent, ok := content.(*message.TextContent); ok && textContent.Text == text {
				return true
			}
		}
	}
	return false
}

func hasUserMessage(messages []*message.Message) bool {
	for _, msg := range messages {
		if msg.Role == message.RoleUser {
			return true
		}
	}
	return false
}

func turnTokenWithEvents() workflow.TurnToken {
	emitEvents := true
	return workflow.TurnToken{EmitEvents: &emitEvents}
}

func emitTextUpdate(ctx *workflow.Context, executorID, text string) error {
	return ctx.AddEvent(workflow.OutputEvent{
		ExecutorID: executorID,
		Output: &agent.ResponseUpdate{
			Role:     message.RoleAssistant,
			Contents: []message.Content{&message.TextContent{Text: text}},
		},
	})
}

func addCrossExecutorEdges(builder *workflow.Builder, startBinding, downstreamBinding workflow.ExecutorBinding) *workflow.Builder {
	return builder.
		AddDirectEdge(startBinding, downstreamBinding, false, func(value any) bool {
			_, ok := value.([]*message.Message)
			return ok
		}).
		AddDirectEdge(startBinding, downstreamBinding, false, func(value any) bool {
			_, ok := value.(workflow.TurnToken)
			return ok
		})
}

func requireWorkflowFunctionCallID(t *testing.T, response *agent.Response) string {
	t.Helper()
	for _, msg := range response.Messages {
		for _, content := range msg.Contents {
			if call, ok := content.(*message.FunctionCallContent); ok && strings.Contains(call.CallID, "_FunctionCall:") {
				return call.CallID
			}
		}
	}
	t.Fatalf("expected workflow-facing FunctionCallContent in response, got %+v", response)
	return ""
}

func responseTexts(response *agent.Response) []string {
	var texts []string
	for _, msg := range response.Messages {
		for _, content := range msg.Contents {
			if textContent, ok := content.(*message.TextContent); ok {
				texts = append(texts, textContent.Text)
			}
		}
	}
	return texts
}

func responseErrorMessages(response *agent.Response) []string {
	var messages []string
	for _, msg := range response.Messages {
		for _, content := range msg.Contents {
			if errorContent, ok := content.(*message.ErrorContent); ok {
				messages = append(messages, errorContent.Message)
			}
		}
	}
	return messages
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
	host := agentworkflow.New(
		requestEmittingAgent("matching-response-call-id", "matchingResponseFunction"),
		agentworkflow.Config{EmitUpdateEvents: true},
	)
	wf, err := workflow.NewBuilder(host).WithOutputFrom(host).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := ag.RunText(t.Context(), "Start", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	var emitted *message.FunctionCallContent
	firstCount := 0
	for _, m := range first.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				if strings.Contains(fc.CallID, "_FunctionCall:") {
					firstCount++
				}
				if emitted == nil && strings.Contains(fc.CallID, "_FunctionCall:") {
					emitted = fc
				}
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
	second, err := ag.Run(t.Context(), resumeMsg, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	secondCount := 0
	for _, m := range second.Messages {
		for _, c := range m.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok && strings.Contains(fc.CallID, "_FunctionCall:") {
				secondCount++
			}
		}
	}
	// One agent turn yields the same FCC via both *agent.ResponseUpdate output and
	// RequestInfoEvent, so first-turn count is the baseline. An extra
	// TurnToken-driven turn would double the second-turn count.
	if secondCount != firstCount {
		t.Errorf("FunctionCallContent count: first=%d, second=%d (extra TurnToken-driven turn detected)", firstCount, secondCount)
	}
}

func TestNew_UnmatchedResponse_TriggersTurnAndKeepsProgressing(t *testing.T) {
	host := agentworkflow.New(
		requestEmittingAgent("unmatched-response-call-id", "unmatchedResponseFunction"),
		agentworkflow.Config{EmitUpdateEvents: true},
	)
	wf, err := workflow.NewBuilder(host).WithOutputFrom(host).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first, err := ag.RunText(t.Context(), "Start", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_ = requireWorkflowFunctionCallID(t, first)

	second, err := ag.Run(t.Context(), []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: "different-call-id", Result: "tool output"},
		},
	}}, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	functionCallCount := 0
	for _, msg := range second.Messages {
		for _, content := range msg.Contents {
			if call, ok := content.(*message.FunctionCallContent); ok && call.CallID == "unmatched-response-call-id" {
				functionCallCount++
			}
		}
	}
	if functionCallCount != 1 {
		t.Fatalf("FunctionCallContent count = %d, want 1; response = %+v", functionCallCount, second)
	}
	if errors := responseErrorMessages(second); len(errors) != 0 {
		t.Fatalf("unexpected ErrorContent messages: %v", errors)
	}
}

func TestNew_MixedResponseAndRegularMessage_CrossExecutorStartExecutorIsReawakened(t *testing.T) {
	const (
		startExecutorID    = "start-executor"
		kickoffInputText   = "Start"
		kickoffMessageText = "kickoff downstream"
		resumeRegularText  = "resume regular"
		resumeProcessed    = "regular message processed"
	)
	downstream := agentworkflow.New(
		requestCompletingAgent("cross-executor-call-id", "crossExecutorFunction"),
		agentworkflow.Config{EmitUpdateEvents: true},
	)
	start := kickoffOnStartExecutorBinding(
		startExecutorID,
		downstream.ID,
		kickoffInputText,
		kickoffMessageText,
		resumeRegularText,
		resumeProcessed,
	)
	wf, err := addCrossExecutorEdges(workflow.NewBuilder(start), start, downstream).
		WithOutputFrom(downstream).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first, err := ag.RunText(t.Context(), kickoffInputText, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	requestID := requireWorkflowFunctionCallID(t, first)

	second, err := ag.Run(t.Context(), []*message.Message{
		{
			Role: message.RoleTool,
			Contents: []message.Content{
				&message.FunctionResultContent{CallID: requestID, Result: "tool output"},
			},
		},
		{
			Role:     message.RoleUser,
			Contents: []message.Content{&message.TextContent{Text: resumeRegularText}},
		},
	}, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	texts := strings.Join(responseTexts(second), "\n")
	if !strings.Contains(texts, resumeProcessed) {
		t.Fatalf("second response text = %q, want %q", texts, resumeProcessed)
	}
	if !strings.Contains(texts, "Request processed") {
		t.Fatalf("second response text = %q, want Request processed", texts)
	}
	if errors := responseErrorMessages(second); len(errors) != 0 {
		t.Fatalf("unexpected ErrorContent messages: %v", errors)
	}
}

func TestNew_ResponseOnlyToNonStartExecutor_StartExecutorIsStillActivated(t *testing.T) {
	const (
		startExecutorID = "start-executor"
		activatedMarker = "start-executor-activated"
	)
	downstream := agentworkflow.New(
		requestCompletingAgent("response-only-call-id", "responseOnlyFunction"),
		agentworkflow.Config{EmitUpdateEvents: true},
	)
	start := turnTrackingStartExecutorBinding(startExecutorID, downstream.ID, activatedMarker)
	wf, err := addCrossExecutorEdges(workflow.NewBuilder(start), start, downstream).
		WithOutputFrom(downstream).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ag, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := ag.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first, err := ag.RunText(t.Context(), "Start", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	requestID := requireWorkflowFunctionCallID(t, first)

	second, err := ag.Run(t.Context(), []*message.Message{{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: requestID, Result: "tool output"},
		},
	}}, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	texts := strings.Join(responseTexts(second), "\n")
	if !strings.Contains(texts, "Request processed") {
		t.Fatalf("second response text = %q, want Request processed", texts)
	}
	if !strings.Contains(texts, activatedMarker) {
		t.Fatalf("second response text = %q, want %q", texts, activatedMarker)
	}
	if errors := responseErrorMessages(second); len(errors) != 0 {
		t.Fatalf("unexpected ErrorContent messages: %v", errors)
	}
}
