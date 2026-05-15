// Copyright (c) Microsoft. All rights reserved.

package workflowhosting_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

const (
	testAgentID   = "TestAgentId"
	testAgentName = "TestAgentName"
)

var testReplayMessages = []string{
	"",
	"Hello world!",
	"Lorem ipsum dolor sit amet, consectetur adipiscing elit.",
	"Quisque dignissim ante odio, at facilisis orci porta a. Duis mi augue, fringilla eu egestas a, pellentesque sed lacus.",
}

func sendStreamMessage(t *testing.T, stream *inproc.StreamingRun, ctx context.Context, message any) {
	t.Helper()
	if err := stream.SendMessage(ctx, message); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
}

// expectedReplayUpdateContents returns the [TextContent] payloads the replay
// agent will emit, in order. Empty messages contribute zero updates so they
// can be skipped by callers that count updates.
func expectedReplayUpdateContents() []*message.TextContent {
	var out []*message.TextContent
	for _, text := range testReplayMessages {
		if text == "" {
			continue
		}
		for _, w := range splitWordsKeepSpaces(text) {
			out = append(out, &message.TextContent{Text: w})
		}
	}
	return out
}

func splitWordsKeepSpaces(s string) []string {
	if s == "" {
		return nil
	}
	var words []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			words = append(words, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		words = append(words, s[start:])
	}
	return words
}

// newReplayAgent emits one *ResponseUpdate per word of each non-empty source
// message, sharing a stable MessageID across all updates from the same source
// message.
func newReplayAgent() *agent.Agent {
	type segment struct {
		messageID string
		content   *message.TextContent
	}
	var segments []segment
	for i, text := range testReplayMessages {
		if text == "" {
			continue
		}
		msgID := fmt.Sprintf("msg-%d", i)
		for _, w := range splitWordsKeepSpaces(text) {
			segments = append(segments, segment{
				messageID: msgID,
				content:   &message.TextContent{Text: w},
			})
		}
	}
	run := func(ctx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, s := range segments {
				if !yield(&agent.ResponseUpdate{
					Role:      message.RoleAssistant,
					MessageID: s.messageID,
					Contents:  []message.Content{s.content},
				}, nil) {
					return
				}
			}
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "replay", Run: run},
		agent.Config{
			ID:                  testAgentID,
			Name:                testAgentName,
			DisableFuncAutoCall: true,
		},
	)
}

// newRoleCheckAgent emits a single "Ok" update and returns an error if it
// observes a RoleAssistant message whose AuthorName is non-empty and not its
// own .
func newRoleCheckAgent() *agent.Agent {
	selfName := testAgentName
	run := func(ctx context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, m := range msgs {
				if m == nil || m.Role != message.RoleAssistant {
					continue
				}
				if m.AuthorName != "" && m.AuthorName != selfName {
					yield(nil, fmt.Errorf("message from other assistant role detected: AuthorName=%s", m.AuthorName))
					return
				}
			}
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    testAgentID,
				AuthorName: selfName,
				Contents:   []message.Content{&message.TextContent{Text: "Ok"}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "rolecheck", Run: run},
		agent.Config{
			ID:                  testAgentID,
			Name:                testAgentName,
			DisableFuncAutoCall: true,
		},
	)
}

func newContentAgent(updates ...*agent.ResponseUpdate) *agent.Agent {
	run := func(ctx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, update := range updates {
				if !yield(update, nil) {
					return
				}
			}
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "content", Run: run},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)
}

func newNamedNoopAgent(id string, name string) *agent.Agent {
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "noop", Run: run},
		agent.Config{ID: id, Name: name, DisableFuncAutoCall: true},
	)
}

func newRecordingAgent(calls *[][]*message.Message) *agent.Agent {
	run := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			snapshot := make([]*message.Message, len(msgs))
			copy(snapshot, msgs)
			*calls = append(*calls, snapshot)
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "ok"}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "recording", Run: run},
		agent.Config{
			ID:                  testAgentID,
			Name:                testAgentName,
			DisableFuncAutoCall: true,
			HistoryProvider:     &agent.HistoryProvider{SourceID: "noop"},
		},
	)
}

func TestHostedAgent_BindingIDUsesAgentNameWhenProvided(t *testing.T) {
	agentA := workflowhosting.New(newNamedNoopAgent("agent-a-id", "AgentA"), workflowhosting.Config{})
	agentB := workflowhosting.New(newNamedNoopAgent("agent-b-id", "AgentB"), workflowhosting.Config{})
	wf, err := workflow.NewBuilder(agentA).AddEdge(agentA, agentB).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	assertHostedAgentBindingID(t, wf, agentA, "AgentA_agent_a_id")
	assertHostedAgentBindingID(t, wf, agentB, "AgentB_agent_b_id")
}

func TestHostedAgent_BindingIDUsesAgentIDWhenNameMissing(t *testing.T) {
	agentA := workflowhosting.New(newNamedNoopAgent("agent-a-id", ""), workflowhosting.Config{})
	agentB := workflowhosting.New(newNamedNoopAgent("agent-b-id", ""), workflowhosting.Config{})
	wf, err := workflow.NewBuilder(agentA).AddEdge(agentA, agentB).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	assertHostedAgentBindingID(t, wf, agentA, "agent_a_id")
	assertHostedAgentBindingID(t, wf, agentB, "agent_b_id")
}

func TestHostedAgent_BindingIDSanitizesNameAndID(t *testing.T) {
	binding := workflowhosting.New(newNamedNoopAgent("agent id", "Agent A!"), workflowhosting.Config{})
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assertHostedAgentBindingID(t, wf, binding, "Agent_A_agent_id")
}

func assertHostedAgentBindingID(t *testing.T, wf *workflow.Workflow, binding workflow.ExecutorBinding, want string) {
	t.Helper()
	if binding.ID != want {
		t.Fatalf("binding ID = %q, want %q", binding.ID, want)
	}
	executors := wf.ReflectExecutors()
	if _, ok := executors[want]; !ok {
		t.Fatalf("workflow executors missing %q; got %v", want, executors)
	}
	executor, err := binding.CreateInstance("")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if executor.ID != want {
		t.Fatalf("executor instance ID = %q, want %q", executor.ID, want)
	}
}

func collectForwardedResponseMessages(t *testing.T, a *agent.Agent, cfg workflowhosting.Config) []*message.Message {
	t.Helper()
	var observed []*message.Message
	sinkID := "sink"
	sink := workflow.ExecutorBinding{
		ID:           sinkID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	sink.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: sinkID,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandlerRaw(reflect.TypeFor[[]*message.Message](), nil, func(_ *workflow.Context, msg any) (any, error) {
						observed = append(observed, msg.([]*message.Message)...)
						return nil, nil
					}), nil
				},
			},
		}, nil
	}

	hostCfg := cfg
	hostCfg.DisableMessageForwarding = true
	binding := workflowhosting.New(a, hostCfg)
	wf, err := workflow.NewBuilder(binding).
		AddEdge(binding, sink).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "go"}},
	}})
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
	}
	return observed
}

