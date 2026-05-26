// Copyright (c) Microsoft. All rights reserved.

package messageworkflow_test

import (
	"iter"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	testMessageContent = "TestMessageContent"
	customRoleName     = "CustomChatRole"
)

func newForwardingExecutorForTest(options *messageworkflow.ForwardingOptions) *workflow.Executor {
	executor := workflow.Executor{ID: "start"}
	messageworkflow.ConfigureForwarding(&executor, options)
	return &executor
}

func runForwardMessageTest(t *testing.T, executor *workflow.Executor, msg any) []any {
	t.Helper()
	var sent []any
	ctx := &workflow.Context{
		Context:     t.Context(),
		AddEvent:    func(workflow.Event) error { return nil },
		SendMessage: func(_ string, msg any) error { sent = append(sent, msg); return nil },
	}

	result, err := executor.Execute(ctx, msg)
	if err != nil {
		t.Fatalf("Execute(%T): %v", msg, err)
	}
	if result != nil {
		t.Fatalf("Execute(%T) result = %#v, want nil", msg, result)
	}
	return sent
}

func TestConfigureForwarding_DescribesForwardedTypes(t *testing.T) {
	executor := newForwardingExecutorForTest(nil)
	protocol := executor.DescribeProtocol()

	wantAccepts := []reflect.Type{
		reflect.TypeFor[*message.Message](),
		reflect.TypeFor[[]*message.Message](),
		reflect.TypeFor[iter.Seq[*message.Message]](),
		reflect.TypeFor[workflow.TurnToken](),
	}
	for _, typ := range wantAccepts {
		if !slices.Contains(protocol.Accepts, typ) {
			t.Errorf("Accepts missing %v; got %v", typ, protocol.Accepts)
		}
	}
	if slices.Contains(protocol.Accepts, reflect.TypeFor[string]()) {
		t.Errorf("Accepts includes string without StringMessageRole: %v", protocol.Accepts)
	}

	wantSends := []reflect.Type{
		reflect.TypeFor[*message.Message](),
		reflect.TypeFor[[]*message.Message](),
		reflect.TypeFor[workflow.TurnToken](),
	}
	for _, typ := range wantSends {
		if !slices.Contains(protocol.Sends, typ) {
			t.Errorf("Sends missing %v; got %v", typ, protocol.Sends)
		}
	}
}

func TestConfigureForwarding_DoesNotForwardStringByDefault(t *testing.T) {
	executor := newForwardingExecutorForTest(nil)
	ctx := &workflow.Context{
		Context:     t.Context(),
		AddEvent:    func(workflow.Event) error { return nil },
		SendMessage: func(_ string, _ any) error { return nil },
	}

	if _, err := executor.Execute(ctx, testMessageContent); err == nil {
		t.Fatal("expected string execution to fail when StringMessageRole is not configured")
	}
}

func TestConfigureForwarding_ForwardsStringIfConfigured(t *testing.T) {
	tests := []struct {
		name      string
		role      message.Role
		wantError bool
	}{
		{name: "none", wantError: true},
		{name: "user", role: message.RoleUser},
		{name: "assistant", role: message.RoleAssistant},
		{name: "custom", role: message.Role(customRoleName)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := newForwardingExecutorForTest(&messageworkflow.ForwardingOptions{
				StringMessageRole: test.role,
			})
			ctx := &workflow.Context{
				Context:     t.Context(),
				AddEvent:    func(workflow.Event) error { return nil },
				SendMessage: func(_ string, _ any) error { return nil },
			}

			if test.wantError {
				if _, err := executor.Execute(ctx, testMessageContent); err == nil {
					t.Fatal("expected string execution to fail")
				}
				return
			}

			sent := runForwardMessageTest(t, executor, testMessageContent)
			if len(sent) != 1 {
				t.Fatalf("sent count = %d, want 1", len(sent))
			}
			got, ok := sent[0].(*message.Message)
			if !ok {
				t.Fatalf("sent[0] = %T, want *message.Message", sent[0])
			}
			if got.Role != test.role {
				t.Fatalf("role = %q, want %q", got.Role, test.role)
			}
			if len(got.Contents) != 1 || got.Contents[0].(*message.TextContent).Text != testMessageContent {
				t.Fatalf("contents = %#v, want text %q", got.Contents, testMessageContent)
			}
		})
	}
}

func TestConfigureForwarding_ForwardsMessageUnmodified(t *testing.T) {
	executor := newForwardingExecutorForTest(nil)
	testMessage := &message.Message{
		Role:     message.Role(customRoleName),
		Contents: []message.Content{&message.TextContent{Text: testMessageContent}},
	}

	sent := runForwardMessageTest(t, executor, testMessage)
	if len(sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sent))
	}
	if sent[0] != testMessage {
		t.Fatalf("sent[0] = %#v, want original message", sent[0])
	}
}

func TestConfigureForwarding_ForwardsMessageSliceUnmodified(t *testing.T) {
	executor := newForwardingExecutorForTest(nil)
	testMessages := []*message.Message{
		{Role: message.Role(customRoleName), Contents: []message.Content{&message.TextContent{Text: testMessageContent}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ResponseMessage"}}},
	}

	sent := runForwardMessageTest(t, executor, testMessages)
	if len(sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sent))
	}
	got, ok := sent[0].([]*message.Message)
	if !ok || !slices.Equal(got, testMessages) {
		t.Fatalf("sent[0] = %#v, want original message slice", sent[0])
	}
}

func TestConfigureForwarding_ForwardsMessageSequenceAsSlice(t *testing.T) {
	executor := newForwardingExecutorForTest(nil)
	testMessages := []*message.Message{
		{Role: message.Role(customRoleName), Contents: []message.Content{&message.TextContent{Text: testMessageContent}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ResponseMessage"}}},
	}
	seq := iter.Seq[*message.Message](func(yield func(*message.Message) bool) {
		for _, msg := range testMessages {
			if !yield(msg) {
				return
			}
		}
	})

	sent := runForwardMessageTest(t, executor, seq)
	if len(sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sent))
	}
	got, ok := sent[0].([]*message.Message)
	if !ok || !slices.Equal(got, testMessages) {
		t.Fatalf("sent[0] = %#v, want collected message slice", sent[0])
	}
	if len(got) > 0 && &got[0] == &testMessages[0] {
		t.Fatalf("sent[0] shares slice storage with input sequence")
	}
}

func TestConfigureForwarding_ForwardsTurnTokenUnmodified(t *testing.T) {
	for _, emitEvents := range []*bool{nil, boolPtr(false), boolPtr(true)} {
		executor := newForwardingExecutorForTest(nil)
		token := workflow.TurnToken{EmitEvents: emitEvents}

		sent := runForwardMessageTest(t, executor, token)
		if len(sent) != 1 {
			t.Fatalf("sent count = %d, want 1", len(sent))
		}
		if !reflect.DeepEqual(sent[0], token) {
			t.Fatalf("sent[0] = %#v, want %#v", sent[0], token)
		}
	}
}

func boolPtr(value bool) *bool { return &value }
