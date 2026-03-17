// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/tool"
)

type stubTool struct {
	name string
}

func (t stubTool) Name() string {
	return t.name
}

func (t stubTool) Description() string {
	return t.name
}

type prependMiddleware struct {
	prependMessages []*message.Message
	instructions    string
	runCalls        int
	lastSession     *memory.Session
}

func (m *prependMiddleware) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
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

func (m *errorMiddleware) Run(_ middleware.RunFunc, _ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		yield(nil, m.err)
	}
}

type trackingMiddleware struct {
	runCalls int
	lastErr  error
}

func (m *trackingMiddleware) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
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

func failRunFunc(runErr error) func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, runErr)
		}
	}
}

func newGenericTestAgent(runFn func(context.Context, []*message.Message, ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error], instructions string, middlewares []middleware.Middleware, runOptions ...agentopt.Option) *agent.Agent {
	return agent.New(agent.ProviderConfig{
		Run: runFn,
	}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		Instructions: instructions,
		Middlewares:  middlewares,
		RunOptions:   runOptions,
	})
}

func TestAgent_RunText(t *testing.T) {
	var capturedMessages []*message.Message
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
			capturedMessages = messages
		},
	).AddText("Hello, world!")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	resp, err := a.RunText(ctx, "test message").Collect()
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
	var capturedOptions []agentopt.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
			capturedMessages = messages
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	inputMsg := message.NewText("input")
	customOption := agentopt.Stream(false)
	resp, err := a.RunMessage(ctx, inputMsg, customOption).Collect()
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
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
			capturedMessages = messages
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	messages := []*message.Message{
		message.NewText("first"),
		message.NewText("second"),
	}

	ctx := t.Context()
	resp, err := a.Run(ctx, messages).Collect()
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

func TestAgent_Run_RejectsMessagesWithContinuationToken(t *testing.T) {
	runCalled := false
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
			runCalled = true
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agentopt.ContinuationToken("token-123")).Collect()
	if err == nil {
		t.Fatal("expected error when continuation token and messages are both provided")
	}
	if runCalled {
		t.Fatal("expected run function not to be called when validation fails")
	}
}

func TestAgent_Run_CreatesSession(t *testing.T) {
	var capturedOptions []agentopt.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test").Collect()
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

func TestAgent_Run_RequiresSessionWhenAllowBackgroundResponsesEnabled(t *testing.T) {
	runCalled := false
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
			runCalled = true
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agentopt.AllowBackgroundResponses(true)).Collect()
	if err == nil {
		t.Fatal("expected error when AllowBackgroundResponses is enabled without a session")
	}
	if err.Error() != "a session must be provided when AllowBackgroundResponses is enabled" {
		t.Fatalf("unexpected error: %v", err)
	}
	if runCalled {
		t.Fatal("expected run function not to be called when validation fails")
	}
}

func TestAgent_Run_UsesProvidedSession(t *testing.T) {
	var capturedOptions []agentopt.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	providedSession := agenttest.CreateSession()
	_, err := a.RunText(ctx, "test", agentopt.Session(providedSession)).Collect()
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
	var capturedOptions []agentopt.Option
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{{
			Callbacks: []func(context.Context, []*message.Message, ...agentopt.Option){
				func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
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
	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		ID:         "test",
		Name:       "test",
		RunOptions: []agentopt.Option{agentOption},
	})

	ctx := t.Context()
	callOption := agentopt.Stream(false)
	_, err := a.RunText(ctx, "test", callOption).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent options should be prepended, so call options come after
	if len(capturedOptions) < 2 {
		t.Fatalf("expected at least 2 options, got %d", len(capturedOptions))
	}
}

func TestAgent_Run_AddsConfigToolsToRunOptions(t *testing.T) {
	var capturedOptions []agentopt.Option
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{{
			Callbacks: []func(context.Context, []*message.Message, ...agentopt.Option){
				func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
					capturedOptions = opts
				},
			},
			Responses: []agenttest.Response{{
				Response: &message.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "response"},
					},
				},
			}},
		}},
	}

	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{
		ID:    "test",
		Name:  "test",
		Tools: []tool.Tool{stubTool{name: "weather"}, stubTool{name: "time"}},
	})

	_, err := a.RunText(t.Context(), "test").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var names []string
	for configuredTool := range agentopt.All(capturedOptions, agentopt.Tool) {
		names = append(names, configuredTool.Name())
	}

	if !slices.Equal(names, []string{"weather", "time"}) {
		t.Fatalf("expected configured tools to be added to run options, got %v", names)
	}
}