// runHostedAgent builds a single-node workflow hosting the agent under cfg,
// drives it with the given input messages and a TurnToken, and returns the
// collected workflow events. Lockstep execution keeps these host-behavior
// assertions independent of off-thread scheduling.
func runHostedAgent(t *testing.T, a *agent.Agent, cfg workflowhosting.Config, token workflow.TurnToken, msgs []*message.Message) []workflow.Event {
	t.Helper()
	binding := workflowhosting.New(a, cfg)
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	if len(msgs) > 0 {
		sendStreamMessage(t, stream, ctx, msgs)
	}
	sendStreamMessage(t, stream, ctx, token)

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch stream: %v", err)
		}
		events = append(events, evt)
	}
	return events
}

// boolPtr is a tiny helper for *bool literals.
func boolPtr(b bool) *bool { return &b }

// TestHostedAgent_EmitsStreamingUpdatesIfConfigured exercises the matrix
// (executorSetting × turnSetting) for streaming response update output emission. The rule:
// the TurnToken's EmitEvents flag overrides the executor's EmitUpdateEvents
// when set; otherwise the executor setting applies (default false).
func TestHostedAgent_EmitsStreamingUpdatesIfConfigured(t *testing.T) {
	cases := []struct {
		executorSetting *bool
		turnSetting     *bool
	}{
		{nil, nil},
		{nil, boolPtr(true)},
		{nil, boolPtr(false)},
		{boolPtr(true), nil},
		{boolPtr(true), boolPtr(true)},
		{boolPtr(true), boolPtr(false)},
		{boolPtr(false), nil},
		{boolPtr(false), boolPtr(true)},
		{boolPtr(false), boolPtr(false)},
	}

	expectedContents := expectedReplayUpdateContents()

	for _, tc := range cases {
		name := fmt.Sprintf("executor=%v/turn=%v", boolPtrStr(tc.executorSetting), boolPtrStr(tc.turnSetting))
		t.Run(name, func(t *testing.T) {
			cfg := workflowhosting.Config{}
			if tc.executorSetting != nil {
				cfg.EmitUpdateEvents = *tc.executorSetting
			}
			token := workflow.TurnToken{EmitEvents: tc.turnSetting}

			events := runHostedAgent(t, newReplayAgent(), cfg, token, []*message.Message{
				{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
			})

			updates := collectOutputPayloads[*agent.ResponseUpdate](events)

			expecting := false
			switch {
			case tc.turnSetting != nil:
				expecting = *tc.turnSetting
			case tc.executorSetting != nil:
				expecting = *tc.executorSetting
			}

			if expecting {
				if len(updates) != len(expectedContents) {
					t.Fatalf("expected %d updates, got %d", len(expectedContents), len(updates))
				}
				for i, u := range updates {
					if u.ExecutorID == "" {
						t.Errorf("update[%d] missing ExecutorID", i)
					}
					if u.Payload.AuthorName != testAgentName {
						t.Errorf("update[%d] AuthorName = %q, want %q", i, u.Payload.AuthorName, testAgentName)
					}
					if len(u.Payload.Contents) != 1 {
						t.Errorf("update[%d] expected 1 content, got %d", i, len(u.Payload.Contents))
						continue
					}
					gotText := u.Payload.Contents[0].(*message.TextContent).Text
					if gotText != expectedContents[i].Text {
						t.Errorf("update[%d] text = %q, want %q", i, gotText, expectedContents[i].Text)
					}
				}
			} else if len(updates) != 0 {
				t.Errorf("expected no updates, got %d", len(updates))
			}
		})
	}
}

func boolPtrStr(b *bool) string {
	if b == nil {
		return "nil"
	}
	if *b {
		return "true"
	}
	return "false"
}

// TestHostedAgent_EmitsResponseIfConfigured verifies that the EmitResponseEvents
// flag controls whether a single aggregated response output is produced.
func TestHostedAgent_EmitsResponseIfConfigured(t *testing.T) {
	for _, executorSetting := range []bool{true, false} {
		t.Run(fmt.Sprintf("emit=%v", executorSetting), func(t *testing.T) {
			events := runHostedAgent(t, newReplayAgent(),
				workflowhosting.Config{EmitResponseEvents: executorSetting},
				workflow.TurnToken{},
				[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}}})

			responses := collectOutputPayloads[*agent.Response](events)

			if executorSetting {
				if len(responses) != 1 {
					t.Fatalf("expected 1 response output, got %d", len(responses))
				}
				resp := responses[0]
				if resp.ExecutorID == "" {
					t.Error("response output missing ExecutorID")
				}
				if resp.Payload == nil {
					t.Fatal("response output payload is nil")
				}
				// One *Message per non-empty source message: 3 in the
				// reference data (the first is empty and produces no updates,
				// hence no message).
				if len(resp.Payload.Messages) != len(testReplayMessages)-1 {
					t.Errorf("Response.Messages count = %d, want %d",
						len(resp.Payload.Messages), len(testReplayMessages)-1)
				}
				for _, msg := range resp.Payload.Messages {
					if msg.AuthorName != testAgentName {
						t.Errorf("message AuthorName = %q, want %q", msg.AuthorName, testAgentName)
					}
				}
			} else if len(responses) != 0 {
				t.Errorf("expected no response outputs, got %d", len(responses))
			}
		})
	}
}

// TestHostedAgent_ReassignsRolesIfConfigured verifies that the agent receives
// only RoleUser / self-authored RoleAssistant messages by default, and that
// disabling reassignment causes a RoleCheckAgent to surface an error event
// when foreign assistant messages are present.
func TestHostedAgent_ReassignsRolesIfConfigured(t *testing.T) {
	userMsg := &message.Message{
		Role: message.RoleUser, AuthorName: "User",
		Contents: []message.Content{&message.TextContent{Text: "hello"}},
	}
	selfMsg := &message.Message{
		Role: message.RoleAssistant, AuthorName: testAgentName,
		Contents: []message.Content{&message.TextContent{Text: fmt.Sprintf("Hello from %s!", testAgentName)}},
	}
	otherMsg := &message.Message{
		Role: message.RoleAssistant, AuthorName: "OtherAgent",
		Contents: []message.Content{&message.TextContent{Text: "Hello from Assistant!"}},
	}

	cases := []struct {
		reassign        bool
		includeUser     bool
		includeSelf     bool
		includeOther    bool
		wantErrorReport bool
	}{}
	for _, reassign := range []bool{true, false} {
		for _, includeUser := range []bool{true, false} {
			for _, includeSelf := range []bool{true, false} {
				for _, includeOther := range []bool{true, false} {
					cases = append(cases, struct {
						reassign        bool
						includeUser     bool
						includeSelf     bool
						includeOther    bool
						wantErrorReport bool
					}{
						reassign, includeUser, includeSelf, includeOther,
						includeOther && !reassign,
					})
				}
			}
		}
	}

	for _, tc := range cases {
		name := fmt.Sprintf("reassign=%v/u=%v/s=%v/o=%v", tc.reassign, tc.includeUser, tc.includeSelf, tc.includeOther)
		t.Run(name, func(t *testing.T) {
			cfg := workflowhosting.Config{
				DisableRoleReassignment: !tc.reassign,
			}
			var msgs []*message.Message
			if tc.includeUser {
				msgs = append(msgs, userMsg)
			}
			if tc.includeSelf {
				msgs = append(msgs, selfMsg)
			}
			if tc.includeOther {
				msgs = append(msgs, otherMsg)
			}

			events := runHostedAgent(t, newRoleCheckAgent(), cfg, workflow.TurnToken{}, msgs)

			var sawError bool
			for _, e := range events {
				if errEvt, ok := e.(workflow.ErrorEvent); ok {
					if errEvt.Error == nil {
						continue
					}
					sawError = true
					if !tc.wantErrorReport && !errors.Is(errEvt.Error, errEvt.Error) {
						t.Errorf("unexpected error: %v", errEvt.Error)
					}
				}
			}
			if tc.wantErrorReport && !sawError {
				t.Errorf("expected an ErrorEvent, got none")
			}
			if !tc.wantErrorReport && sawError {
				t.Errorf("expected no ErrorEvent")
			}
		})
	}
}

