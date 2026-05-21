// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
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
	lastSession     *agent.Session
}

func (m *prependMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	m.runCalls++
	if session, ok := agent.GetOption(opts, agent.WithSession); ok {
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

func (m *errorMiddleware) Run(_ agent.RunFunc, _ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		yield(nil, m.err)
	}
}

type trackingMiddleware struct {
	runCalls int
	lastErr  error
}

func (m *trackingMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	m.runCalls++
	return func(yield func(*agent.ResponseUpdate, error) bool) {
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

func failRunFunc(runErr error) func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, runErr)
		}
	}
}

func messageStrings(messages []*message.Message) []string {
	strings := make([]string, 0, len(messages))
	for _, msg := range messages {
		strings = append(strings, msg.String())
	}
	return strings
}

func updateStrings(updates []*agent.ResponseUpdate) []string {
	strings := make([]string, 0, len(updates))
	for _, update := range updates {
		strings = append(strings, update.String())
	}
	return strings
}

func newGenericTestAgent(runFn func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error], middlewares []agent.Middleware, runOptions ...agent.Option) *agent.Agent {
	return agent.New(agent.ProviderConfig{
		Run: runFn,
	}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		Middlewares: middlewares,
		RunOptions:  runOptions,
	})
}

func TestAgent_RunText(t *testing.T) {
	var capturedMessages []*message.Message
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
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

	if resp.AgentID != a.ID() {
		t.Errorf("expected agent ID %q, got %q", a.ID(), resp.AgentID)
	}

	if resp.Messages[0].AuthorName != a.Name() {
		t.Errorf("expected author name %q, got %q", a.Name(), resp.Messages[0].AuthorName)
	}
}

func TestAgent_RunMessage(t *testing.T) {
	var capturedMessages []*message.Message
	var capturedOptions []agent.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedMessages = messages
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	inputMsg := message.NewText("input")
	customOption := agent.Stream(false)
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

	if _, ok := agent.GetOption(capturedOptions, agent.Stream); !ok {
		t.Error("expected Stream option to be present")
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 response message, got %d", len(resp.Messages))
	}
}

func TestAgent_Run(t *testing.T) {
	var capturedMessages []*message.Message
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
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
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			runCalled = true
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	token := agenttest.NewContinuationToken(t, "token-123")
	_, err := a.RunText(ctx, "test", agent.WithContinuationToken(token)).Collect()
	if err == nil {
		t.Fatal("expected error when continuation token and messages are both provided")
	}
	if runCalled {
		t.Fatal("expected run function not to be called when validation fails")
	}
}

func TestAgent_Run_RejectsRawContinuationToken(t *testing.T) {
	runCalled := false
	runFn := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent"})

	_, err := a.Run(t.Context(), nil, agent.WithContinuationToken("raw-token")).Collect()
	if err == nil {
		t.Fatal("expected error for raw continuation token")
	}
	if err.Error() != "continuation token is not a valid agent continuation token" {
		t.Fatalf("unexpected error: %v", err)
	}
	if runCalled {
		t.Fatal("expected run function not to be called")
	}
}

func TestAgent_Run_CreatesSession(t *testing.T) {
	var capturedOptions []agent.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
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
	session, ok := agent.GetOption(capturedOptions, agent.WithSession)
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
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			runCalled = true
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agent.AllowBackgroundResponses(true)).Collect()
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
	var capturedOptions []agent.Option
	responseBuilder := agenttest.NewResponseBuilder(
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedOptions = opts
		},
	).AddText("response")

	a := agenttest.New(responseBuilder.Build())

	ctx := t.Context()
	providedSession := agenttest.CreateSession()
	_, err := a.RunText(ctx, "test", agent.WithSession(providedSession)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	session, ok := agent.GetOption(capturedOptions, agent.WithSession)
	if !ok {
		t.Fatal("expected session to be present")
	}

	if session != providedSession {
		t.Error("expected provided session to be used")
	}
}

