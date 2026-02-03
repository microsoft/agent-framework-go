// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/agenttest"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/message"
)

func TestAgent_RunText(t *testing.T) {
	var capturedMessages []*message.Message
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) {
			capturedMessages = messages
		},
	).AddText("Hello, world!")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	resp, err := a.RunText("test message").Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify message was converted correctly
	if len(capturedMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedMessages))
	}

	if capturedMessages[0].Role != message.RoleUser {
		t.Errorf("expected role %s, got %s", message.RoleUser, capturedMessages[0].Role)
	}

	textContent, ok := capturedMessages[0].Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", capturedMessages[0].Contents[0])
	}

	if textContent.Text != "test message" {
		t.Errorf("expected text 'test message', got %q", textContent.Text)
	}

	// Verify response and author info
	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 response message, got %d", len(resp.Messages))
	}

	if resp.Messages[0].Role != message.RoleAssistant {
		t.Errorf("expected role %s, got %s", message.RoleAssistant, resp.Messages[0].Role)
	}

	if resp.Messages[0].AuthorID != a.ID() {
		t.Errorf("expected author ID %q, got %q", a.ID(), resp.Messages[0].AuthorID)
	}

	if resp.Messages[0].AuthorName != a.Name() {
		t.Errorf("expected author name %q, got %q", a.Name(), resp.Messages[0].AuthorName)
	}
}

func TestAgent_RunMessage(t *testing.T) {
	var capturedMessages []*message.Message
	var capturedOptions []agentopt.RunOption
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) {
			capturedMessages = messages
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	inputMsg := message.NewText("input")
	customOption := agentopt.Stream(false)
	resp, err := a.RunMessage(inputMsg, customOption).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify message was passed through
	if len(capturedMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedMessages))
	}

	if capturedMessages[0] != inputMsg {
		t.Errorf("expected input message to be passed through")
	}

	// Verify options were passed
	if len(capturedOptions) == 0 {
		t.Fatal("expected options to be passed, got none")
	}

	if _, ok := agentopt.Get(capturedOptions, agentopt.Stream); !ok {
		t.Error("expected Stream option to be present")
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 response message, got %d", len(resp.Messages))
	}
}

func TestAgent_Run(t *testing.T) {
	var capturedMessages []*message.Message
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) {
			capturedMessages = messages
		},
	).AddText("response")

	a := agenttest.NewAgent(responseBuilder.Build())

	messages := []*message.Message{
		message.NewText("first"),
		message.NewText("second"),
	}

	ctx := t.Context()
	resp, err := a.Run(messages).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(capturedMessages))
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 response message, got %d", len(resp.Messages))
	}
}

func TestAgent_Run_CreatesSession(t *testing.T) {
	var capturedOptions []agentopt.RunOption
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) {
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.Run([]*message.Message{message.NewText("test")}).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that a session was created and passed
	session, ok := agentopt.Get(capturedOptions, agentopt.Session)
	if !ok {
		t.Fatal("expected session to be created")
	}

	if session == nil {
		t.Error("expected session to be non-nil")
	}
}

func TestAgent_Run_UsesProvidedSession(t *testing.T) {
	var capturedOptions []agentopt.RunOption
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) {
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	providedSession := agenttest.CreateSession()
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(providedSession)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	session, ok := agentopt.Get(capturedOptions, agentopt.Session)
	if !ok {
		t.Fatal("expected session to be present")
	}

	if session != providedSession {
		t.Error("expected provided session to be used")
	}
}

func TestAgent_Run_PrependsAgentOptions(t *testing.T) {
	var capturedOptions []agentopt.RunOption
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{{
			Callbacks: []func(context.Context, []*message.Message, ...agentopt.RunOption){
				func(ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) {
					capturedOptions = opts
				},
			},
			Responses: []agenttest.Response{
				{Response: &message.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "response"},
					},
				}},
			},
		}},
	}

	agentOption := agentopt.Stream(true)
	a := agent.New(agent.Config{
		Metadata: agent.Metadata{
			ID:   "test",
			Name: "test",
		},
		RunOptions: []agentopt.RunOption{agentOption},
		CreateSession: func(ctx context.Context, opts ...agentopt.CreateSessionOption) (memory.Session, error) {
			return agenttest.CreateSession(), nil
		},
		UnmarshalSession: func(data []byte) (memory.Session, error) { return agenttest.CreateSession(), nil },
		Run:              runner.Run,
	})

	ctx := t.Context()
	callOption := agentopt.Stream(false)
	_, err := a.Run([]*message.Message{message.NewText("test")}, callOption).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent options should be prepended, so call options come after
	if len(capturedOptions) < 2 {
		t.Fatalf("expected at least 2 options, got %d", len(capturedOptions))
	}
}

func TestAgent_Run_StreamingResponses(t *testing.T) {
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("chunk 1").
		AddText("chunk 2").
		AddText("chunk 3")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	updates := []*message.ResponseUpdate{}
	for update, err := range a.Run([]*message.Message{message.NewText("test")}).All(ctx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}
}

func TestAgent_Run_AddsMetadataToContext(t *testing.T) {
	var capturedCtx context.Context
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) {
			capturedCtx = ctx
		},
	).AddText("response")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.Run([]*message.Message{message.NewText("test")}).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	metadata, ok := agent.MetadataFromContext(capturedCtx)
	if !ok {
		t.Fatal("expected metadata in context")
	}

	if metadata != a.Metadata() {
		t.Errorf("expected metadata %+v, got %+v", a.Metadata(), metadata)
	}
}

func TestRun_Collect(t *testing.T) {
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("hello").
		AddText(" world")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	resp, err := a.Run([]*message.Message{message.NewText("test")}).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message after coalescing, got %d", len(resp.Messages))
	}

	if resp.Messages[0].Role != message.RoleAssistant {
		t.Errorf("expected role %s, got %s", message.RoleAssistant, resp.Messages[0].Role)
	}
}

func TestRun_Collect_WithError(t *testing.T) {
	expectedErr := errors.New("collection error")
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("before error").
		AddError(expectedErr).
		AddText("after error")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.Run([]*message.Message{message.NewText("test")}).Collect(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestRun_All(t *testing.T) {
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("chunk 1").
		AddText("chunk 2").
		AddText("chunk 3")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	updates := []*message.ResponseUpdate{}
	for update, err := range a.Run([]*message.Message{message.NewText("test")}).All(ctx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}
}

func TestRun_All_WithError(t *testing.T) {
	expectedErr := errors.New("streaming error")
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("before error").
		AddError(expectedErr).
		AddText("after error")

	a := agenttest.NewAgent(responseBuilder.Build())

	ctx := t.Context()
	updateCount := 0
	var receivedErr error
	for _, err := range a.Run([]*message.Message{message.NewText("test")}).All(ctx) {
		if err != nil {
			receivedErr = err
			break
		}
		updateCount++
	}

	if receivedErr == nil {
		t.Fatal("expected error, got nil")
	}

	if receivedErr != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, receivedErr)
	}

	if updateCount != 1 {
		t.Errorf("expected 1 update before error, got %d", updateCount)
	}
}