func TestHostedAgent_RoleReassignmentSkipsNonConversationalAssistantMessages(t *testing.T) {
	var calls [][]*message.Message
	textMsg := &message.Message{
		Role:       message.RoleAssistant,
		AuthorName: "OtherAgent",
		Contents:   []message.Content{&message.TextContent{Text: "hello"}},
	}
	callMsg := &message.Message{
		Role:       message.RoleAssistant,
		AuthorName: "OtherAgent",
		Contents:   []message.Content{&message.FunctionCallContent{CallID: "call-1", Name: "do"}},
	}

	runHostedAgent(t, newRecordingAgent(&calls), workflowhosting.Config{}, workflow.TurnToken{}, []*message.Message{textMsg, callMsg})

	if len(calls) != 1 {
		t.Fatalf("agent invocation count = %d, want 1", len(calls))
	}
	if len(calls[0]) != 2 {
		t.Fatalf("received message count = %d, want 2", len(calls[0]))
	}
	if calls[0][0].Role != message.RoleUser {
		t.Fatalf("text assistant message role = %s, want %s", calls[0][0].Role, message.RoleUser)
	}
	if calls[0][1].Role != message.RoleAssistant {
		t.Fatalf("function-call assistant message role = %s, want %s", calls[0][1].Role, message.RoleAssistant)
	}
}

// TestHostedAgent_ForwardsIncomingMessages verifies that incoming messages
// are forwarded downstream by default and not when the option is disabled.
func TestHostedAgent_ForwardsIncomingMessages(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprintf("disable=%v", disable), func(t *testing.T) {
			// Sink executor simply records what it receives.
			var observed []any
			sinkID := "sink"
			sink := workflow.ExecutorBinding{
				ID:           sinkID,
				ExecutorType: reflect.TypeFor[*workflow.Executor](),
			}
			sink.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
				return &workflow.Executor{
					ID: sinkID,
					Spec: workflow.ExecutorSpec{
						DisableAutoSendMessageHandlerResultObject: true,
						DisableAutoYieldOutputHandlerResultObject: true,
						ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
							return rb.AddHandlerRaw(reflect.TypeFor[[]*message.Message](), nil, func(_ *workflow.Context, msg any) (any, error) {
								observed = append(observed, msg)
								return nil, nil
							}), nil
						},
					},
				}, nil
			}

			binding := workflowhosting.New(newReplayAgent(), workflowhosting.Config{DisableMessageForwarding: disable})
			wf, err := workflow.NewBuilder(binding).
				AddEdge(binding, sink).
				Build()
			if err != nil {
				t.Fatalf("build: %v", err)
			}

			ctx := context.Background()
			stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			defer func() { _ = stream.CancelRun() }()

			input := []*message.Message{{
				Role:       message.RoleUser,
				AuthorName: "User",
				Contents:   []message.Content{&message.TextContent{Text: "ping"}},
			}}
			sendStreamMessage(t, stream, ctx, input)
			sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
			for range stream.WatchStream(ctx) {
			}

			// First call: incoming forwarded message (only when forwarding is enabled).
			// Then: agent's own response messages.
			if disable {
				if len(observed) == 0 {
					t.Fatalf("expected at least one forwarded message (the agent reply), got 0")
				}
				// First (and only) batch must NOT be the original user message.
				first := observed[0].([]*message.Message)
				if len(first) > 0 && first[0].AuthorName == "User" {
					t.Errorf("forwarding disabled but received user-authored message")
				}
			} else {
				if len(observed) < 2 {
					t.Fatalf("expected forwarded user message + agent reply (2 batches), got %d", len(observed))
				}
				first := observed[0].([]*message.Message)
				if len(first) != 1 || first[0].AuthorName != "User" {
					t.Errorf("expected forwarded user message first, got %+v", first)
				}
			}
		})
	}
}

func TestHostedAgent_HandlesSingleMessage(t *testing.T) {
	var calls [][]*message.Message
	input := &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "single message"}},
	}

	binding := workflowhosting.New(newRecordingAgent(&calls), workflowhosting.Config{})
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, input)
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
	}

	if len(calls) != 1 {
		t.Fatalf("agent invocation count = %d, want 1", len(calls))
	}
	if len(calls[0]) != 1 {
		t.Fatalf("received message count = %d, want 1", len(calls[0]))
	}
	if calls[0][0] != input {
		t.Fatalf("agent received %p, want original message %p", calls[0][0], input)
	}
}

func TestHostedAgent_QueuesDotNetStateKeys(t *testing.T) {
	var calls [][]*message.Message
	binding := workflowhosting.New(newRecordingAgent(&calls), workflowhosting.Config{})
	executor, err := binding.CreateInstance("")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	queued := make(map[string]any)
	ctx := &workflow.Context{
		Context: context.Background(),
		AddEvent: func(workflow.Event) error {
			return nil
		},
		SendMessage: func(string, any) error {
			return nil
		},
		QueueStateUpdate: func(key string, _ string, value any) error {
			queued[key] = value
			return nil
		},
	}

	_, err = executor.Execute(ctx, &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "buffered"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := executor.OnCheckpoint(ctx); err != nil {
		t.Fatalf("OnCheckpoint: %v", err)
	}

	for _, key := range []string{
		"AIAgentHostExecutor.State",
		"AIAgentHostState",
		"_userInputHandler_PendingRequests",
		"_functionCallHandler_PendingRequests",
	} {
		if _, ok := queued[key]; !ok {
			t.Fatalf("missing queued state key %q; got %v", key, queued)
		}
	}
	if _, ok := queued["agent_buffered"]; ok {
		t.Fatalf("queued old buffered state key agent_buffered; got %v", queued)
	}
}

func TestHostedAgent_HandlesStringMessageAsUser(t *testing.T) {
	var calls [][]*message.Message
	binding := workflowhosting.New(newRecordingAgent(&calls), workflowhosting.Config{})
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, "hello")
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
	}

	if len(calls) != 1 {
		t.Fatalf("agent invocation count = %d, want 1", len(calls))
	}
	if len(calls[0]) != 1 {
		t.Fatalf("received message count = %d, want 1", len(calls[0]))
	}
	got := calls[0][0]
	if got.Role != message.RoleUser {
		t.Fatalf("message role = %s, want %s", got.Role, message.RoleUser)
	}
	if got.Contents.Text() != "hello" {
		t.Fatalf("message text = %q, want hello", got.Contents.Text())
	}
}