func TestAgent_Run_PrependsAgentOptions(t *testing.T) {
	var capturedOptions []agent.Option
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{{
			Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
				func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
					capturedOptions = opts
				},
			},
			Responses: []agenttest.Response{
				{Response: &agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "response"},
					},
				}},
			},
		}},
	}

	agentOption := agent.Stream(true)
	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		ID:         "test",
		Name:       "test",
		RunOptions: []agent.Option{agentOption},
	})

	ctx := t.Context()
	callOption := agent.Stream(false)
	_, err := a.RunText(ctx, "test", callOption).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent options should be prepended, so call options come after
	if len(capturedOptions) < 2 {
		t.Fatalf("expected at least 2 options, got %d", len(capturedOptions))
	}
}

func TestAgent_New_DoesNotAutomaticallyInvokeTools(t *testing.T) {
	invoked := false
	weatherTool := functool.MustNew(functool.Config{Name: "GetWeather", Description: "Get weather"}, func(context.Context, struct{}) (string, error) {
		invoked = true
		return "sunny", nil
	})

	runFn := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call-1", Name: "GetWeather", Arguments: `{}`},
				},
			}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{})
	resp, err := a.RunText(t.Context(), "weather", agent.WithTool(weatherTool)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invoked {
		t.Fatal("expected agent.New not to invoke tools automatically")
	}
	if len(resp.Messages) != 1 || len(resp.Messages[0].Contents) != 1 {
		t.Fatalf("expected one function-call response message, got %#v", resp.Messages)
	}
	if _, ok := resp.Messages[0].Contents[0].(*message.FunctionCallContent); !ok {
		t.Fatalf("expected function call content to pass through, got %T", resp.Messages[0].Contents[0])
	}
}