func TestAgent_Run_StreamingResponses(t *testing.T) {
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("chunk 1").
		AddText("chunk 2").
		AddText("chunk 3")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	updates := []*message.ResponseUpdate{}
	for update, err := range a.RunText(ctx, "test", agentopt.Stream(true)) {
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
		func(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) {
			capturedCtx = ctx
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actx, ok := agent.AgentFromContext(capturedCtx)
	if !ok {
		t.Fatal("expected metadata in context")
	}

	if a != actx {
		t.Errorf("expected agent %+v, got %+v", a, actx)
	}
}

func TestAgent_Run_InvokesSingleContextMiddleware(t *testing.T) {
	mw := &prependMiddleware{
		prependMessages: []*message.Message{{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "context message"}}}},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}

	a := newGenericTestAgent(runFn, "", []middleware.Middleware{mw})

	ctx := t.Context()
	session := agenttest.CreateSession()
	_, err := a.RunText(ctx, "user input", agentopt.Session(session)).Collect()
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

func TestAgent_Run_ContextMiddlewareReceivesSession(t *testing.T) {
	mw := &prependMiddleware{}
	runFn := func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, "", []middleware.Middleware{mw})

	ctx := t.Context()
	session := agenttest.CreateSession()
	_, err := a.RunText(ctx, "test", agentopt.Session(session)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mw.lastSession != session {
		t.Error("expected middleware to receive the session")
	}
}

func TestAgent_Run_ContextMiddlewareCanFailBeforeRun(t *testing.T) {
	invokeErr := errors.New("middleware failed")
	runFn := func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, "", []middleware.Middleware{&errorMiddleware{err: invokeErr}})

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agentopt.Session(agenttest.CreateSession())).Collect()
	if !errors.Is(err, invokeErr) {
		t.Fatalf("expected %v, got %v", invokeErr, err)
	}
}

func TestAgent_Run_MiddlewareObservesRunFailure(t *testing.T) {
	runErr := errors.New("run failed")
	tracker := &trackingMiddleware{}
	a := newGenericTestAgent(failRunFunc(runErr), "", []middleware.Middleware{tracker})

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agentopt.Session(agenttest.CreateSession())).Collect()
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

func TestAgent_Run_IncludesInstructions(t *testing.T) {
	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, "You are a helpful assistant.", nil)

	ctx := t.Context()
	_, err := a.RunText(ctx, "hello", agentopt.Session(agenttest.CreateSession())).Collect()
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