func TestHostedAgent_AccumulatesAndClearsMessagesPerTurn(t *testing.T) {
	var calls [][]*message.Message
	binding := workflowhosting.New(newRecordingAgent(&calls), workflowhosting.Config{})
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	first := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "first"}}}
	second := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "second"}}}
	third := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "third"}}}
	fourth := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "fourth"}}}

	sendStreamMessage(t, stream, ctx, first)
	sendStreamMessage(t, stream, ctx, []*message.Message{second, third})
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch first turn: %v", err)
		}
	}

	sendStreamMessage(t, stream, ctx, []*message.Message{fourth})
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch second turn: %v", err)
		}
	}

	if len(calls) != 2 {
		t.Fatalf("agent invocation count = %d, want 2", len(calls))
	}
	if len(calls[0]) != 3 {
		t.Fatalf("first turn message count = %d, want 3", len(calls[0]))
	}
	if calls[0][0] != first || calls[0][1] != second || calls[0][2] != third {
		t.Fatalf("first turn messages = %#v, want first/second/third", calls[0])
	}
	if len(calls[1]) != 1 {
		t.Fatalf("second turn message count = %d, want 1", len(calls[1]))
	}
	if calls[1][0] != fourth {
		t.Fatalf("second turn message = %p, want fourth %p", calls[1][0], fourth)
	}
}

func TestHostedAgent_FiltersNonPortableContentFromForwardedResponseMessages(t *testing.T) {
	agent := newContentAgent(
		&agent.ResponseUpdate{
			Role:              message.RoleAssistant,
			MessageID:         "text-message",
			RawRepresentation: "provider-text",
			Contents: message.Contents{
				&message.TextContent{Text: "Useful response text"},
			},
		},
		&agent.ResponseUpdate{
			Role:              message.RoleAssistant,
			MessageID:         "reasoning-message",
			RawRepresentation: "provider-reasoning",
			Contents: message.Contents{
				&message.TextReasoningContent{Text: "internal reasoning"},
			},
		},
		&agent.ResponseUpdate{
			Role:              message.RoleAssistant,
			MessageID:         "usage-message",
			RawRepresentation: "provider-usage",
			Contents: message.Contents{
				&message.UsageContent{Details: message.UsageDetails{TotalTokenCount: 10}},
			},
		},
	)

	forwarded := collectForwardedResponseMessages(t, agent, workflowhosting.Config{})
	if len(forwarded) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(forwarded))
	}
	if forwarded[0].ID != "text-message" {
		t.Fatalf("expected text-message to be forwarded, got %q", forwarded[0].ID)
	}
	if len(forwarded[0].Contents) != 1 {
		t.Fatalf("expected 1 forwarded content item, got %d", len(forwarded[0].Contents))
	}
	text, ok := forwarded[0].Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", forwarded[0].Contents[0])
	}
	if text.Text != "Useful response text" {
		t.Fatalf("unexpected forwarded text: %q", text.Text)
	}
}

func TestHostedAgent_StripsRawRepresentationFromForwardedResponseMessages(t *testing.T) {
	text := &message.TextContent{
		ContentHeader: message.ContentHeader{RawRepresentation: "content-raw"},
		Text:          "Response",
	}
	agent := newContentAgent(&agent.ResponseUpdate{
		Role:              message.RoleAssistant,
		MessageID:         "raw-message",
		RawRepresentation: "message-raw",
		Contents:          message.Contents{text},
	})

	forwarded := collectForwardedResponseMessages(t, agent, workflowhosting.Config{})
	if len(forwarded) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(forwarded))
	}
	if forwarded[0].RawRepresentation != nil {
		t.Fatalf("expected forwarded message RawRepresentation to be nil, got %#v", forwarded[0].RawRepresentation)
	}
	if forwarded[0].AuthorName != testAgentName {
		t.Fatalf("expected AuthorName %q, got %q", testAgentName, forwarded[0].AuthorName)
	}
	forwardedText := forwarded[0].Contents[0].(*message.TextContent)
	if forwardedText.RawRepresentation != "content-raw" {
		t.Fatalf("expected content RawRepresentation to be preserved, got %#v", forwardedText.RawRepresentation)
	}
}

func TestHostedAgent_PreservesProviderAuthorNameOnForwardedResponseMessages(t *testing.T) {
	agent := newContentAgent(&agent.ResponseUpdate{
		Role:       message.RoleAssistant,
		AuthorName: "provider-author",
		MessageID:  "provider-author-message",
		Contents:   message.Contents{&message.TextContent{Text: "Response"}},
	})

	forwarded := collectForwardedResponseMessages(t, agent, workflowhosting.Config{})
	if len(forwarded) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(forwarded))
	}
	if forwarded[0].AuthorName != "provider-author" {
		t.Fatalf("AuthorName = %q, want provider-author", forwarded[0].AuthorName)
	}
}

func TestHostedAgent_PreservesForwardableContentInMixedForwardedResponseMessages(t *testing.T) {
	agent := newContentAgent(&agent.ResponseUpdate{
		Role:              message.RoleAssistant,
		MessageID:         "mixed-message",
		RawRepresentation: "mixed-raw",
		Contents: message.Contents{
			&message.TextContent{Text: "Visible text"},
			&message.TextReasoningContent{Text: "Hidden reasoning"},
			&message.FunctionCallContent{CallID: "call-1", Name: "my_function", Arguments: `{"arg":"val"}`},
		},
	})

	forwarded := collectForwardedResponseMessages(t, agent, workflowhosting.Config{})
	if len(forwarded) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d", len(forwarded))
	}
	if len(forwarded[0].Contents) != 2 {
		t.Fatalf("expected 2 forwarded content items, got %d", len(forwarded[0].Contents))
	}
	if _, ok := forwarded[0].Contents[0].(*message.TextContent); !ok {
		t.Fatalf("expected first content to be TextContent, got %T", forwarded[0].Contents[0])
	}
	if _, ok := forwarded[0].Contents[1].(*message.FunctionCallContent); !ok {
		t.Fatalf("expected second content to be FunctionCallContent, got %T", forwarded[0].Contents[1])
	}
	if forwarded[0].RawRepresentation != nil {
		t.Fatalf("expected forwarded message RawRepresentation to be nil, got %#v", forwarded[0].RawRepresentation)
	}
}

func TestHostedAgent_DropsResponseMessagesWithOnlyNonPortableContent(t *testing.T) {
	agent := newContentAgent(
		&agent.ResponseUpdate{
			Role:      message.RoleAssistant,
			MessageID: "reasoning-message",
			Contents: message.Contents{
				&message.TextReasoningContent{Text: "reasoning only"},
			},
		},
		&agent.ResponseUpdate{
			Role:      message.RoleAssistant,
			MessageID: "vector-store-message",
			Contents: message.Contents{
				&message.HostedVectorStoreContent{VectorStoreID: "vs-1"},
			},
		},
	)

	forwarded := collectForwardedResponseMessages(t, agent, workflowhosting.Config{})
	if len(forwarded) != 0 {
		t.Fatalf("expected no forwarded messages, got %d", len(forwarded))
	}
}