func TestAgent_Run_AddsConfigToolsToRunOptions(t *testing.T) {
	var capturedOptions []agent.Option
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{{
			Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
				func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
					capturedOptions = opts
				},
			},
			Responses: []agenttest.Response{{
				Response: &agent.ResponseUpdate{
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
	for configuredTool := range agent.AllOptions(capturedOptions, agent.WithTool) {
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
	updates := []*agent.ResponseUpdate{}
	for update, err := range a.RunText(ctx, "test", agent.Stream(true)) {
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
		func(ctx context.Context, messages []*message.Message, opts ...agent.Option) {
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
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}

	a := newGenericTestAgent(runFn, []agent.Middleware{mw})

	ctx := t.Context()
	session := agenttest.CreateSession()
	_, err := a.RunText(ctx, "user input", agent.WithSession(session)).Collect()
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
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, []agent.Middleware{mw})

	ctx := t.Context()
	session := agenttest.CreateSession()
	_, err := a.RunText(ctx, "test", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mw.lastSession != session {
		t.Error("expected middleware to receive the session")
	}
}

func TestAgent_Run_ContextMiddlewareCanFailBeforeRun(t *testing.T) {
	middlewareErr := errors.New("middleware failed")
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, []agent.Middleware{&errorMiddleware{err: middlewareErr}})

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, middlewareErr) {
		t.Fatalf("expected %v, got %v", middlewareErr, err)
	}
}

func TestAgent_Run_MiddlewareObservesRunFailure(t *testing.T) {
	runErr := errors.New("run failed")
	tracker := &trackingMiddleware{}
	a := newGenericTestAgent(failRunFunc(runErr), []agent.Middleware{tracker})

	ctx := t.Context()
	_, err := a.RunText(ctx, "test", agent.WithSession(agenttest.CreateSession())).Collect()
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

func TestAgent_Run_IncludesInstructionsRunOption(t *testing.T) {
	var capturedMessages []*message.Message
	var capturedInstructions []string
	runFn := func(_ context.Context, msgs []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		capturedInstructions = slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, nil, agent.WithInstructions("  You are a helpful assistant.  "))

	ctx := t.Context()
	_, err := a.RunText(ctx, "hello", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := messageStrings(capturedMessages); !slices.Equal(got, []string{"hello"}) {
		t.Fatalf("messages = %v, want [hello]", got)
	}
	if !slices.Equal(capturedInstructions, []string{"You are a helpful assistant."}) {
		t.Fatalf("instructions = %q, want %q", capturedInstructions, []string{"You are a helpful assistant."})
	}
}

func TestAgent_Run_CombinesInstructionOptions(t *testing.T) {
	var capturedInstructions []string
	runFn := func(_ context.Context, _ []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedInstructions = slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}
	a := newGenericTestAgent(runFn, nil, agent.WithInstructions(" base instructions "))

	_, err := a.RunText(t.Context(), "hello", agent.WithSession(agenttest.CreateSession()), agent.WithInstructions(" run instructions ")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(capturedInstructions, []string{"base instructions", "run instructions"}) {
		t.Fatalf("instructions = %q, want combined instruction options", capturedInstructions)
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
	updates := []*agent.ResponseUpdate{}
	for update, err := range a.RunText(ctx, "test", agent.Stream(true)) {
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
	for _, err := range a.RunText(ctx, "test", agent.Stream(true)) {
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
	var storedResponseMessages []*message.Message

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			provideCalled = true
			return append(messages, message.NewText("history")), options, nil
		},
		Store: func(_ context.Context, _ []*message.Message, responseMessages []*message.Message, _ ...agent.Option) error {
			storedResponseMessages = responseMessages
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
	})

	session := agenttest.CreateSession()
	session.SetServiceID("server-managed")
	_, err := a.RunText(t.Context(), "input", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !provideCalled {
		t.Fatal("expected history provider to be used for service-managed sessions")
	}
	if len(capturedMessages) != 2 {
		t.Fatal("expected provider context to be appended to request")
	}
	if capturedMessages[0].String() != "input" || capturedMessages[1].String() != "history" {
		t.Fatalf("expected message order [input history], got [%s %s]", capturedMessages[0].String(), capturedMessages[1].String())
	}
	if got := messageStrings(storedResponseMessages); !slices.Equal(got, []string{"ok"}) {
		t.Fatalf("stored response messages = %v, want [ok]", got)
	}
}

func TestAgent_Run_ContextProviders_SkipWithContinuationToken(t *testing.T) {
	provideCalled := false
	runCalled := false

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			provideCalled = true
			return messages, options, nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		if len(msgs) != 0 {
			t.Fatalf("expected no messages with continuation token run, got %d", len(msgs))
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
	})

	token := agenttest.NewContinuationToken(t, "ct-1")
	_, err := a.Run(t.Context(), nil, agent.WithContinuationToken(token)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !runCalled {
		t.Fatal("expected provider run function to be called")
	}
	if provideCalled {
		t.Fatal("expected context provider to be skipped with continuation token")
	}
}

func TestAgent_Run_StreamingContinuationToken_SavesInputMessagesAndUpdates(t *testing.T) {
	runCalls := 0
	runFn := func(_ context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalls++
		if runCalls == 1 {
			if got := messageStrings(messages); !slices.Equal(got, []string{"Tell me a story"}) {
				t.Fatalf("initial messages = %v, want [Tell me a story]", got)
			}
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Once"}}, ContinuationToken: "inner-1"}, nil)
			}
		}

		if len(messages) != 0 {
			t.Fatalf("resume messages = %v, want none", messageStrings(messages))
		}
		if token, _ := agent.GetOption(options, agent.WithContinuationToken); token != "inner-1" {
			t.Fatalf("resume continuation token = %q, want inner-1", token)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " upon"}}, ContinuationToken: "inner-2"}, nil) {
				return
			}
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " a"}}, ContinuationToken: "inner-3"}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " time"}}, ContinuationToken: "inner-4"}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent"})
	session := agenttest.CreateSession()

	var firstToken string
	for update, err := range a.RunText(t.Context(), "Tell me a story", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected initial error: %v", err)
		}
		firstToken = update.ContinuationToken
		break
	}
	first := agenttest.DecodeContinuationToken(t, firstToken)
	if first.Type != agenttest.ContinuationTokenType || first.InnerToken != "inner-1" {
		t.Fatalf("first token = %#v, want wrapped inner-1", first)
	}
	if got := messageStrings(first.InputMessages); !slices.Equal(got, []string{"Tell me a story"}) {
		t.Fatalf("first token input messages = %v, want [Tell me a story]", got)
	}
	if len(first.ResponseUpdates) != 1 || first.ResponseUpdates[0].String() != "Once" {
		t.Fatalf("first token updates = %v, want [Once]", updateStrings(first.ResponseUpdates))
	}

	var tokens []agenttest.ContinuationToken
	for update, err := range a.Run(t.Context(), nil, agent.WithSession(session), agent.WithContinuationToken(firstToken), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected resume error: %v", err)
		}
		tokens = append(tokens, agenttest.DecodeContinuationToken(t, update.ContinuationToken))
	}
	if len(tokens) != 3 {
		t.Fatalf("resume token count = %d, want 3", len(tokens))
	}
	last := tokens[len(tokens)-1]
	if last.InnerToken != "inner-4" {
		t.Fatalf("last inner token = %q, want inner-4", last.InnerToken)
	}
	if got := messageStrings(last.InputMessages); !slices.Equal(got, []string{"Tell me a story"}) {
		t.Fatalf("last token input messages = %v, want [Tell me a story]", got)
	}
	if got := updateStrings(last.ResponseUpdates); !slices.Equal(got, []string{"Once", " upon", " a", " time"}) {
		t.Fatalf("last token updates = %v, want [Once  upon  a  time]", got)
	}
}

