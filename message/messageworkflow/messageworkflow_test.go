// Copyright (c) Microsoft. All rights reserved.

package messageworkflow_test

import (
	"context"
	"iter"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

type TestExecutor struct {
	receivedMessages []*message.Message
	turnCount        int
}

func (e *TestExecutor) takeTurn(ctx *workflow.Context, token workflow.TurnToken, messages []*message.Message) error {
	e.receivedMessages = append(e.receivedMessages, messages...)
	e.turnCount++
	return nil
}

func createExecutor(options *messageworkflow.Options) (*workflow.Executor, *workflow.Context) {
	executor := workflow.Executor{ID: "test-executor"}
	messageworkflow.Configure(&executor, options)

	ctx := &workflow.Context{
		Context:     context.Background(),
		SendMessage: func(targetID string, message any) error { return nil },
		AddEvent:    func(event workflow.Event) error { return nil },
	}

	return &executor, ctx
}

func createExecutorWithSent(options *messageworkflow.Options) (*workflow.Executor, *workflow.Context, *[]any) {
	executor := workflow.Executor{ID: "test-executor"}
	messageworkflow.Configure(&executor, options)
	var sent []any

	ctx := &workflow.Context{
		Context: context.Background(),
		SendMessage: func(targetID string, message any) error {
			sent = append(sent, message)
			return nil
		},
		AddEvent: func(event workflow.Event) error { return nil },
	}

	return &executor, ctx, &sent
}

func TestExecutor_DescribedProtocol(t *testing.T) {
	te := &TestExecutor{}
	executor, _ := createExecutor(&messageworkflow.Options{
		StateKey:        "test-state",
		TakeTurnHandler: te.takeTurn,
	})

	protocol := executor.DescribeProtocol()

	// Verify it accepts expected types
	expectedTypes := []reflect.Type{
		reflect.TypeFor[*message.Message](),
		reflect.TypeFor[[]*message.Message](),
		reflect.TypeFor[iter.Seq[*message.Message]](),
		reflect.TypeFor[workflow.TurnToken](),
	}

	for _, expected := range expectedTypes {
		if !containsType(protocol.Accepts, expected) {
			t.Errorf("Protocol should accept type %v", expected)
		}
	}
	if !containsType(protocol.Sends, reflect.TypeFor[workflow.TurnToken]()) {
		t.Errorf("Protocol should send type %v", reflect.TypeFor[workflow.TurnToken]())
	}
}

func containsType(types []reflect.Type, want reflect.Type) bool {
	return slices.Contains(types, want)
}

func TestExecutor_Handles_ListOfMessages(t *testing.T) {
	for _, test := range []struct {
		name       string
		emitEvents *bool
	}{
		{name: "default"},
		{name: "disabled", emitEvents: new(false)},
	} {
		t.Run(test.name, func(t *testing.T) {
			te := &TestExecutor{}
			executor, ctx := createExecutor(&messageworkflow.Options{
				StateKey:        "test-state",
				TakeTurnHandler: te.takeTurn,
			})

			messages := []*message.Message{
				{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Hello"}}},
				{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "World"}}},
			}

			if _, err := executor.Execute(ctx, messages); err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if _, err := executor.Execute(ctx, workflow.TurnToken{EmitEvents: test.emitEvents}); err != nil {
				t.Fatalf("Execute TurnToken failed: %v", err)
			}

			if len(te.receivedMessages) != 2 {
				t.Fatalf("Expected 2 messages, got %d", len(te.receivedMessages))
			}
			if te.receivedMessages[0].Contents[0].(*message.TextContent).Text != "Hello" {
				t.Errorf("Expected first message 'Hello', got %s", te.receivedMessages[0].Contents[0].(*message.TextContent).Text)
			}
			if te.receivedMessages[1].Contents[0].(*message.TextContent).Text != "World" {
				t.Errorf("Expected second message 'World', got %s", te.receivedMessages[1].Contents[0].(*message.TextContent).Text)
			}
			if got := te.receivedMessages[0].String(); got != "Hello" {
				t.Errorf("first message text = %q, want %q", got, "Hello")
			}
			if got := te.receivedMessages[1].String(); got != "World" {
				t.Errorf("second message text = %q, want %q", got, "World")
			}
			if te.turnCount != 1 {
				t.Errorf("Expected 1 turn, got %d", te.turnCount)
			}
		})
	}
}