func newApprovalAgent() *agent.Agent {
	const callID = "call-1"
	step := 0
	run := func(ctx context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			defer func() { step++ }()
			if step == 0 {
				yield(&agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{&message.ToolApprovalRequestContent{
						RequestID: callID,
						ToolCall: &message.FunctionCallContent{
							CallID: callID,
							Name:   "do",
						},
					}},
				}, nil)
				return
			}
			var saw *message.ToolApprovalResponseContent
			for _, m := range msgs {
				for _, c := range m.Contents {
					if r, ok := c.(*message.ToolApprovalResponseContent); ok {
						saw = r
					}
				}
			}
			text := "no-approval"
			if saw != nil {
				if saw.Approved {
					text = "approved"
				} else {
					text = "denied"
				}
			}
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: text}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "approval", Run: run},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)
}

func newFunctionCallAgent() *agent.Agent {
	const callID = "call-1"
	step := 0
	run := func(ctx context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			defer func() { step++ }()
			if step == 0 {
				yield(&agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{&message.FunctionCallContent{
						CallID: callID,
						Name:   "do",
					}},
				}, nil)
				return
			}
			var saw *message.FunctionResultContent
			for _, m := range msgs {
				for _, c := range m.Contents {
					if r, ok := c.(*message.FunctionResultContent); ok {
						saw = r
					}
				}
			}
			text := "no-result"
			if saw != nil {
				if rs, ok := saw.Result.(string); ok {
					text = "got:" + rs
				}
			}
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: text}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "fcall", Run: run},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)
}

// approverExecutor is a downstream executor that forwards a fixed
// FunctionApprovalResponseContent back to the binding it observes a
// FunctionApprovalRequestContent for.
func approverExecutor(target workflow.ExecutorBinding, approve bool) workflow.ExecutorBinding {
	id := "approver"
	binding := workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				SendTypes: []reflect.Type{reflect.TypeFor[*message.ToolApprovalResponseContent]()},
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandlerRaw(reflect.TypeFor[*message.ToolApprovalRequestContent](), nil, func(ctx *workflow.Context, msg any) (any, error) {
						req := msg.(*message.ToolApprovalRequestContent)
						return nil, ctx.SendMessage(target.ID, req.CreateResponse(approve, ""))
					}), nil
				},
			},
		}, nil
	}
	return binding
}

// resultExecutor responds to a FunctionCallContent with a FunctionResultContent.
func resultExecutor(target workflow.ExecutorBinding, result any) workflow.ExecutorBinding {
	id := "executor"
	binding := workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				SendTypes: []reflect.Type{reflect.TypeFor[*message.FunctionResultContent]()},
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandlerRaw(reflect.TypeFor[*message.FunctionCallContent](), nil, func(ctx *workflow.Context, msg any) (any, error) {
						call := msg.(*message.FunctionCallContent)
						return nil, ctx.SendMessage(target.ID, &message.FunctionResultContent{
							CallID: call.CallID,
							Result: result,
						})
					}), nil
				},
			},
		}, nil
	}
	return binding
}

type outputPayload[T any] struct {
	ExecutorID string
	Payload    T
}

func collectOutputPayloads[T any](ev []workflow.Event) []outputPayload[T] {
	var out []outputPayload[T]
	for _, e := range ev {
		o, ok := e.(workflow.OutputEvent)
		if !ok {
			continue
		}
		payload, ok := o.Output.(T)
		if ok {
			out = append(out, outputPayload[T]{ExecutorID: o.ExecutorID, Payload: payload})
		}
	}
	return out
}

func collectResponseOutputs(t *testing.T, ev []workflow.Event) []*agent.Response {
	t.Helper()
	var out []*agent.Response
	for _, response := range collectOutputPayloads[*agent.Response](ev) {
		out = append(out, response.Payload)
	}
	return out
}

// TestHostedAgent_InterceptUserInputRequests verifies that when the agent
// emits a FunctionApprovalRequestContent and the flag is set, the request is
// dispatched as a workflow message and the response, when routed back to the
// host, drives a second agent invocation that observes the response.
func TestHostedAgent_InterceptUserInputRequests(t *testing.T) {
	host := workflowhosting.New(newApprovalAgent(), workflowhosting.Config{
		InterceptUserInputRequests: true,
		EmitResponseEvents:         true,
	})
	app := approverExecutor(host, true)

	wf, err := workflow.NewBuilder(host).
		AddEdge(host, app).
		AddEdge(app, host).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, []*message.Message{{
		Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}},
	}})
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		events = append(events, evt)
	}

	responses := collectResponseOutputs(t, events)
	if len(responses) < 2 {
		t.Fatalf("expected >=2 response outputs (initial + post-approval), got %d", len(responses))
	}
	last := responses[len(responses)-1]
	if len(last.Messages) == 0 || last.Messages[0].Contents.Text() != "approved" {
		t.Errorf("final agent response = %v, want 'approved'", last.Messages[0].Contents)
	}
}

// TestHostedAgent_InterceptUnterminatedFunctionCalls verifies that when the
// agent emits a FunctionCallContent without a matching result and the flag is
// set, the call is dispatched as a workflow message and the result, when
// routed back, drives a second agent invocation that observes the result.
func TestHostedAgent_InterceptUnterminatedFunctionCalls(t *testing.T) {
	host := workflowhosting.New(newFunctionCallAgent(), workflowhosting.Config{
		InterceptUnterminatedFunctionCalls: true,
		EmitResponseEvents:                 true,
	})
	exec := resultExecutor(host, "42")

	wf, err := workflow.NewBuilder(host).
		AddEdge(host, exec).
		AddEdge(exec, host).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, []*message.Message{{
		Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}},
	}})
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		events = append(events, evt)
	}

	responses := collectResponseOutputs(t, events)
	if len(responses) < 2 {
		t.Fatalf("expected >=2 response outputs, got %d", len(responses))
	}
	last := responses[len(responses)-1]
	if last.Messages[0].Contents.Text() != "got:42" {
		t.Errorf("final agent response = %v, want 'got:42'", last.Messages[0].Contents)
	}
}

func TestHostedAgent_FunctionResultMessageMetadataMatchesHostedAgent(t *testing.T) {
	const agentID = "agent-without-name"
	var calls [][]*message.Message
	step := 0
	run := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			snapshot := make([]*message.Message, len(msgs))
			copy(snapshot, msgs)
			calls = append(calls, snapshot)
			defer func() { step++ }()
			if step == 0 {
				yield(&agent.ResponseUpdate{
					Role:     message.RoleAssistant,
					Contents: []message.Content{&message.FunctionCallContent{CallID: "call-1", Name: "do"}},
				}, nil)
				return
			}
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "done"}},
			}, nil)
		}
	}
	a := agent.New(
		agent.ProviderConfig{ProviderName: "metadata", Run: run},
		agent.Config{ID: agentID, DisableFuncAutoCall: true, HistoryProvider: &agent.HistoryProvider{SourceID: "noop"}},
	)
	host := workflowhosting.New(a, workflowhosting.Config{InterceptUnterminatedFunctionCalls: true})
	exec := resultExecutor(host, "42")
	wf, err := workflow.NewBuilder(host).
		AddEdge(host, exec).
		AddEdge(exec, host).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
	}

	if len(calls) < 2 {
		t.Fatalf("agent invocation count = %d, want at least 2", len(calls))
	}
	var resultMsg *message.Message
	for _, msg := range calls[1] {
		for _, content := range msg.Contents {
			if _, ok := content.(*message.FunctionResultContent); ok {
				resultMsg = msg
			}
		}
	}
	if resultMsg == nil {
		t.Fatal("second invocation did not include a function result message")
	}
	if resultMsg.Role != message.RoleTool {
		t.Fatalf("result message role = %s, want %s", resultMsg.Role, message.RoleTool)
	}
	if resultMsg.AuthorName != agentID {
		t.Fatalf("result message AuthorName = %q, want %q", resultMsg.AuthorName, agentID)
	}
	if resultMsg.ID == "" {
		t.Fatal("result message ID is empty")
	}
	if resultMsg.CreatedAt.IsZero() {
		t.Fatal("result message CreatedAt is zero")
	}
}