func TestAgent_Run_ContinuationToken_PersistsSavedResponseUpdates(t *testing.T) {
	var historyResponseMessages []*message.Message
	var contextResponseMessages []*message.Message
	var historyStoreSawContinuationToken bool
	var contextStoreSawContinuationToken bool
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Store: func(_ context.Context, _ []*message.Message, responseMessages []*message.Message, options ...agent.Option) error {
			historyResponseMessages = responseMessages
			_, historyStoreSawContinuationToken = agent.GetOption(options, agent.WithContinuationToken)
			return nil
		},
	}
	contextProvider := &agent.ContextProvider{
		SourceID: "ctx",
		Store: func(_ context.Context, _ []*message.Message, responseMessages []*message.Message, options ...agent.Option) error {
			contextResponseMessages = responseMessages
			_, contextStoreSawContinuationToken = agent.GetOption(options, agent.WithContinuationToken)
			return nil
		},
	}
	runFn := func(_ context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		if len(messages) != 0 {
			t.Fatalf("resume messages = %v, want none", messageStrings(messages))
		}
		if token, _ := agent.GetOption(options, agent.WithContinuationToken); token != "inner" {
			t.Fatalf("provider continuation token = %q, want inner", token)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " upon"}}}, nil) {
				return
			}
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " a"}}}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " time"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		HistoryProvider:  historyProvider,
		ContextProviders: []*agent.ContextProvider{contextProvider},
	})
	token := agenttest.EncodeContinuationToken(t, agenttest.ContinuationToken{
		Type:       agenttest.ContinuationTokenType,
		InnerToken: "inner",
		ResponseUpdates: []*agent.ResponseUpdate{
			{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "once"}}},
		},
	})

	_, err := a.Run(t.Context(), nil, agent.WithSession(agenttest.CreateSession()), agent.WithContinuationToken(token), agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := messageStrings(historyResponseMessages); !slices.Equal(got, []string{"once upon a time"}) {
		t.Fatalf("history response messages = %v, want [once upon a time]", got)
	}
	if got := messageStrings(contextResponseMessages); !slices.Equal(got, []string{"once upon a time"}) {
		t.Fatalf("context response messages = %v, want [once upon a time]", got)
	}
	if historyStoreSawContinuationToken {
		t.Fatal("history provider Store saw continuation token option")
	}
	if contextStoreSawContinuationToken {
		t.Fatal("context provider Store saw continuation token option")
	}
}

