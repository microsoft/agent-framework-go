// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/agent/middleware/messagehistory"
	"github.com/microsoft/agent-framework-go/message"
)

type prependMiddleware struct {
	prependMessages []*message.Message
	instructions    string
	runCalls        int
	lastSession     memory.Session
}

func (m *prependMiddleware) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	m.runCalls++
	if session, ok := agentopt.Get(opts, agentopt.Session); ok {
		m.lastSession = session
	}
	msgForNext := make([]*message.Message, 0, len(m.prependMessages)+1+len(messages))
	msgForNext = append(msgForNext, m.prependMessages...)
	if m.instructions != "" {
		msgForNext = append(msgForNext, &message.Message{
			Role: message.RoleSystem,
			Contents: []message.Content{
				&message.TextContent{Text: m.instructions},
			},
		})
	}
	msgForNext = append(msgForNext, messages...)
	return next(ctx, msgForNext, opts...)
}

type errorMiddleware struct {
	err error
}

func (m *errorMiddleware) Run(_ middleware.RunFunc, _ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		yield(nil, m.err)
	}
}

type trackingMiddleware struct {
	runCalls int
	lastErr  error
}

func (m *trackingMiddleware) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	m.runCalls++
	return func(yield func(*message.ResponseUpdate, error) bool) {
		for update, err := range next(ctx, messages, opts...) {
			if err != nil {
				m.lastErr = err
			}
			if !yield(update, err) {
				return
			}
		}
	}
}

func noopRunFunc(_ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		yield(&message.ResponseUpdate{
			Role:     message.RoleAssistant,
			Contents: []message.Content{&message.TextContent{Text: "response"}},
		}, nil)
	}
}

func failRunFunc(runErr error) RunFunc {
	return func(_ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, runErr)
		}
	}
}

func TestSession_IsLightweight(t *testing.T) {
	session := &Session{ConversationID: "conv-123"}
	if session.ConversationID != "conv-123" {
		t.Errorf("expected ConversationID 'conv-123', got %q", session.ConversationID)
	}
	if session.GetStateBag() == nil {
		t.Error("expected non-nil StateBag")
	}
}

func TestSession_Serialization_Roundtrip(t *testing.T) {
	session := &Session{ConversationID: "conv-456"}
	session.State.Set("key1", "value1")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("failed to marshal session: %v", err)
	}

	restored, err := newSessionFromJSON(data)
	if err != nil {
		t.Fatalf("failed to unmarshal session: %v", err)
	}

	if restored.ConversationID != "conv-456" {
		t.Errorf("expected ConversationID 'conv-456', got %q", restored.ConversationID)
	}
	if restored.GetStateBag() == nil {
		t.Error("expected non-nil StateBag after deserialization")
	}
}

func TestSession_Serialization_WithStateBag(t *testing.T) {
	session := &Session{ConversationID: "conv-789"}
	session.State.Set("greeting", "hello")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if _, ok := m["State"]; !ok {
		t.Error("expected 'State' field in JSON")
	}
}

func TestSession_Serialization_Empty(t *testing.T) {
	session := &Session{}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	restored, err := newSessionFromJSON(data)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if restored.ConversationID != "" {
		t.Errorf("expected empty ConversationID, got %q", restored.ConversationID)
	}
}

func TestCreateSession_ReturnsSessionWithStateBag(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	ctx := t.Context()
	session, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chatSession, ok := session.(*Session)
	if !ok {
		t.Fatalf("expected *Session, got %T", session)
	}
	if chatSession.GetStateBag() == nil {
		t.Error("expected non-nil StateBag on new session")
	}
}

func TestCreateSession_WithConversationID(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	ctx := t.Context()
	session, err := a.CreateSession(ctx, ConversationID("conv-abc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chatSession := session.(*Session)
	if chatSession.ConversationID != "conv-abc" {
		t.Errorf("expected ConversationID 'conv-abc', got %q", chatSession.ConversationID)
	}
}

func TestCreateSession_DoesNotAttachMiddlewareState(t *testing.T) {
	mw := &prependMiddleware{}
	a := NewAgent(noopRunFunc, Config{
		RunOptions: []agentopt.RunOption{middleware.With(mw)},
	}, ProviderConfig{})
	ctx := t.Context()
	session, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chatSession := session.(*Session)
	if chatSession.ConversationID != "" {
		t.Error("expected empty ConversationID")
	}
	if mw.runCalls != 0 {
		t.Error("middleware should not run during session creation")
	}
}

func TestRun_InvokesSingleContextMiddleware(t *testing.T) {
	mw := &prependMiddleware{
		prependMessages: []*message.Message{
			{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "context message"}}},
		},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		Instructions: "base instructions",
		RunOptions:   []agentopt.RunOption{middleware.With(mw)},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("user input")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mw.runCalls != 1 {
		t.Errorf("expected 1 middleware call, got %d", mw.runCalls)
	}

	foundContext := false
	for _, msg := range capturedMessages {
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "context message" {
				foundContext = true
			}
		}
	}
	if !foundContext {
		t.Error("expected context message to be included in messages sent to run function")
	}
}