// TestHostedAgent_InterceptDisabled_PostsExternalRequest verifies that when
// the Intercept flags are not set (the default), the agent's request content
// is raised as a workflow ExternalRequest via PostRequest rather than being
// sent as a workflow message of the request content type.
func TestHostedAgent_InterceptDisabled_PostsExternalRequest(t *testing.T) {
	host := workflowhosting.New(newApprovalAgent(), workflowhosting.Config{})
	var sawApprovalRequestMessage bool
	probeID := "probe"
	probe := workflow.ExecutorBinding{
		ID:           probeID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	probe.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: probeID,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandlerRaw(reflect.TypeFor[*message.ToolApprovalRequestContent](), nil, func(_ *workflow.Context, _ any) (any, error) {
						sawApprovalRequestMessage = true
						return nil, nil
					}), nil
				},
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(host).AddEdge(host, probe).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, workflow.TurnToken{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawExternalRequest bool
	var externalRequestID string
	for evt := range run.OutgoingEvents() {
		if r, ok := evt.(workflow.RequestInfoEvent); ok {
			if r.Request != nil && r.Request.PortInfo.PortID == host.ID+"_UserInput" {
				sawExternalRequest = true
				externalRequestID = r.Request.RequestID
			}
		}
	}

	if sawApprovalRequestMessage {
		t.Errorf("expected no FunctionApprovalRequestContent workflow-message dispatch when InterceptUserInputRequests is false")
	}
	if !sawExternalRequest {
		t.Errorf("expected a RequestInfoEvent for the user-input port when InterceptUserInputRequests is false")
	}
	portID := host.ID + "_UserInput"
	wantRequestID := fmt.Sprintf("%d:%s:%s", len(portID), portID, "call-1")
	if externalRequestID != wantRequestID {
		t.Errorf("external request ID = %q, want %q", externalRequestID, wantRequestID)
	}

	status, err := run.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != inproc.RunStatusPendingRequests {
		t.Errorf("status = %v, want PendingRequests", status)
	}
}

func TestHostedAgent_BindingSupportsConcurrentSharedExecution(t *testing.T) {
	host := workflowhosting.New(newApprovalAgent(), workflowhosting.Config{})
	if !host.SupportsConcurrentSharedExecution {
		t.Fatal("hosted agent binding is not concurrent-shareable; want true to match AIAgentBinding")
	}
}

func TestHostedAgent_ResetSignal_StartsNewSession(t *testing.T) {
	var sessions []agent.Session
	createSessions := 0
	run := func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			session, ok := agent.GetOption(options, agent.WithSession)
			if !ok {
				yield(nil, errors.New("missing session"))
				return
			}
			sessions = append(sessions, session)
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "ok"}},
			}, nil)
		}
	}
	a := agent.New(
		agent.ProviderConfig{
			ProviderName: "reset-session",
			Run:          run,
			CreateSession: func(_ context.Context, _ agent.Session, _ ...agent.Option) error {
				createSessions++
				return nil
			},
		},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)
	host := workflowhosting.New(a, workflowhosting.Config{})
	wf, err := workflow.NewBuilder(host).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, "first")
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch first turn: %v", err)
		}
	}
	sendStreamMessage(t, stream, ctx, workflowhosting.ResetSignal{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch reset: %v", err)
		}
	}
	sendStreamMessage(t, stream, ctx, "second")
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for _, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch second turn: %v", err)
		}
	}

	if createSessions != 2 {
		t.Fatalf("CreateSession calls = %d, want 2", createSessions)
	}
	if len(sessions) != 2 {
		t.Fatalf("agent session count = %d, want 2", len(sessions))
	}
	if sessions[0] == sessions[1] {
		t.Fatalf("expected ResetSignal to create a new session")
	}
}

// TestHostedAgent_InterceptDisabled_ResumesWithExternalResponse drives the
// default port-based path end to end: the agent emits an approval request,
// the workflow halts at PendingRequests, the caller resumes the run with a
// matching ExternalResponse, and the agent is re-invoked observing the
// approval result.
func TestHostedAgent_InterceptDisabled_ResumesWithExternalResponse(t *testing.T) {
	host := workflowhosting.New(newApprovalAgent(), workflowhosting.Config{
		EmitResponseEvents: true,
	})

	wf, err := workflow.NewBuilder(host).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, workflow.TurnToken{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var req *workflow.ExternalRequest
	for evt := range run.OutgoingEvents() {
		if r, ok := evt.(workflow.RequestInfoEvent); ok {
			req = r.Request
			break
		}
	}
	if req == nil {
		t.Fatalf("expected a RequestInfoEvent")
	}

	// Build the approval response from the request data.
	reqContent, ok := req.Data.As(reflect.TypeFor[*message.ToolApprovalRequestContent]())
	if !ok {
		t.Fatalf("expected request data to be *FunctionApprovalRequestContent, got %T", req.Data.Any())
	}
	approval := reqContent.(*message.ToolApprovalRequestContent).CreateResponse(true, "")
	resp, err := req.NewResponse(approval)
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	var responses []*agent.Response
	for evt := range run.OutgoingEvents() {
		if o, ok := evt.(workflow.OutputEvent); ok {
			if response, ok := o.Output.(*agent.Response); ok {
				responses = append(responses, response)
			}
		}
	}
	if len(responses) == 0 {
		t.Fatalf("expected at least one response output after resume, got 0")
	}
	last := responses[len(responses)-1]
	if last.Messages[0].Contents.Text() != "approved" {
		t.Errorf("final agent response = %v, want 'approved'", last.Messages[0].Contents)
	}
}

// On the first turn it produces totalCount FunctionCallContent items, of which
// pairedCount have a matching FunctionResultContent in the same response (so
// they are already "paired" and should not be intercepted). On subsequent
// turns it counts how many of its previously-unpaired request IDs have been
// resolved by the messages it sees and emits either "Remaining: N" or "Done".
type requestAgentSession struct {
	unpaired    map[string]struct{}
	hasSentReqs bool
}

func newRequestAgent(unpaired, paired int) *agent.Agent {
	state := &requestAgentSession{unpaired: make(map[string]struct{})}
	run := func(ctx context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !state.hasSentReqs {
				total := unpaired + paired
				if !yield(&agent.ResponseUpdate{
					Role:     message.RoleAssistant,
					Contents: []message.Content{&message.TextContent{Text: fmt.Sprintf("Creating %d requests, %d paired.", total, paired)}},
				}, nil) {
					return
				}

				// Build interleaved requests: first `unpaired` are unpaired, last
				// `paired` are immediately resolved by appending matching results.
				var pairedResults []message.Content
				for i := 0; i < total; i++ {
					id := fmt.Sprintf("call-%d", i)
					call := &message.FunctionCallContent{CallID: id, Name: "TestFunction"}
					if i < unpaired {
						state.unpaired[id] = struct{}{}
					} else {
						pairedResults = append(pairedResults, &message.FunctionResultContent{CallID: id, Result: "ok"})
					}
					if !yield(&agent.ResponseUpdate{
						Role:     message.RoleAssistant,
						Contents: []message.Content{call},
					}, nil) {
						return
					}
				}
				if len(pairedResults) > 0 {
					if !yield(&agent.ResponseUpdate{
						Role:     message.RoleAssistant,
						Contents: pairedResults,
					}, nil) {
						return
					}
				}
				state.hasSentReqs = true
				return
			}
			// Subsequent invocation: count resolved requests.
			for _, m := range msgs {
				for _, c := range m.Contents {
					if r, ok := c.(*message.FunctionResultContent); ok {
						delete(state.unpaired, r.CallID)
					}
				}
			}
			text := "Done"
			if remaining := len(state.unpaired); remaining > 0 {
				text = fmt.Sprintf("Remaining: %d", remaining)
			}
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: text}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "request", Run: run},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)
}