func TestAgent_Run_ContinuationToken_PersistsSavedInputMessages(t *testing.T) {
	var historyRequestMessages []*message.Message
	var contextRequestMessages []*message.Message
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Store: func(_ context.Context, requestMessages []*message.Message, _ []*message.Message, _ ...agent.Option) error {
			historyRequestMessages = requestMessages
			return nil
		},
	}
	contextProvider := &agent.ContextProvider{
		SourceID: "ctx",
		Store: func(_ context.Context, requestMessages []*message.Message, _ []*message.Message, _ ...agent.Option) error {
			contextRequestMessages = requestMessages
			return nil
		},
	}
	runFn := func(_ context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		if len(messages) != 0 {
			t.Fatalf("resume messages = %v, want none", messageStrings(messages))
		}
		if token, _ := agent.GetOption(options, agent.WithContinuationToken); token != "inner" {
			t.Fatalf("provider continuation token = %q, want inner", token)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		HistoryProvider:  historyProvider,
		ContextProviders: []*agent.ContextProvider{contextProvider},
	})
	token := agenttest.EncodeContinuationToken(t, agenttest.ContinuationToken{
		Type:          agenttest.ContinuationTokenType,
		InnerToken:    "inner",
		InputMessages: []*message.Message{message.NewText("Tell me a story")},
	})

	_, err := a.Run(t.Context(), nil, agent.WithSession(agenttest.CreateSession()), agent.WithContinuationToken(token), agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := messageStrings(historyRequestMessages); !slices.Equal(got, []string{"Tell me a story"}) {
		t.Fatalf("history request messages = %v, want [Tell me a story]", got)
	}
	if got := messageStrings(contextRequestMessages); !slices.Equal(got, []string{"Tell me a story"}) {
		t.Fatalf("context request messages = %v, want [Tell me a story]", got)
	}
}

func TestAgent_Run_UsesConfigContextProvider(t *testing.T) {
	provideCalled := false
	runCalled := false

	contextProvider := &agent.ContextProvider{
		SourceID: "ctx-provider",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			provideCalled = true
			return messages, options, nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{contextProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
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

func TestAgent_Run_DefaultHistoryProvider_SkipsAutoCreatedSession(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first"}) {
							t.Fatalf("first turn messages = %v, want [first]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"second"}) {
							t.Fatalf("second turn messages = %v, want [second]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ID: "test-agent", Name: "test-agent"})

	if _, err := a.RunText(t.Context(), "first").Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second").Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_SkipsServiceManagedSession(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first"}) {
							t.Fatalf("first turn messages = %v, want [first]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"second"}) {
							t.Fatalf("second turn messages = %v, want [second]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ID: "test-agent", Name: "test-agent"})
	session := agenttest.CreateSession()
	session.SetServiceID("server-managed")

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_SkipsWhenSessionBecomesServiceManaged(t *testing.T) {
	turn := 0
	runFn := func(_ context.Context, msgs []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		turn++
		want := []string{"first"}
		if turn == 2 {
			want = []string{"second"}
		}
		if got := messageStrings(msgs); !slices.Equal(got, want) {
			t.Fatalf("turn %d messages = %v, want %v", turn, got, want)
		}
		session, _ := agent.GetOption(options, agent.WithSession)
		session.SetServiceID("server-managed")
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent"})
	session := agenttest.CreateSession()

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_UsesExplicitLocalSession(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first"}) {
							t.Fatalf("first turn messages = %v, want [first]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first", "one", "second"}) {
							t.Fatalf("second turn messages = %v, want [first one second]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ID: "test-agent", Name: "test-agent"})
	session := agenttest.CreateSession()

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_DefaultHistoryProvider_RunsWithContextProviders(t *testing.T) {
	contextProvider := &agent.ContextProvider{
		SourceID: "ctx",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return append(messages, message.NewText("context")), options, nil
		},
	}
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first", "context"}) {
							t.Fatalf("first turn messages = %v, want [first context]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first", "context", "one", "second", "context"}) {
							t.Fatalf("second turn messages = %v, want [first context one second context]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		ContextProviders: []*agent.ContextProvider{contextProvider},
	})
	session := agenttest.CreateSession()

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_UsesConfigHistoryProvider(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: []agenttest.Turn{
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first"}) {
							t.Fatalf("first turn messages = %v, want [first]", got)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "one"}}}}},
			},
			{
				Callbacks: []func(context.Context, []*message.Message, ...agent.Option){
					func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						if got := messageStrings(messages); !slices.Equal(got, []string{"first", "one", "second"}) {
							t.Fatalf("second turn messages = %v, want [first one second]", got)
						}
						if messages[0].SourceID != "in-memory" || messages[1].SourceID != "in-memory" || messages[2].SourceID != "" {
							t.Fatalf("unexpected source IDs: [%q %q %q]", messages[0].SourceID, messages[1].SourceID, messages[2].SourceID)
						}
					},
				},
				Responses: []agenttest.Response{{Response: &agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "two"}}}}},
			},
		},
	}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{
		ID:              "test-agent",
		Name:            "test-agent",
		HistoryProvider: agent.NewInMemoryHistoryProvider(""),
	})
	session := agenttest.CreateSession()

	if _, err := a.RunText(t.Context(), "first", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected first turn error: %v", err)
	}
	if _, err := a.RunText(t.Context(), "second", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("unexpected second turn error: %v", err)
	}
}