func TestExecutor_Handles_SingleMessage(t *testing.T) {
	for _, test := range []struct {
		name       string
		emitEvents *bool
	}{
		{name: "default"},
		{name: "disabled", emitEvents: new(false)},
		{name: "enabled", emitEvents: new(true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			te := &TestExecutor{}
			executor, ctx := createExecutor(&messageworkflow.Options{
				StateKey:        "test-state",
				TakeTurnHandler: te.takeTurn,
			})
			msg := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Single message"}}}
			token := workflow.TurnToken{EmitEvents: test.emitEvents}

			if _, err := executor.Execute(ctx, msg); err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if _, err := executor.Execute(ctx, token); err != nil {
				t.Fatalf("Execute TurnToken failed: %v", err)
			}

			if len(te.receivedMessages) != 1 {
				t.Fatalf("Expected 1 message, got %d", len(te.receivedMessages))
			}
			if te.receivedMessages[0].Contents[0].(*message.TextContent).Text != "Single message" {
				t.Errorf("Expected message 'Single message', got %s", te.receivedMessages[0].Contents[0].(*message.TextContent).Text)
			}
			if te.turnCount != 1 {
				t.Errorf("Expected 1 turn, got %d", te.turnCount)
			}
		})
	}
}

func TestExecutor_AccumulatesAndClearsMessagesPerTurn(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx := createExecutor(&messageworkflow.Options{
		StateKey:        "test-state",
		TakeTurnHandler: te.takeTurn,
	})

	// Send multiple message batches before taking a turn
	if _, err := executor.Execute(ctx, &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Message 1"}}}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Message 2"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Message 3"}}},
	}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Message 4"}}},
	}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if te.turnCount != 0 || len(te.receivedMessages) != 0 {
		t.Fatal("accumulated messages were processed before a turn token arrived")
	}

	if _, err := executor.Execute(ctx, workflow.TurnToken{EmitEvents: new(false)}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 4 {
		t.Fatalf("Expected 4 messages, got %d", len(te.receivedMessages))
	}
	expectedTexts := []string{"Message 1", "Message 2", "Message 3", "Message 4"}
	for i, txt := range expectedTexts {
		if got := te.receivedMessages[i].Contents.Text(); got != txt {
			t.Errorf("message %d text = %q, want %q", i, got, txt)
		}
	}
	if te.turnCount != 1 {
		t.Errorf("Expected 1 turn, got %d", te.turnCount)
	}

	// Clear received messages in our test struct to verify next turn
	te.receivedMessages = nil

	// Second turn should process new messages only
	if _, err := executor.Execute(ctx, []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Second batch"}}},
	}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{EmitEvents: new(false)}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(te.receivedMessages))
	}
	if got := te.receivedMessages[0].Contents.Text(); got != "Second batch" {
		t.Errorf("message text = %q, want %q", got, "Second batch")
	}
	if te.turnCount != 2 {
		t.Errorf("Expected 2 turns, got %d", te.turnCount)
	}
}

func TestExecutor_WithStringRole_ConvertsStringToMessage(t *testing.T) {
	for _, test := range []struct {
		name       string
		emitEvents *bool
	}{
		{name: "default"},
		{name: "disabled", emitEvents: new(false)},
	} {
		t.Run(test.name, func(t *testing.T) {
			te := &TestExecutor{}
			executor, ctx := createExecutor(&messageworkflow.Options{
				StateKey:          "test-state",
				TakeTurnHandler:   te.takeTurn,
				StringMessageRole: message.RoleUser,
			})

			if _, err := executor.Execute(ctx, "String message"); err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if _, err := executor.Execute(ctx, workflow.TurnToken{EmitEvents: test.emitEvents}); err != nil {
				t.Fatalf("Execute failed: %v", err)
			}

			if len(te.receivedMessages) != 1 {
				t.Fatalf("Expected 1 message, got %d", len(te.receivedMessages))
			}
			if te.receivedMessages[0].Role != message.RoleUser {
				t.Errorf("Expected role User, got %s", te.receivedMessages[0].Role)
			}
			if te.receivedMessages[0].Contents[0].(*message.TextContent).Text != "String message" {
				t.Errorf("Expected message 'String message'")
			}
			if got := te.receivedMessages[0].String(); got != "String message" {
				t.Errorf("message text = %q, want %q", got, "String message")
			}
		})
	}
}