func TestRun_InvokesMultipleContextMiddlewaresInSequence(t *testing.T) {
	mw1 := &prependMiddleware{
		prependMessages: []*message.Message{
			{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "middleware1 context"}}},
		},
	}
	mw2 := &prependMiddleware{
		prependMessages: []*message.Message{
			{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "middleware2 context"}}},
		},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		RunOptions: []agentopt.RunOption{middleware.With(mw1), middleware.With(mw2)},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("user input")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mw1.runCalls != 1 {
		t.Errorf("expected 1 middleware call for mw1, got %d", mw1.runCalls)
	}
	if mw2.runCalls != 1 {
		t.Errorf("expected 1 middleware call for mw2, got %d", mw2.runCalls)
	}

	found1, found2 := false, false
	for _, msg := range capturedMessages {
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok {
				if tc.Text == "middleware1 context" {
					found1 = true
				}
				if tc.Text == "middleware2 context" {
					found2 = true
				}
			}
		}
	}
	if !found1 {
		t.Error("expected middleware1 context message in captured messages")
	}
	if !found2 {
		t.Error("expected middleware2 context message in captured messages")
	}
}

func TestRun_ContextMiddlewareReceivesSession(t *testing.T) {
	mw := &prependMiddleware{}
	a := NewAgent(noopRunFunc, Config{
		RunOptions: []agentopt.RunOption{middleware.With(mw)},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mw.lastSession != session {
		t.Error("expected middleware to receive the session")
	}
}

func TestRun_ContextMiddlewareCanFailBeforeRun(t *testing.T) {
	invokeErr := errors.New("middleware failed")
	a := NewAgent(noopRunFunc, Config{
		RunOptions: []agentopt.RunOption{middleware.With(&errorMiddleware{err: invokeErr})},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if !errors.Is(err, invokeErr) {
		t.Fatalf("expected %v, got %v", invokeErr, err)
	}
}

func TestRun_MiddlewareObservesRunFailure(t *testing.T) {
	runErr := errors.New("run failed")
	tracker := &trackingMiddleware{}
	a := NewAgent(failRunFunc(runErr), Config{
		RunOptions: []agentopt.RunOption{middleware.With(tracker)},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if err == nil {
		t.Fatal("expected error")
	}

	if tracker.runCalls != 1 {
		t.Errorf("expected 1 middleware call, got %d", tracker.runCalls)
	}
	if !errors.Is(tracker.lastErr, runErr) {
		t.Errorf("expected middleware to observe %v, got %v", runErr, tracker.lastErr)
	}
}

func TestRun_UsesMessageHistoryMiddleware(t *testing.T) {
	historyMiddleware := messagehistory.New()

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		RunOptions: []agentopt.RunOption{middleware.With(historyMiddleware)},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	session.GetStateBag().Set("messagehistory.inmemory.messages", []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "previous message"}}},
	})
	_, err := a.Run([]*message.Message{message.NewText("new message")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundHistory := false
	for _, msg := range capturedMessages {
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "previous message" {
				foundHistory = true
			}
		}
	}
	if !foundHistory {
		t.Error("expected history message to be included in messages sent to run function")
	}
}

func TestRun_WorksWithoutMessageHistoryMiddleware(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateStreamResumption_RejectsWithoutConversationID(t *testing.T) {
	session := &Session{}
	err := validateStreamResumptionAllowed("some-token", session)
	if err == nil {
		t.Fatal("expected error for streaming resumption without ConversationID")
	}
}

func TestValidateStreamResumption_AllowsWithConversationID(t *testing.T) {
	session := &Session{ConversationID: "conv-123"}
	err := validateStreamResumptionAllowed("some-token", session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateStreamResumption_AllowsEmptyToken(t *testing.T) {
	session := &Session{}
	err := validateStreamResumptionAllowed("", session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_IncludesInstructions(t *testing.T) {
	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		Instructions: "You are a helpful assistant.",
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("hello")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedMessages) == 0 {
		t.Fatal("expected at least 1 message")
	}

	if capturedMessages[0].Role != message.RoleSystem {
		t.Errorf("expected first message to be system role, got %s", capturedMessages[0].Role)
	}
	tc, ok := capturedMessages[0].Contents[0].(*message.TextContent)
	if !ok || tc.Text != "You are a helpful assistant." {
		t.Error("expected instructions message as first message")
	}
}

func TestRun_ContextMiddlewareAddsInstructions(t *testing.T) {
	mw := &prependMiddleware{instructions: "extra instructions from middleware"}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		RunOptions: []agentopt.RunOption{middleware.With(mw)},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("hello")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInstruction := false
	for _, msg := range capturedMessages {
		if msg.Role == message.RoleSystem {
			for _, c := range msg.Contents {
				if tc, ok := c.(*message.TextContent); ok && tc.Text == "extra instructions from middleware" {
					foundInstruction = true
				}
			}
		}
	}
	if !foundInstruction {
		t.Error("expected middleware instructions to be included as system message")
	}
}

func TestRun_WorksWithoutContextMiddlewares(t *testing.T) {
	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("hello")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedMessages))
	}
}

func TestMarshalUnmarshalSession(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	ctx := t.Context()

	session, err := a.CreateSession(ctx, ConversationID("conv-test"))
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	data, err := a.MarshalSession(ctx, session)
	if err != nil {
		t.Fatalf("failed to marshal session: %v", err)
	}

	restored, err := a.UnmarshalSession(ctx, data)
	if err != nil {
		t.Fatalf("failed to unmarshal session: %v", err)
	}

	restoredSession := restored.(*Session)
	if restoredSession.ConversationID != "conv-test" {
		t.Errorf("expected ConversationID 'conv-test', got %q", restoredSession.ConversationID)
	}
}

func TestMarshalSession_RejectsIncompatibleSession(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	ctx := t.Context()

	incompatible := &incompatibleSession{}
	_, err := a.MarshalSession(ctx, incompatible)
	if err == nil {
		t.Fatal("expected error for incompatible session")
	}
}

type incompatibleSession struct{}

func (incompatibleSession) GetStateBag() *memory.StateBag { return nil }