func TestAgent_Run_HistoryProvider_SkipsWhenSessionHasServiceID(t *testing.T) {
	provideCalled := false
	storeCalled := false
	var capturedMessages []*message.Message
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
			provideCalled = true
			return append([]*message.Message{message.NewText("history")}, messages...), nil
		},
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			storeCalled = true
			return nil
		},
	}
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent", HistoryProvider: historyProvider})
	session := agenttest.CreateSession()
	session.SetServiceID("server-managed")

	_, err := a.RunText(t.Context(), "input", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provideCalled || storeCalled {
		t.Fatal("expected configured history provider to be skipped for service-managed sessions")
	}
	if got := messageStrings(capturedMessages); !slices.Equal(got, []string{"input"}) {
		t.Fatalf("messages = %v, want [input]", got)
	}
}

func TestAgent_Run_HistoryProvider_ThrowsWhenServiceIDReturnedByDefault(t *testing.T) {
	provideCalled := false
	storeCalled := false
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
			provideCalled = true
			return messages, nil
		},
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			storeCalled = true
			return nil
		},
	}
	runFn := func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		session, _ := agent.GetOption(options, agent.WithSession)
		session.SetServiceID("server-managed")
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent", HistoryProvider: historyProvider})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err == nil {
		t.Fatal("expected history provider conflict error")
	}
	if err.Error() != "only Session.ServiceID or HistoryProvider may be used, but not both; the service returned an ID indicating service-managed history while the agent has a HistoryProvider configured" {
		t.Fatalf("error = %q", err.Error())
	}
	if !provideCalled {
		t.Fatal("expected history provider to run before the service returned an ID")
	}
	if storeCalled {
		t.Fatal("expected history provider store to be skipped on conflict")
	}
}

func TestAgent_Run_HistoryProvider_ClearsWhenThrowDisabledAndClearEnabled(t *testing.T) {
	provideCalls := 0
	storeCalled := false
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
			provideCalls++
			return messages, nil
		},
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			storeCalled = true
			return nil
		},
	}
	runCalls := 0
	runFn := func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalls++
		if runCalls == 1 {
			session, _ := agent.GetOption(options, agent.WithSession)
			session.SetServiceID("server-managed")
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:                           "test-agent",
		Name:                         "test-agent",
		HistoryProvider:              historyProvider,
		AllowHistoryProviderConflict: true,
	})

	if _, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
		t.Fatalf("unexpected first run error: %v", err)
	}
	if storeCalled {
		t.Fatal("expected cleared history provider not to store conflict run")
	}
	if provideCalls != 1 {
		t.Fatalf("provide calls = %d, want 1", provideCalls)
	}
	if _, err := a.RunText(t.Context(), "next", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
		t.Fatalf("unexpected second run error: %v", err)
	}
	if provideCalls != 1 {
		t.Fatalf("expected cleared history provider not to run again, got %d calls", provideCalls)
	}
}

func TestAgent_Run_HistoryProvider_KeepsWhenThrowAndClearDisabled(t *testing.T) {
	provideCalls := 0
	storeCalls := 0
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
			provideCalls++
			return messages, nil
		},
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			storeCalls++
			return nil
		},
	}
	runCalls := 0
	runFn := func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalls++
		if runCalls == 1 {
			session, _ := agent.GetOption(options, agent.WithSession)
			session.SetServiceID("server-managed")
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:                                     "test-agent",
		Name:                                   "test-agent",
		HistoryProvider:                        historyProvider,
		AllowHistoryProviderConflict:           true,
		SuppressHistoryProviderConflictWarning: true,
		KeepHistoryProviderOnConflict:          true,
	})

	if _, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
		t.Fatalf("unexpected first run error: %v", err)
	}
	if provideCalls != 1 || storeCalls != 1 {
		t.Fatalf("after first run provide/store = %d/%d, want 1/1", provideCalls, storeCalls)
	}
	if _, err := a.RunText(t.Context(), "next", agent.WithSession(agenttest.CreateSession())).Collect(); err != nil {
		t.Fatalf("unexpected second run error: %v", err)
	}
	if provideCalls != 2 || storeCalls != 2 {
		t.Fatalf("after second run provide/store = %d/%d, want 2/2", provideCalls, storeCalls)
	}
}