// TestHostedAgent_InterceptsOnlyUnpairedFunctionCalls_PortMode verifies that
// only FunctionCalls without a matching FunctionResultContent in the same
// response trigger an ExternalRequest, and that the workflow only emits a
// final "Done" response after every outstanding call has been resolved via
// matching ExternalResponses.
func TestHostedAgent_InterceptsOnlyUnpairedFunctionCalls_PortMode(t *testing.T) {
	const unpaired, paired = 2, 3
	host := workflowhosting.New(newRequestAgent(unpaired, paired), workflowhosting.Config{
		EmitResponseEvents: true,
	})
	wf, err := workflow.NewBuilder(host).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, workflow.TurnToken{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var requests []*workflow.ExternalRequest
	for evt := range run.OutgoingEvents() {
		if r, ok := evt.(workflow.RequestInfoEvent); ok {
			requests = append(requests, r.Request)
		}
	}
	if got := len(requests); got != unpaired {
		t.Fatalf("expected %d external requests (only unpaired), got %d", unpaired, got)
	}

	// Resume with all but the last response and verify "Remaining: 1".
	for _, req := range requests[:unpaired-1] {
		v, _ := req.Data.As(reflect.TypeFor[*message.FunctionCallContent]())
		call := v.(*message.FunctionCallContent)
		resp, err := req.NewResponse(&message.FunctionResultContent{CallID: call.CallID, Result: "ok"})
		if err != nil {
			t.Fatalf("NewResponse: %v", err)
		}
		if _, err := run.Resume(ctx, resp); err != nil {
			t.Fatalf("Resume: %v", err)
		}
	}
	mid := lastResponseText(t, run)
	if mid != "Remaining: 1" {
		t.Errorf("intermediate response = %q, want %q", mid, "Remaining: 1")
	}

	// Final response.
	final := requests[unpaired-1]
	v, _ := final.Data.As(reflect.TypeFor[*message.FunctionCallContent]())
	call := v.(*message.FunctionCallContent)
	resp, err := final.NewResponse(&message.FunctionResultContent{CallID: call.CallID, Result: "ok"})
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	last := lastResponseText(t, run)
	if last != "Done" {
		t.Errorf("final response = %q, want %q", last, "Done")
	}
}

func TestHostedAgent_ResultBeforeFunctionCall_StillInterceptsCall(t *testing.T) {
	const callID = "call-after-result"
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionResultContent{CallID: callID, Result: "early"},
					&message.FunctionCallContent{CallID: callID, Name: "do"},
				},
			}, nil)
		}
	}
	a := agent.New(
		agent.ProviderConfig{ProviderName: "ordered", Run: run},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)
	host := workflowhosting.New(a, workflowhosting.Config{})
	wf, err := workflow.NewBuilder(host).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	runResult, err := inproc.Default.Run(context.Background(), wf, workflow.TurnToken{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var requests []*workflow.ExternalRequest
	for evt := range runResult.OutgoingEvents() {
		if r, ok := evt.(workflow.RequestInfoEvent); ok {
			requests = append(requests, r.Request)
		}
	}
	if len(requests) != 1 {
		t.Fatalf("external request count = %d, want 1", len(requests))
	}
	payload, ok := requests[0].Data.As(reflect.TypeFor[*message.FunctionCallContent]())
	if !ok {
		t.Fatalf("request payload type = %T, want *message.FunctionCallContent", requests[0].Data.Any())
	}
	if got := payload.(*message.FunctionCallContent).CallID; got != callID {
		t.Fatalf("request CallID = %q, want %q", got, callID)
	}
}

// lastResponseText drains all currently-available events from the run and
// returns the text of the last response output observed (or "" if none).
func lastResponseText(t *testing.T, run *inproc.Run) string {
	t.Helper()
	var text string
	for evt := range run.OutgoingEvents() {
		if o, ok := evt.(workflow.OutputEvent); ok {
			if response, ok := o.Output.(*agent.Response); ok && response != nil && len(response.Messages) > 0 {
				text = response.Messages[len(response.Messages)-1].Contents.Text()
			}
		}
	}
	return text
}

// TestHostedAgent_DuplicateRequestID_RaisesError verifies that emitting two
// FunctionCallContents with the same CallID in a single response is rejected.
func TestHostedAgent_DuplicateRequestID_RaisesError(t *testing.T) {
	dup := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "x", Name: "f"},
					&message.FunctionCallContent{CallID: "x", Name: "f"},
				},
			}, nil)
		}
	}
	a := agent.New(
		agent.ProviderConfig{ProviderName: "dup", Run: dup},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)
	host := workflowhosting.New(a, workflowhosting.Config{
		InterceptUnterminatedFunctionCalls: true,
	})
	wf, err := workflow.NewBuilder(host).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, workflow.TurnToken{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawErr bool
	for evt := range run.OutgoingEvents() {
		if e, ok := evt.(workflow.ErrorEvent); ok && e.Error != nil &&
			strings.Contains(e.Error.Error(), "duplicate function call id") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Errorf("expected ErrorEvent for duplicate CallID, got none")
	}
}

// TestHostedAgent_UnknownResponseID_RaisesError verifies that a
// FunctionResultContent whose CallID does not match a pending call is
// surfaced as an ErrorEvent.
func TestHostedAgent_UnknownResponseID_RaisesError(t *testing.T) {
	// Agent never produces any FunctionCalls; we route a stray
	// FunctionResultContent to the host directly.
	stub := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "hi"}},
			}, nil)
		}
	}
	a := agent.New(
		agent.ProviderConfig{ProviderName: "stub", Run: stub},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)
	host := workflowhosting.New(a, workflowhosting.Config{
		InterceptUnterminatedFunctionCalls: true,
	})

	// Sender forwards a stray FunctionResultContent to the host.
	senderID := "sender"
	sender := workflow.ExecutorBinding{
		ID:           senderID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	sender.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: senderID,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				SendTypes: []reflect.Type{reflect.TypeFor[*message.FunctionResultContent]()},
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, _ any) (any, error) {
						return nil, ctx.SendMessage(host.ID, &message.FunctionResultContent{
							CallID: "no-such-call",
							Result: "x",
						})
					}), nil
				},
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(sender).
		AddEdge(sender, host).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawErr bool
	for evt := range run.OutgoingEvents() {
		if e, ok := evt.(workflow.ErrorEvent); ok && e.Error != nil &&
			strings.Contains(e.Error.Error(), "no pending function call") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Errorf("expected ErrorEvent for unknown CallID, got none")
	}
}