func TestConfigure_RejectsNilOptions(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected Configure to panic for nil options")
		}
	}()
	messageworkflow.Configure(&workflow.Executor{ID: "test-executor"}, nil)
}

func TestConfigure_SnapshotsOptions(t *testing.T) {
	var configuredCalls int
	autoSendTurnToken := true
	options := &messageworkflow.Options{
		StateKey:          "test-state",
		StringMessageRole: message.RoleUser,
		AutoSendTurnToken: &autoSendTurnToken,
		TakeTurnHandler: func(_ *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
			configuredCalls++
			return nil
		},
	}
	executor, ctx, sent := createExecutorWithSent(options)
	options.StringMessageRole = ""
	*options.AutoSendTurnToken = false
	options.TakeTurnHandler = func(_ *workflow.Context, _ workflow.TurnToken, _ []*message.Message) error {
		t.Fatal("Configure retained caller-owned options")
		return nil
	}

	if _, err := executor.Execute(ctx, "String message"); err != nil {
		t.Fatalf("Execute string: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute turn token: %v", err)
	}
	if configuredCalls != 1 {
		t.Fatalf("configured handler calls = %d, want 1", configuredCalls)
	}
	if len(*sent) != 1 {
		t.Fatalf("sent messages = %v, want auto-sent turn token", *sent)
	}
}

func TestExecutor_UsesSharedMessageState(t *testing.T) {
	te := &TestExecutor{}
	state := messageworkflow.NewMessageState("test-state", "")
	executor, ctx := createExecutor(&messageworkflow.Options{
		StateKey:        "test-state",
		TakeTurnHandler: te.takeTurn,
		MessageState:    state,
	})

	first := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "buffered"}}}
	second := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "injected"}}}
	if _, err := executor.Execute(ctx, first); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if err := state.ProcessTurnMessages(ctx, func(_ *workflow.Context, messages []*message.Message) ([]*message.Message, error) {
		return append(messages, second), nil
	}); err != nil {
		t.Fatalf("ProcessTurnMessages: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute turn token failed: %v", err)
	}

	if len(te.receivedMessages) != 2 {
		t.Fatalf("received message count = %d, want 2", len(te.receivedMessages))
	}
	if te.receivedMessages[0] != first || te.receivedMessages[1] != second {
		t.Fatalf("received messages = %#v, want first then second", te.receivedMessages)
	}
}