func TestAgent_Run_HistoryProvider_SkipsWithContinuationToken(t *testing.T) {
	provideCalled := false
	runCalled := false
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
			provideCalled = true
			return messages, nil
		},
	}
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		if len(msgs) != 0 {
			t.Fatalf("expected no messages with continuation token run, got %d", len(msgs))
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{ID: "test-agent", Name: "test-agent", HistoryProvider: historyProvider})

	token := agenttest.NewContinuationToken(t, "ct-1")
	_, err := a.Run(t.Context(), nil, agent.WithContinuationToken(token)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !runCalled {
		t.Fatal("expected provider run function to be called")
	}
	if provideCalled {
		t.Fatal("expected history provider to be skipped with continuation token")
	}
}

func TestAgent_Run_UsesHistoryBeforeContextProviders(t *testing.T) {
	sequence := make([]string, 0, 5)
	var storedRequestMessages []*message.Message
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
			sequence = append(sequence, "history-before")
			return append([]*message.Message{message.NewText("history")}, messages...), nil
		},
		Store: func(_ context.Context, requestMessages []*message.Message, _ []*message.Message, _ ...agent.Option) error {
			sequence = append(sequence, "history-after")
			storedRequestMessages = requestMessages
			return nil
		},
	}
	contextProvider := &agent.ContextProvider{
		SourceID: "ctx",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			sequence = append(sequence, "context-before")
			return append(messages, message.NewText("context")), options, nil
		},
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			sequence = append(sequence, "context-after")
			return nil
		},
	}
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		sequence = append(sequence, "run")
		if got := messageStrings(msgs); !slices.Equal(got, []string{"history", "input", "context"}) {
			t.Fatalf("messages = %v, want [history input context]", got)
		}
		if msgs[0].SourceID != "history" || msgs[1].SourceID != "" || msgs[2].SourceID != "ctx" {
			t.Fatalf("unexpected source IDs: [%q %q %q]", msgs[0].SourceID, msgs[1].SourceID, msgs[2].SourceID)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:               "test-agent",
		Name:             "test-agent",
		HistoryProvider:  historyProvider,
		ContextProviders: []*agent.ContextProvider{contextProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"history-before", "context-before", "run", "history-after", "context-after"}
	if !slices.Equal(sequence, expected) {
		t.Fatalf("sequence = %v, want %v", sequence, expected)
	}
	if got := messageStrings(storedRequestMessages); !slices.Equal(got, []string{"input", "context"}) {
		t.Fatalf("stored request messages = %v, want [input context]", got)
	}
}

func TestAgent_Run_HistoryProvider_DoesNotStoreInstructions(t *testing.T) {
	var storedRequestMessages []*message.Message
	var capturedInstructions []string
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Store: func(_ context.Context, requestMessages []*message.Message, _ []*message.Message, _ ...agent.Option) error {
			storedRequestMessages = requestMessages
			return nil
		},
	}
	runFn := func(_ context.Context, msgs []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedInstructions = slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
		if got := messageStrings(msgs); !slices.Equal(got, []string{"input"}) {
			t.Fatalf("messages = %v, want [input]", got)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:              "test-agent",
		Name:            "test-agent",
		HistoryProvider: historyProvider,
		RunOptions:      []agent.Option{agent.WithInstructions(" instructions ")},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(capturedInstructions, []string{"instructions"}) {
		t.Fatalf("instructions = %q, want instructions", capturedInstructions)
	}
	if got := messageStrings(storedRequestMessages); !slices.Equal(got, []string{"input"}) {
		t.Fatalf("stored request messages = %v, want [input]", got)
	}
}

func TestAgent_Run_HistoryProvider_SkipsStoreAfterRunError(t *testing.T) {
	expected := errors.New("run failed")
	storeCalled := false
	historyProvider := &agent.HistoryProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
			return messages, nil
		},
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			storeCalled = true
			return nil
		},
	}
	runFn := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, expected)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID:              "test-agent",
		Name:            "test-agent",
		HistoryProvider: historyProvider,
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v, want %v", err, expected)
	}
	if storeCalled {
		t.Fatal("expected history provider store to be skipped after run error")
	}
}

