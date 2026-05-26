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
	executor, ctx, _ := createExecutorWithSent(options)
	return executor, ctx
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
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute TurnToken failed: %v", err)
	}

	if len(te.receivedMessages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(te.receivedMessages))
	}
	if te.receivedMessages[0].Contents[0].(*message.TextContent).Text != "Hello" {
		t.Errorf("Expected first message 'Hello', got %s", te.receivedMessages[0].Contents[0].(*message.TextContent).Text)
	}
	if te.receivedMessages[1].Contents[0].(*message.TextContent).Text != "World" {
		t.Errorf("Expected second message 'World', got %s", te.receivedMessages[1].Contents[0].(*message.TextContent).Text)
	}
	if te.turnCount != 1 {
		t.Errorf("Expected 1 turn, got %d", te.turnCount)
	}
}

func TestExecutor_Handles_SingleMessage(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx := createExecutor(&messageworkflow.Options{
		StateKey:        "test-state",
		TakeTurnHandler: te.takeTurn,
	})

	msg := &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Single message"}}}

	if _, err := executor.Execute(ctx, msg); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute TurnToken failed: %v", err)
	}

	if len(te.receivedMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(te.receivedMessages))
	}
	if te.receivedMessages[0].Contents[0].(*message.TextContent).Text != "Single message" {
		t.Errorf("Expected message 'Single message', got %s", te.receivedMessages[0].Contents[0].(*message.TextContent).Text)
	}
	if te.turnCount != 1 {
		t.Errorf("Expected 1 turn, got %d", te.turnCount)
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
	// Note: Go doesn't have a separate array type vs slice for this purpose usually, but we can test iter.Seq if we want.
	// The C# test used array. Here we just use another slice or single message.
	if _, err := executor.Execute(ctx, &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Message 4"}}}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 4 {
		t.Errorf("Expected 4 messages, got %d", len(te.receivedMessages))
	}
	expectedTexts := []string{"Message 1", "Message 2", "Message 3", "Message 4"}
	for i, txt := range expectedTexts {
		if te.receivedMessages[i].Contents[0].(*message.TextContent).Text != txt {
			t.Errorf("Expected message %d to be '%s'", i, txt)
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
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(te.receivedMessages))
	}
	if te.receivedMessages[0].Contents[0].(*message.TextContent).Text != "Second batch" {
		t.Errorf("Expected message 'Second batch'")
	}
	if te.turnCount != 2 {
		t.Errorf("Expected 2 turns, got %d", te.turnCount)
	}
}

func TestExecutor_WithStringRole_ConvertsStringToMessage(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx := createExecutor(&messageworkflow.Options{
		StateKey:          "test-state",
		TakeTurnHandler:   te.takeTurn,
		StringMessageRole: string(message.RoleUser),
	})

	if _, err := executor.Execute(ctx, "String message"); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(te.receivedMessages))
	}
	if te.receivedMessages[0].Role != message.RoleUser {
		t.Errorf("Expected role User, got %s", te.receivedMessages[0].Role)
	}
	if te.receivedMessages[0].Contents[0].(*message.TextContent).Text != "String message" {
		t.Errorf("Expected message 'String message'")
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

	if _, err := executor.Execute(ctx, []*message.Message{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	emptySeq := func(yield func(*message.Message) bool) {}
	if _, err := executor.Execute(ctx, iter.Seq[*message.Message](emptySeq)); err != nil {
		t.Fatalf("Execute seq failed: %v", err)
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
	tests := []struct {
		name  string
		input any
	}{
		{
			name: "slice",
			input: []*message.Message{
				{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Test message"}}},
			},
		},
		{
			name: "sequence",
			input: iter.Seq[*message.Message](func(yield func(*message.Message) bool) {
				yield(&message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Test message"}}})
			}),
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
			if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
				t.Fatalf("Execute failed: %v", err)
			}

			if len(te.receivedMessages) != 1 {
				t.Fatalf("Expected 1 message, got %d", len(te.receivedMessages))
			}
			if te.receivedMessages[0].Contents.Text() != "Test message" {
				t.Fatalf("message text = %q, want %q", te.receivedMessages[0].Contents.Text(), "Test message")
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

	if _, err := executor.Execute(ctx, []*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Turn 1"}}}}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(te.receivedMessages))
	}

	if _, err := executor.Execute(ctx, &message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Turn 2"}}}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if _, err := executor.Execute(ctx, workflow.TurnToken{}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(te.receivedMessages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(te.receivedMessages))
	}
	if te.receivedMessages[0].Contents[0].(*message.TextContent).Text != "Turn 1" {
		t.Errorf("Expected message 'Turn 1'")
	}
	if te.receivedMessages[1].Contents[0].(*message.TextContent).Text != "Turn 2" {
		t.Errorf("Expected message 'Turn 2'")
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

func TestExecutor_DisableAutoSendTurnToken(t *testing.T) {
	te := &TestExecutor{}
	executor, ctx, sent := createExecutorWithSent(&messageworkflow.Options{
		StateKey:                 "test-state",
		TakeTurnHandler:          te.takeTurn,
		DisableAutoSendTurnToken: true,
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