// TestHostedAgent_HeldTurnToken_StampsResolvedEmitEvents verifies that when
// a hosting executor holds the TurnToken across an intercepted request and
// later forwards it downstream, the forwarded TurnToken carries the resolved
// EmitEvents value (i.e. with EmitEvents != nil) rather than a possibly-nil
// input EmitEvents.
func TestHostedAgent_HeldTurnToken_StampsResolvedEmitEvents(t *testing.T) {
	host := workflowhosting.New(newApprovalAgent(), workflowhosting.Config{
		EmitUpdateEvents:           true,
		InterceptUserInputRequests: true,
	})
	app := approverExecutor(host, true)

	// Sink records every workflow.TurnToken it observes downstream.
	sinkID := "sink"
	var observed []workflow.TurnToken
	sink := workflow.ExecutorBinding{
		ID:           sinkID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	sink.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: sinkID,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandlerRaw(reflect.TypeFor[workflow.TurnToken](), nil, func(_ *workflow.Context, msg any) (any, error) {
						observed = append(observed, msg.(workflow.TurnToken))
						return nil, nil
					}), nil
				},
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(host).
		AddEdge(host, app).
		AddEdge(app, host).
		AddEdge(host, sink).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	// Send a TurnToken with EmitEvents=nil; the executor's EmitUpdateEvents
	// (true) is the resolved default.
	sendStreamMessage(t, stream, ctx, []*message.Message{{
		Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}},
	}})
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		_ = evt
	}

	if len(observed) == 0 {
		t.Fatalf("expected sink to observe at least one TurnToken downstream")
	}
	got := observed[len(observed)-1]
	if got.EmitEvents == nil {
		t.Fatalf("forwarded TurnToken.EmitEvents = nil, want non-nil (resolved)")
	}
	if !*got.EmitEvents {
		t.Errorf("forwarded TurnToken.EmitEvents = %v, want true (resolved from executor default)", *got.EmitEvents)
	}
}

// TestHostedAgent_RegistersDynamicPorts verifies that the hosting binding
// declares its UserInput / FunctionCall ports so they appear in
// Workflow.Ports (and hence in workflow metadata via ReflectPorts).
func TestHostedAgent_RegistersDynamicPorts(t *testing.T) {
	a := newReplayAgent()
	host := workflowhosting.New(a, workflowhosting.Config{})

	wf, err := workflow.NewBuilder(host).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	wantUserInput := host.ID + "_UserInput"
	wantFunctionCall := host.ID + "_FunctionCall"

	if _, ok := wf.Ports[wantUserInput]; !ok {
		t.Errorf("expected Workflow.Ports to contain %q, got %v", wantUserInput, wf.Ports)
	}
	if _, ok := wf.Ports[wantFunctionCall]; !ok {
		t.Errorf("expected Workflow.Ports to contain %q, got %v", wantFunctionCall, wf.Ports)
	}
}

// TestHostedAgent_HandledRequestNotReEmitted_PortMode mirrors .NET's
// Test_AsAgent_FunctionCallRoundtrip_ResponseIsProcessedAsync: after an
// ExternalResponse matches the executor's emitted FunctionCallContent, the
// original request must not be re-emitted on subsequent turns.
func TestHostedAgent_HandledRequestNotReEmitted_PortMode(t *testing.T) {
	host := workflowhosting.New(newFunctionCallAgent(), workflowhosting.Config{
		EmitResponseEvents: true,
	})
	wf, err := workflow.NewBuilder(host).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, workflow.TurnToken{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var firstReq *workflow.ExternalRequest
	for evt := range run.NewEvents() {
		if r, ok := evt.(workflow.RequestInfoEvent); ok && firstReq == nil {
			firstReq = r.Request
		}
	}
	if firstReq == nil {
		t.Fatalf("expected first turn to emit a RequestInfoEvent")
	}

	// Resume with a matching response.
	v, _ := firstReq.Data.As(reflect.TypeFor[*message.FunctionCallContent]())
	call := v.(*message.FunctionCallContent)
	resp, err := firstReq.NewResponse(&message.FunctionResultContent{CallID: call.CallID, Result: "ok"})
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// After resume, only NEW events should be observed; the handled
	// request must not appear among them.
	for evt := range run.NewEvents() {
		if r, ok := evt.(workflow.RequestInfoEvent); ok {
			payload, _ := r.Request.Data.As(reflect.TypeFor[*message.FunctionCallContent]())
			if c := payload.(*message.FunctionCallContent); c.CallID == call.CallID {
				t.Errorf("FunctionCallContent for already-handled CallID %q was re-emitted after resume", c.CallID)
			}
		}
	}
}

// TestHostedAgent_AlreadyPendingRequest_IsIdempotent_InterceptMode covers the
// across-response idempotent re-emission path: when an agent emits a
// FunctionApprovalRequestContent whose ID is already pending from a previous
// response (e.g. a re-emission), the host must not dispatch it twice nor
// raise an error. Mirrors .NET's
// AIContentExternalHandler.ProcessRequestContentAsync no-op-on-TryAdd-fail.
func TestHostedAgent_AlreadyPendingRequest_IsIdempotent_InterceptMode(t *testing.T) {
	const reqID = "req-1"
	step := 0
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			defer func() { step++ }()
			// Emit the same approval request on every turn.
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{&message.ToolApprovalRequestContent{
					RequestID: reqID,
					ToolCall: &message.FunctionCallContent{
						CallID: reqID,
						Name:   "do",
					},
				}},
			}, nil)
		}
	}
	a := agent.New(
		agent.ProviderConfig{ProviderName: "rep", Run: run},
		agent.Config{ID: testAgentID, Name: testAgentName, DisableFuncAutoCall: true},
	)

	// Probe records every approval-request workflow message it sees.
	probeID := "probe"
	var seen []*message.ToolApprovalRequestContent
	probe := workflow.ExecutorBinding{
		ID:           probeID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	probe.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: probeID,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandlerRaw(reflect.TypeFor[*message.ToolApprovalRequestContent](), nil, func(_ *workflow.Context, msg any) (any, error) {
						seen = append(seen, msg.(*message.ToolApprovalRequestContent))
						return nil, nil
					}), nil
				},
			},
		}, nil
	}

	host := workflowhosting.New(a, workflowhosting.Config{
		InterceptUserInputRequests: true,
	})
	wf, err := workflow.NewBuilder(host).
		AddEdge(host, probe).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	// Two consecutive turns with the same agent => same request ID twice.
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{})

	var sawErr bool
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		if e, ok := evt.(workflow.ErrorEvent); ok && e.Error != nil &&
			strings.Contains(e.Error.Error(), "duplicate FunctionApprovalRequest") {
			sawErr = true
		}
	}
	if sawErr {
		t.Errorf("re-emission of already-pending request ID should be idempotent, not raise duplicate error")
	}
	// Probe should observe the request only once across both turns.
	if len(seen) != 1 {
		t.Errorf("probe saw %d approval requests, want 1 (idempotent)", len(seen))
	}
}