func TestExecutor_EmptyCollection_HandledCorrectly(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx := createExecutor(&messageworkflow.Options{
		StateKey:        "test-state",
		TakeTurnHandler: te.takeTurn,
	})

	if _, err := executor.Execute(ctx, []*message.Message(nil)); err != nil {
		t.Fatalf("Execute nil slice failed: %v", err)
	}
	if _, err := executor.Execute(ctx, []*message.Message{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	emptySeq := func(yield func(*message.Message) bool) {}
	if _, err := executor.Execute(ctx, iter.Seq[*message.Message](emptySeq)); err != nil {
		t.Fatalf("Execute seq failed: %v", err)
	}
	if te.turnCount != 0 {
		t.Fatalf("turn count before token = %d, want 0", te.turnCount)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(te.receivedMessages))
	}
	if te.turnCount != 1 {
		t.Errorf("Expected 1 turn, got %d", te.turnCount)
	}
}

func TestExecutor_RoutesCollectionTypes(t *testing.T) {
	sourceMessages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Test message"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Reply"}}},
	}
	tests := []struct {
		name       string
		input      any
		wantTexts  []string
		emitEvents *bool
	}{
		{
			name:      "slice",
			input:     sourceMessages,
			wantTexts: []string{"Test message", "Reply"},
		},
		{
			name:      "sequence",
			input:     slices.Values(sourceMessages),
			wantTexts: []string{"Test message", "Reply"},
		},
		{
			name:       "singleton-slice",
			input:      sourceMessages[:1],
			wantTexts:  []string{"Test message"},
			emitEvents: new(false),
		},
		{
			name:       "singleton-sequence",
			input:      slices.Values(sourceMessages[:1]),
			wantTexts:  []string{"Test message"},
			emitEvents: new(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			te := &TestExecutor{}
			executor, ctx := createExecutor(&messageworkflow.Options{
				StateKey:        "test-state",
				TakeTurnHandler: te.takeTurn,
			})

			if _, err := executor.Execute(ctx, tt.input); err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if _, err := executor.Execute(ctx, workflow.TurnToken{EmitEvents: tt.emitEvents}); err != nil {
				t.Fatalf("Execute failed: %v", err)
			}

			if len(te.receivedMessages) != len(tt.wantTexts) {
				t.Fatalf("received message count = %d, want %d", len(te.receivedMessages), len(tt.wantTexts))
			}
			for i, want := range tt.wantTexts {
				if got := te.receivedMessages[i].Contents.Text(); got != want {
					t.Fatalf("message %d text = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestExecutor_MultipleTurns_EachTurnProcessesSeparately(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx := createExecutor(&messageworkflow.Options{
		StateKey:        "test-state",
		TakeTurnHandler: te.takeTurn,
	})

	first := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Turn 1"}}}
	second := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Turn 2"}}}
	if _, err := executor.Execute(ctx, []*message.Message{first}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(te.receivedMessages))
	}

	if _, err := executor.Execute(ctx, second); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if te.turnCount != 1 || !slices.Equal(te.receivedMessages, []*message.Message{first}) {
		t.Fatal("second message was processed before the second turn token")
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(te.receivedMessages))
	}
	if got := te.receivedMessages[0].Contents.Text(); got != "Turn 1" {
		t.Errorf("first message text = %q, want %q", got, "Turn 1")
	}
	if got := te.receivedMessages[1].Contents.Text(); got != "Turn 2" {
		t.Errorf("second message text = %q, want %q", got, "Turn 2")
	}
	if te.turnCount != 2 {
		t.Errorf("Expected 2 turns, got %d", te.turnCount)
	}
}

func TestExecutor_InitialWorkflowMessages_RoutedCorrectly(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx := createExecutor(&messageworkflow.Options{
		StateKey:        "test-state",
		TakeTurnHandler: te.takeTurn,
	})

	initialMessages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Kick off the workflow"}}},
	}
	if _, err := executor.Execute(ctx, initialMessages); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(te.receivedMessages))
	}
	if te.receivedMessages[0].Contents.Text() != "Kick off the workflow" {
		t.Fatalf("message text = %q, want %q", te.receivedMessages[0].Contents.Text(), "Kick off the workflow")
	}
}

func TestExecutor_DefaultAutoSendsTurnToken(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx, sent := createExecutorWithSent(&messageworkflow.Options{
		StateKey:        "test-state",
		TakeTurnHandler: te.takeTurn,
	})

	emit := true
	token := workflow.TurnToken{EmitEvents: &emit}
	if _, err := executor.Execute(ctx, token); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(*sent) != 1 {
		t.Fatalf("sent message count = %d, want 1", len(*sent))
	}
	got, ok := (*sent)[0].(workflow.TurnToken)
	if !ok {
		t.Fatalf("sent message type = %T, want workflow.TurnToken", (*sent)[0])
	}
	if got.EmitEvents == nil || !*got.EmitEvents {
		t.Fatalf("sent token EmitEvents = %v, want true", got.EmitEvents)
	}
}

func TestExecutor_AutoSendTurnTokenFalse(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx, sent := createExecutorWithSent(&messageworkflow.Options{
		StateKey:          "test-state",
		TakeTurnHandler:   te.takeTurn,
		AutoSendTurnToken: new(false),
	})

	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(*sent) != 0 {
		t.Fatalf("sent message count = %d, want 0", len(*sent))
	}
	protocol := executor.DescribeProtocol()
	if containsType(protocol.Sends, reflect.TypeFor[workflow.TurnToken]()) {
		t.Fatalf("Protocol sends = %v, want no TurnToken when auto-send is disabled", protocol.Sends)
	}
}