func TestRun_Collect(t *testing.T) {
	responseBuilder := agenttest.NewResponseBuilder().
		AddText("hello").
		AddText(" world")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	resp, err := a.RunText(ctx, "test").Collect()
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

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test").Collect()
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

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	updates := []*message.ResponseUpdate{}
	for update, err := range a.RunText(ctx, "test", agentopt.Stream(true)) {
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

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	updateCount := 0
	var receivedErr error
	for _, err := range a.RunText(ctx, "test", agentopt.Stream(true)) {
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

func TestAgent_Run_ProviderMiddleware_RunsProvidersWhenSessionHasServiceID(t *testing.T) {
	provideCalled := false
	var capturedMessages []*message.Message

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			provideCalled = true
			return memory.Context{Messages: []*message.Message{message.NewText("history")}}, nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	session := agenttest.CreateSession()
	session.ServiceID = "server-managed"
	_, err := a.RunText(t.Context(), "input", agentopt.Session(session)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !provideCalled {
		t.Fatal("expected history provider to be used for service-managed sessions")
	}
	if len(capturedMessages) != 2 {
		t.Fatal("expected provider context to be prepended to request")
	}
}

func TestAgent_Run_ProviderMiddleware_RunsProvidersWithContinuationToken(t *testing.T) {
	provideCalled := false
	runCalled := false

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			provideCalled = true
			return memory.Context{}, nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		runCalled = true
		if len(msgs) != 0 {
			t.Fatalf("expected no messages with continuation token run, got %d", len(msgs))
		}
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	_, err := a.Run(t.Context(), nil, agentopt.ContinuationToken("ct-1")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !runCalled {
		t.Fatal("expected provider run function to be called")
	}
	if !provideCalled {
		t.Fatal("expected history provider to be used with continuation token")
	}
}

func TestAgent_Run_UsesConfigContextProvider(t *testing.T) {
	provideCalled := false
	runCalled := false

	contextProvider := &memory.ContextProvider{
		SourceID: "ctx-provider",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			provideCalled = true
			return memory.Context{}, nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{contextProvider},
	})

	_, err := a.RunText(t.Context(), "input", agentopt.Session(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !runCalled {
		t.Fatal("expected run function to be called")
	}
	if !provideCalled {
		t.Fatal("expected context provider to be used")
	}
}

func TestAgent_Run_ProviderMiddleware_PropagatesInvokingError(t *testing.T) {
	expected := errors.New("invoking failed")
	runCalled := false

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			return memory.Context{}, expected
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*message.ResponseUpdate, error) bool) {}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agentopt.Session(agenttest.CreateSession())).Collect()
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
	if runCalled {
		t.Fatal("expected run function not to be called when invoking fails")
	}
}

func TestAgent_Run_ProviderMiddleware_RunsProvidersWhenSessionAutoCreated(t *testing.T) {
	provideCalled := false
	runCalled := false

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			provideCalled = true
			return memory.Context{}, nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !runCalled {
		t.Fatal("expected run function to be called")
	}
	if !provideCalled {
		t.Fatal("expected history provider to run for auto-created session")
	}
}

func TestAgent_Run_ProviderMiddleware_PersistsHistoryAfterSuccessfulRun(t *testing.T) {
	historyMessage := message.NewText("history")
	requestMessage := message.NewText("input")

	var capturedMessages []*message.Message
	var storedRequest []*message.Message
	var storedResponse []*message.Message
	storeCalled := false

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			return memory.Context{Messages: []*message.Message{historyMessage}}, nil
		},
		Store: func(ctx memory.AfterRunContext) error {
			storeCalled = true
			storedRequest = ctx.RequestMessages
			storedResponse = ctx.ResponseMessages
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			if !yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "part1"}}}, nil) {
				return
			}
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "part2"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	_, err := a.RunMessage(t.Context(), requestMessage, agentopt.Session(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Contains(capturedMessages, requestMessage) {
		t.Fatal("expected request message to be included")
	}
	if !storeCalled {
		t.Fatal("expected store to be called")
	}
	if len(storedRequest) != 1 || storedRequest[0] != requestMessage {
		t.Fatal("expected default store filter to remove history-sourced request")
	}
	if len(storedResponse) == 0 {
		t.Fatal("expected response messages to be persisted")
	}
}

func TestAgent_Run_ProviderMiddleware_PersistsWithoutResponseMessages(t *testing.T) {
	storeCalled := false
	storedResponseCount := -1

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Store: func(ctx memory.AfterRunContext) error {
			storeCalled = true
			storedResponseCount = len(ctx.ResponseMessages)
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agentopt.Session(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !storeCalled {
		t.Fatal("expected store to be called when no response messages are produced")
	}
	if storedResponseCount != 0 {
		t.Fatalf("expected zero response messages, got %d", storedResponseCount)
	}
}

func TestAgent_Run_ProviderMiddleware_PropagatesInvokedError(t *testing.T) {
	expected := errors.New("invoked failed")

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Store: func(ctx memory.AfterRunContext) error {
			return expected
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agentopt.Session(agenttest.CreateSession())).Collect()
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestAgent_Run_ProviderMiddleware_EarlyStopOnErrorStillStores(t *testing.T) {
	runErr := errors.New("run failed")
	storeCalled := false
	var storedErr error

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Store: func(ctx memory.AfterRunContext) error {
			storeCalled = true
			storedErr = ctx.InvokeError
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			if !yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "before error"}}}, nil) {
				return
			}
			yield(nil, runErr)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agentopt.Session(agenttest.CreateSession())).Collect()
	if !errors.Is(err, runErr) {
		t.Fatalf("expected %v, got %v", runErr, err)
	}
	if !storeCalled {
		t.Fatal("expected store to be called when run stops on error")
	}
	if !errors.Is(storedErr, runErr) {
		t.Fatalf("expected invoke error %v, got %v", runErr, storedErr)
	}
}

func TestAgent_Run_ProviderMiddleware_EarlyStopWithoutErrorStillStores(t *testing.T) {
	storeCalled := false

	historyProvider := &memory.ContextProvider{
		SourceID: "history",
		Store: func(ctx memory.AfterRunContext) error {
			storeCalled = true
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			if !yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "first"}}}, nil) {
				return
			}
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "second"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{historyProvider},
	})

	for _, err := range a.RunText(t.Context(), "input", agentopt.Session(agenttest.CreateSession()), agentopt.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		break
	}

	if !storeCalled {
		t.Fatal("expected store to still be called when iteration stops without an error")
	}
}

func TestAgent_Run_UsesContextProvidersInOrder(t *testing.T) {
	sequence := make([]string, 0, 4)
	providerA := &memory.ContextProvider{
		SourceID: "provider-a",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			sequence = append(sequence, "before-a")
			return memory.Context{Messages: append([]*message.Message{message.NewText("a")}, ctx.Messages...)}, nil
		},
		Store: func(ctx memory.AfterRunContext) error {
			sequence = append(sequence, "after-a")
			return nil
		},
	}
	providerB := &memory.ContextProvider{
		SourceID: "provider-b",
		Provide: func(ctx memory.BeforeRunContext) (memory.Context, error) {
			sequence = append(sequence, "before-b")
			return memory.Context{Messages: append([]*message.Message{message.NewText("b")}, ctx.Messages...)}, nil
		},
		Store: func(ctx memory.AfterRunContext) error {
			sequence = append(sequence, "after-b")
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		if len(msgs) < 3 {
			t.Fatalf("expected providers to prepend messages, got %d", len(msgs))
		}
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*memory.ContextProvider{providerA, providerB},
	})

	_, err := a.RunText(t.Context(), "input", agentopt.Session(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"before-a", "before-b", "after-b", "after-a"}
	if !slices.Equal(sequence, expected) {
		t.Fatalf("expected sequence %v, got %v", expected, sequence)
	}
}
