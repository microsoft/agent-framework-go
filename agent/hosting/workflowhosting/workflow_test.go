// Copyright (c) Microsoft. All rights reserved.

package workflowhosting_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
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
	run := func(ctx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			for _, s := range segments {
				if !yield(&message.ResponseUpdate{
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
	run := func(ctx context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			for _, m := range msgs {
				if m == nil || m.Role != message.RoleAssistant {
					continue
				}
				if m.AuthorName != "" && m.AuthorName != selfName {
					yield(nil, fmt.Errorf("message from other assistant role detected: AuthorName=%s", m.AuthorName))
					return
				}
			}
			yield(&message.ResponseUpdate{
				Role:       message.RoleAssistant,
				AuthorID:   testAgentID,
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

// runHostedAgent builds a single-node workflow hosting the agent under cfg,
// drives it with the given input messages and a TurnToken, and returns the
// collected workflow events.
func runHostedAgent(t *testing.T, a *agent.Agent, cfg workflowhosting.Config, token workflow.TurnToken, msgs []*message.Message) []workflow.Event {
	t.Helper()
	binding := workflowhosting.New(a, cfg)
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.OpenStream(ctx, wf, "")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer stream.Cancel()

	if len(msgs) > 0 {
		if err := stream.SendMessage(ctx, msgs); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}
	if err := stream.SendMessage(ctx, token); err != nil {
		t.Fatalf("send TurnToken: %v", err)
	}

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
// (executorSetting × turnSetting) for ResponseUpdateEvent emission. The rule:
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

			var updates []workflow.ResponseUpdateEvent
			for _, e := range events {
				if u, ok := e.(workflow.ResponseUpdateEvent); ok {
					updates = append(updates, u)
				}
			}

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
					if u.Update.AuthorName != testAgentName {
						t.Errorf("update[%d] AuthorName = %q, want %q", i, u.Update.AuthorName, testAgentName)
					}
					if len(u.Update.Contents) != 1 {
						t.Errorf("update[%d] expected 1 content, got %d", i, len(u.Update.Contents))
						continue
					}
					gotText := u.Update.Contents[0].(*message.TextContent).Text
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
// flag controls whether a single aggregated ResponseEvent is produced.
func TestHostedAgent_EmitsResponseIfConfigured(t *testing.T) {
	for _, executorSetting := range []bool{true, false} {
		t.Run(fmt.Sprintf("emit=%v", executorSetting), func(t *testing.T) {
			events := runHostedAgent(t, newReplayAgent(),
				workflowhosting.Config{EmitResponseEvents: executorSetting},
				workflow.TurnToken{},
				[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}}})

			var responses []workflow.ResponseEvent
			for _, e := range events {
				if r, ok := e.(workflow.ResponseEvent); ok {
					responses = append(responses, r)
				}
			}

			if executorSetting {
				if len(responses) != 1 {
					t.Fatalf("expected 1 ResponseEvent, got %d", len(responses))
				}
				resp := responses[0]
				if resp.ExecutorID == "" {
					t.Error("ResponseEvent missing ExecutorID")
				}
				if resp.Response == nil {
					t.Fatal("ResponseEvent.Response is nil")
				}
				// One *Message per non-empty source message: 3 in the
				// reference data (the first is empty and produces no updates,
				// hence no message).
				if len(resp.Response.Messages) != len(testReplayMessages)-1 {
					t.Errorf("Response.Messages count = %d, want %d",
						len(resp.Response.Messages), len(testReplayMessages)-1)
				}
				for _, msg := range resp.Response.Messages {
					if msg.AuthorName != testAgentName {
						t.Errorf("message AuthorName = %q, want %q", msg.AuthorName, testAgentName)
					}
				}
			} else if len(responses) != 0 {
				t.Errorf("expected no ResponseEvents, got %d", len(responses))
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

// TestHostedAgent_ForwardsIncomingMessages verifies that incoming messages
// are forwarded downstream by default and not when the option is disabled.
func TestHostedAgent_ForwardsIncomingMessages(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprintf("disable=%v", disable), func(t *testing.T) {
			// Sink executor simply records what it receives.
			var observed []any
			sinkID := "sink"
			sink := &workflow.ExecutorBinding{
				ID:           sinkID,
				ExecutorType: reflect.TypeFor[*workflow.Executor](),
			}
			sink.NewExecutor = func(_ string) (*workflow.Executor, error) {
				return &workflow.Executor{
					ID: sinkID,
					Options: workflow.ExecutorOptions{
						DisableAutoSendMessageHandlerResultObject: true,
						DisableAutoYieldOutputHandlerResultObject: true,
					},
					Config: []*workflow.ExecutorConfig{{
						ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
							return rb.AddHandler(reflect.TypeFor[[]*message.Message](), nil, false, func(_ *workflow.Context, msg any) (any, error) {
								observed = append(observed, msg)
								return nil, nil
							}), nil
						},
					}},
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
			stream, err := inproc.OpenStream(ctx, wf, "")
			if err != nil {
				t.Fatalf("OpenStream: %v", err)
			}
			defer stream.Cancel()

			input := []*message.Message{{
				Role:       message.RoleUser,
				AuthorName: "User",
				Contents:   []message.Content{&message.TextContent{Text: "ping"}},
			}}
			if err := stream.SendMessage(ctx, input); err != nil {
				t.Fatalf("send msgs: %v", err)
			}
			if err := stream.SendMessage(ctx, workflow.TurnToken{}); err != nil {
				t.Fatalf("send TurnToken: %v", err)
			}
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