func TestAgent_Run_ProviderMiddleware_PropagatesInvokingError(t *testing.T) {
	expected := errors.New("invoking failed")
	runCalled := false

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Provide: func(context.Context, []*message.Message, ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return nil, nil, expected
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
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

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			provideCalled = true
			return messages, options, nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		runCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
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

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			return append(messages, historyMessage), options, nil
		},
		Store: func(_ context.Context, requestMessages, responseMessages []*message.Message, _ ...agent.Option) error {
			storeCalled = true
			storedRequest = requestMessages
			storedResponse = responseMessages
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "part1"}}}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "part2"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
	})

	_, err := a.RunMessage(t.Context(), requestMessage, agent.WithSession(agenttest.CreateSession())).Collect()
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

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Store: func(_ context.Context, _ []*message.Message, responseMessages []*message.Message, _ ...agent.Option) error {
			storeCalled = true
			storedResponseCount = len(responseMessages)
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
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

func TestAgent_Run_ProviderMiddleware_PropagatesStoreError(t *testing.T) {
	expected := errors.New("store failed")

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			return expected
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "response"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestAgent_Run_ProviderMiddleware_EarlyStopOnErrorStillStores(t *testing.T) {
	runErr := errors.New("run failed")
	storeCalled := false

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Store: func(_ context.Context, _ []*message.Message, _ []*message.Message, _ ...agent.Option) error {
			storeCalled = true
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "before error"}}}, nil) {
				return
			}
			yield(nil, runErr)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if !errors.Is(err, runErr) {
		t.Fatalf("expected %v, got %v", runErr, err)
	}
	if !storeCalled {
		t.Fatal("expected store to be called when run stops on error")
	}
}

func TestAgent_Run_ProviderMiddleware_EarlyStopWithoutErrorStillStores(t *testing.T) {
	storeCalled := false

	historyProvider := &agent.ContextProvider{
		SourceID: "history",
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			storeCalled = true
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "first"}}}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "second"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{historyProvider},
	})

	for _, err := range a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession()), agent.Stream(true)) {
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
	providerA := &agent.ContextProvider{
		SourceID: "provider-a",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			sequence = append(sequence, "before-a")
			return append(messages, message.NewText("a")), options, nil
		},
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			sequence = append(sequence, "after-a")
			return nil
		},
	}
	providerB := &agent.ContextProvider{
		SourceID: "provider-b",
		Provide: func(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			sequence = append(sequence, "before-b")
			return append(messages, message.NewText("b")), options, nil
		},
		Store: func(context.Context, []*message.Message, []*message.Message, ...agent.Option) error {
			sequence = append(sequence, "after-b")
			return nil
		},
	}

	runFn := func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		if len(msgs) != 3 {
			t.Fatalf("expected providers to append 2 messages to request, got %d", len(msgs))
		}
		if got := []string{msgs[0].String(), msgs[1].String(), msgs[2].String()}; !slices.Equal(got, []string{"input", "a", "b"}) {
			t.Fatalf("expected providers to append messages in order, got %v", got)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "ok"}}}, nil)
		}
	}

	a := agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{
		ID: "test-agent", Name: "test-agent",
		ContextProviders: []*agent.ContextProvider{providerA, providerB},
	})

	_, err := a.RunText(t.Context(), "input", agent.WithSession(agenttest.CreateSession())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"before-a", "before-b", "after-a", "after-b"}
	if !slices.Equal(sequence, expected) {
		t.Fatalf("expected sequence %v, got %v", expected, sequence)
	}
}
