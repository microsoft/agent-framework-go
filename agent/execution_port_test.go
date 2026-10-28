// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

type executionPortContext struct {
	agent.ContextProvider
	invoking []agent.InvokingContext
	invoked  []agent.InvokedContext
}

func (p *executionPortContext) Invoking(ctx context.Context, in agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	p.invoking = append(p.invoking, in)
	if p.ContextProvider != nil {
		return p.ContextProvider.Invoking(ctx, in)
	}
	return in.Messages, in.Options, nil
}

func (p *executionPortContext) Invoked(ctx context.Context, in agent.InvokedContext) error {
	p.invoked = append(p.invoked, in)
	if p.ContextProvider != nil {
		return p.ContextProvider.Invoked(ctx, in)
	}
	return nil
}

type executionPortHistory struct {
	agent.HistoryProvider
	invoking []agent.InvokingContext
	invoked  []agent.InvokedContext
}

func (p *executionPortHistory) Invoking(ctx context.Context, in agent.InvokingContext) ([]*message.Message, error) {
	p.invoking = append(p.invoking, in)
	if p.HistoryProvider != nil {
		return p.HistoryProvider.Invoking(ctx, in)
	}
	return in.Messages, nil
}

func (p *executionPortHistory) Invoked(ctx context.Context, in agent.InvokedContext) error {
	p.invoked = append(p.invoked, in)
	if p.HistoryProvider != nil {
		return p.HistoryProvider.Invoked(ctx, in)
	}
	return nil
}

func executionPortText(role message.Role, text string) *message.Message {
	return &message.Message{Role: role, Contents: []message.Content{&message.TextContent{Text: text}}}
}

func executionPortUpdate(text string) *agent.ResponseUpdate {
	return &agent.ResponseUpdate{
		Role: message.RoleAssistant, MessageID: "response",
		Contents: []message.Content{&message.TextContent{Text: text}},
	}
}

func executionPortRun(parts ...string) agent.RunFunc {
	return func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, part := range parts {
				if !yield(executionPortUpdate(part), nil) {
					return
				}
			}
		}
	}
}

func executionPortMessages(t *testing.T, got []*message.Message, want ...string) {
	t.Helper()
	texts := make([]string, len(got))
	for i, m := range got {
		texts[i] = m.String()
	}
	if !slices.Equal(texts, want) {
		t.Fatalf("messages = %q, want %q", texts, want)
	}
}

func executionPortSession(t *testing.T, a *agent.Agent) *agent.Session {
	t.Helper()
	s, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func executionPortConsume(t *testing.T, stream agent.ResponseStream) []*agent.ResponseUpdate {
	t.Helper()
	var updates []*agent.ResponseUpdate
	for update, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
	}
	return updates
}

func executionPortProvider(source string, messages []*message.Message, options ...agent.Option) *executionPortContext {
	return &executionPortContext{ContextProvider: agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: source,
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			return messages, options, nil
		},
	})}
}

func TestExecutionPort_ChatClientAgent_IdReturnsMetadataIdWhenMetadataProvided(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, agent.Config{ID: "custom-agent-id"})
	if a.ID() != "custom-agent-id" {
		t.Fatalf("ID = %q", a.ID())
	}
}

func TestExecutionPort_ChatClientAgent_IdFallsBackToBaseImplementationWhenMetadataIsNull(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, agent.Config{})
	if _, err := uuid.Parse(a.ID()); err != nil {
		t.Fatalf("ID %q is not a UUID: %v", a.ID(), err)
	}
}

func TestExecutionPort_ChatClientAgent_IdFallsBackToBaseImplementationWhenMetadataIdIsNull(t *testing.T) {
	cfg := agent.Config{ID: ""}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, cfg)
	if _, err := uuid.Parse(a.ID()); err != nil {
		t.Fatalf("ID %q is not a UUID: %v", a.ID(), err)
	}
}

func TestExecutionPort_ChatClientAgent_NameReturnsMetadataNameWhenMetadataProvided(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, agent.Config{Name: "Test Agent"})
	if a.Name() != "Test Agent" {
		t.Fatalf("Name = %q", a.Name())
	}
}

func TestExecutionPort_ChatClientAgent_NameReturnsNullWhenMetadataIsNull(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, agent.Config{})
	if a.Name() != "" {
		t.Fatalf("Name = %q", a.Name())
	}
}

func TestExecutionPort_ChatClientAgent_NameReturnsNullWhenMetadataNameIsNull(t *testing.T) {
	cfg := agent.Config{Name: ""}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, cfg)
	if a.Name() != "" {
		t.Fatalf("Name = %q", a.Name())
	}
}

func TestExecutionPort_ChatClientAgent_DescriptionReturnsMetadataDescriptionWhenMetadataProvided(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, agent.Config{Description: "A helpful test agent"})
	if a.Description() != "A helpful test agent" {
		t.Fatalf("Description = %q", a.Description())
	}
}

func TestExecutionPort_ChatClientAgent_DescriptionReturnsNullWhenMetadataIsNull(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, agent.Config{})
	if a.Description() != "" {
		t.Fatalf("Description = %q", a.Description())
	}
}

func TestExecutionPort_ChatClientAgent_DescriptionReturnsNullWhenMetadataDescriptionIsNull(t *testing.T) {
	cfg := agent.Config{Description: ""}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, cfg)
	if a.Description() != "" {
		t.Fatalf("Description = %q", a.Description())
	}
}

func TestExecutionPort_ChatClientAgent_VerifyChatClientAgentInvocationAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		return executionPortRun("I'm here!")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("base instructions")}})
	response, err := a.RunText(t.Context(), "Where are you?").Collect()
	if err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, response.Messages, "I'm here!")
	if calls != 1 || response.Messages[0].Role != message.RoleAssistant {
		t.Fatalf("provider calls = %d, role = %q", calls, response.Messages[0].Role)
	}
}

func TestExecutionPort_ChatClientAgent_RunAsyncSetsAuthorNameOnAllResponseMessagesAsync(t *testing.T) {
	for _, name := range []string{"TestAgent", ""} {
		t.Run(name, func(t *testing.T) {
			run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					for i, text := range []string{"response 1", "response 2"} {
						update := executionPortUpdate(text)
						update.MessageID = fmt.Sprint(i)
						if !yield(update, nil) {
							return
						}
					}
				}
			}
			a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{Name: name, RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
			response, err := a.RunText(t.Context(), "test").Collect()
			if err != nil {
				t.Fatal(err)
			}
			executionPortMessages(t, response.Messages, "response 1", "response 2")
			for _, m := range response.Messages {
				if m.AuthorName != name {
					t.Errorf("AuthorName = %q, want %q", m.AuthorName, name)
				}
			}
		})
	}
}

func TestExecutionPort_ChatClientAgent_RunAsyncRetrievesMessagesFromSessionWhenSessionHasChatHistoryProviderAsync(t *testing.T) {
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	_, err := a.RunText(t.Context(), "new message", agent.WithSession(executionPortSession(t, a))).Collect()
	if err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, captured, "new message")
}

func TestExecutionPort_ChatClientAgent_RunAsyncWorksWithoutInstructionsWhenInstructionsAreNullOrEmptyAsync(t *testing.T) {
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{})
	if _, err := a.RunText(t.Context(), "test message").Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, captured, "test message")
	if captured[0].Role != message.RoleUser {
		t.Fatalf("role = %q", captured[0].Role)
	}
}

func TestExecutionPort_ChatClientAgent_RunAsyncWorksWithEmptyMessagesWhenNoMessagesProvidedAsync(t *testing.T) {
	var captured []*message.Message
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		captured = slices.Clone(messages)
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	if _, err := a.Run(t.Context(), []*message.Message{}).Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(captured) != 0 {
		t.Fatalf("calls = %d, messages = %v", calls, captured)
	}
}

func TestExecutionPort_ChatClientAgent_RunAsyncInvokesAIContextProviderAndSucceedsWithEmptyAIContextAsync(t *testing.T) {
	p := &executionPortContext{}
	base := stubTool{name: "base function"}
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		executionPortMessages(t, messages, "user message")
		if messages[0].Role != message.RoleUser {
			t.Fatalf("role = %q", messages[0].Role)
		}
		if got := slices.Collect(agent.AllOptions(options, agent.WithInstructions)); !slices.Equal(got, []string{"base instructions"}) {
			t.Fatalf("instructions = %q", got)
		}
		if got := slices.Collect(agent.AllOptions(options, agent.WithTool)); !slices.Equal(got, []tool.Tool{base}) {
			t.Fatalf("tools = %v", got)
		}
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		ContextProviders: []agent.ContextProvider{p}, Tools: []tool.Tool{base},
		RunOptions: []agent.Option{agent.WithInstructions("base instructions")},
	})
	if _, err := a.RunText(t.Context(), "user message").Collect(); err != nil {
		t.Fatal(err)
	}
	if len(p.invoking) != 1 {
		t.Fatalf("Invoking calls = %d", len(p.invoking))
	}
}

func TestExecutionPort_ChatClientAgent_RunAsyncInvokesAIContextProviderWhenGetResponseFailsAsync(t *testing.T) {
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "context provider message")})
	failure := errors.New("downstream failure")
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(nil, failure) }
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		ContextProviders: []agent.ContextProvider{p}, Tools: []tool.Tool{stubTool{name: "base function"}},
		RunOptions: []agent.Option{agent.WithInstructions("base instructions")},
	})
	if _, err := a.RunText(t.Context(), "user message").Collect(); !errors.Is(err, failure) {
		t.Fatalf("error = %v, want %v", err, failure)
	}
	if len(p.invoking) != 1 || len(p.invoked) != 1 {
		t.Fatalf("lifecycle calls = %d/%d", len(p.invoking), len(p.invoked))
	}
	in := p.invoked[0]
	executionPortMessages(t, in.RequestMessages, "user message", "context provider message")
	if in.ResponseMessages != nil || !errors.Is(in.Err, failure) {
		t.Fatalf("completion = %#v", in)
	}
}

func TestExecutionPort_ChatClientAgent_RunAsyncInvokesMultipleAIContextProvidersOnFailureAsync(t *testing.T) {
	first, second := &executionPortContext{}, &executionPortContext{}
	failure := errors.New("downstream failure")
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(nil, failure) }
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		ContextProviders: []agent.ContextProvider{first, second},
		RunOptions:       []agent.Option{agent.WithInstructions("base instructions")},
	})
	if _, err := a.RunText(t.Context(), "user message").Collect(); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	for _, p := range []*executionPortContext{first, second} {
		if len(p.invoking) != 1 || len(p.invoked) != 1 || !errors.Is(p.invoked[0].Err, failure) {
			t.Fatalf("provider lifecycle = %#v", p)
		}
	}
}

func TestExecutionPort_ChatClientAgent_RunStreamingAsync_WithNullMessageId_PassesThroughNullAsync(t *testing.T) {
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, text := range []string{"Hello", " world"} {
				update := executionPortUpdate(text)
				update.MessageID = ""
				if !yield(update, nil) {
					return
				}
			}
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{})
	updates := executionPortConsume(t, a.RunText(t.Context(), "Hi", agent.Stream(true)))
	if len(updates) != 2 {
		t.Fatalf("updates = %d", len(updates))
	}
	for _, update := range updates {
		if update.MessageID != "" {
			t.Errorf("MessageID = %q", update.MessageID)
		}
	}
}

func TestExecutionPort_ChatClientAgent_RunStreamingAsync_WithProviderMessageId_PreservesItAsync(t *testing.T) {
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, text := range []string{"Hello", " world"} {
				update := executionPortUpdate(text)
				update.MessageID = "chatcmpl-abc123"
				if !yield(update, nil) {
					return
				}
			}
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{})
	updates := executionPortConsume(t, a.RunText(t.Context(), "Hi", agent.Stream(true)))
	if len(updates) != 2 {
		t.Fatalf("updates = %d", len(updates))
	}
	for _, update := range updates {
		if update.MessageID != "chatcmpl-abc123" {
			t.Errorf("MessageID = %q", update.MessageID)
		}
	}
}

func TestExecutionPort_ChatClientAgent_RunStreamingAsyncUsesChatHistoryProviderWhenNoConversationIdReturnedByChatClientAsync(t *testing.T) {
	history := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("wh", "at?")}, agent.Config{
		HistoryProvider: history, RunOptions: []agent.Option{agent.WithInstructions("test instructions")},
	})
	session := executionPortSession(t, a)
	executionPortConsume(t, a.RunText(t.Context(), "test", agent.WithSession(session), agent.Stream(true)))
	messages, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, messages, "test", "what?")
}

func TestExecutionPort_ChatClientAgent_RunStreamingAsyncIncludesChatHistoryInMessagesToChatClientAsync(t *testing.T) {
	var requests [][]*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		requests = append(requests, slices.Clone(messages))
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	session := executionPortSession(t, a)
	for _, text := range []string{"first", "second"} {
		executionPortConsume(t, a.RunText(t.Context(), text, agent.WithSession(session), agent.Stream(true)))
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	executionPortMessages(t, requests[1], "first", "response", "second")
}

func TestExecutionPort_ChatClientAgent_RunStreamingAsyncThrowsWhenChatHistoryProviderProvidedAndConversationIdReturnedByChatClientAsync(t *testing.T) {
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, part := range []string{"wh", "at?"} {
				update := executionPortUpdate(part)
				update.ConversationID = new("ConvId")
				if !yield(update, nil) {
					return
				}
			}
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		HistoryProvider: agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{}),
		RunOptions:      []agent.Option{agent.WithInstructions("test instructions")},
	})
	_, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)).Collect()
	if err == nil || err.Error() != "only Session.ServiceID or HistoryProvider may be used, but not both; the service returned an ID indicating service-managed history while the agent has a HistoryProvider configured" {
		t.Fatalf("error = %v, want configured-history conflict", err)
	}
}

func TestExecutionPort_ChatClientAgent_RunStreamingAsyncInvokesAIContextProviderAndUsesResultAsync(t *testing.T) {
	contextTool := stubTool{name: "context provider function"}
	baseTool := stubTool{name: "base function"}
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "context provider message")},
		agent.WithInstructions("context provider instructions"), agent.WithTool(contextTool))
	history := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		executionPortMessages(t, messages, "user message", "context provider message")
		if messages[0].Role != message.RoleUser || messages[1].Role != message.RoleSystem {
			t.Fatalf("roles = %q/%q", messages[0].Role, messages[1].Role)
		}
		if got := strings.Join(slices.Collect(agent.AllOptions(options, agent.WithInstructions)), "\n"); got != "base instructions\ncontext provider instructions" {
			t.Fatalf("instructions = %q", got)
		}
		if got := slices.Collect(agent.AllOptions(options, agent.WithTool)); !slices.Equal(got, []tool.Tool{baseTool, contextTool}) {
			t.Fatalf("tools = %v", got)
		}
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		ContextProviders: []agent.ContextProvider{p}, HistoryProvider: history, Tools: []tool.Tool{baseTool},
		RunOptions: []agent.Option{agent.WithInstructions("base instructions")},
	})
	session := executionPortSession(t, a)
	executionPortConsume(t, a.RunText(t.Context(), "user message", agent.WithSession(session), agent.Stream(true)))
	stored, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, stored, "user message", "context provider message", "response")
	if len(p.invoking) != 1 || len(p.invoked) != 1 {
		t.Fatalf("lifecycle calls = %d/%d", len(p.invoking), len(p.invoked))
	}
	executionPortMessages(t, p.invoked[0].RequestMessages, "user message", "context provider message")
	executionPortMessages(t, p.invoked[0].ResponseMessages, "response")
	if p.invoked[0].Err != nil {
		t.Fatal(p.invoked[0].Err)
	}
}

func TestExecutionPort_ChatClientAgent_RunStreamingAsyncInvokesAIContextProviderWhenGetResponseFailsAsync(t *testing.T) {
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "context provider message")})
	failure := errors.New("downstream failure")
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(nil, failure) }
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		ContextProviders: []agent.ContextProvider{p}, Tools: []tool.Tool{stubTool{name: "base function"}},
		RunOptions: []agent.Option{agent.WithInstructions("base instructions")},
	})
	if _, err := a.RunText(t.Context(), "user message", agent.Stream(true)).Collect(); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if len(p.invoking) != 1 || len(p.invoked) != 1 {
		t.Fatalf("lifecycle calls = %d/%d", len(p.invoking), len(p.invoked))
	}
	executionPortMessages(t, p.invoked[0].RequestMessages, "user message", "context provider message")
	if p.invoked[0].ResponseMessages != nil || !errors.Is(p.invoked[0].Err, failure) {
		t.Fatalf("completion = %#v", p.invoked[0])
	}
}

func TestExecutionPort_ChatClientAgent_RunStreamingAsyncInvokesMultipleAIContextProvidersAsync(t *testing.T) {
	first := executionPortProvider("first", []*message.Message{executionPortText(message.RoleSystem, "provider1 context")}, agent.WithInstructions("provider1 instructions"))
	second := executionPortProvider("second", []*message.Message{executionPortText(message.RoleSystem, "provider2 context")}, agent.WithInstructions("provider2 instructions"))
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		executionPortMessages(t, messages, "user message", "provider1 context", "provider2 context")
		got := strings.Join(slices.Collect(agent.AllOptions(options, agent.WithInstructions)), "\n")
		if got != "base instructions\nprovider1 instructions\nprovider2 instructions" {
			t.Fatalf("instructions = %q", got)
		}
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		ContextProviders: []agent.ContextProvider{first, second},
		RunOptions:       []agent.Option{agent.WithInstructions("base instructions")},
	})
	executionPortConsume(t, a.RunText(t.Context(), "user message", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)))
	for _, p := range []*executionPortContext{first, second} {
		if len(p.invoking) != 1 || len(p.invoked) != 1 || p.invoked[0].Err != nil {
			t.Fatalf("provider lifecycle = %#v", p)
		}
	}
}

func TestExecutionPort_MessageAIContextProviderAgent_RunAsync_SequentialProviders_EachReceivesPreviousOutputAsync(t *testing.T) {
	first := executionPortProvider("first", []*message.Message{executionPortText(message.RoleSystem, "From provider 1")})
	var received []*message.Message
	second := agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "second",
		Provide: func(_ context.Context, in agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			received = slices.Clone(in.Messages)
			return []*message.Message{executionPortText(message.RoleSystem, "From provider 2")}, nil, nil
		},
	})
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Response")}, agent.Config{
		Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(first), agent.ContextProviderMiddleware(second)},
	})
	if _, err := a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, received, "Hello")
}

func TestExecutionPort_MessageAIContextProviderAgent_RunAsync_OnSuccess_InvokedAsyncCalledOnAllProvidersAsync(t *testing.T) {
	first, second := &executionPortContext{}, &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Response")}, agent.Config{
		Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(first), agent.ContextProviderMiddleware(second)},
	})
	if _, err := a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*executionPortContext{first, second} {
		if len(p.invoked) != 1 || p.invoked[0].Err != nil {
			t.Fatalf("completion notifications = %#v", p.invoked)
		}
	}
}

func TestExecutionPort_MessageAIContextProviderAgent_RunAsync_OnSuccess_InvokedContextContainsResponseMessagesAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Response text")}, agent.Config{
		Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)},
	})
	if _, err := a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(p.invoked) != 1 {
		t.Fatalf("completion notifications = %d", len(p.invoked))
	}
	executionPortMessages(t, p.invoked[0].ResponseMessages, "Response text")
}

func TestExecutionPort_MessageAIContextProviderAgent_RunStreamingAsync_SingleProvider_EnrichesMessagesAndStreamsAsync(t *testing.T) {
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "Extra context")})
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("Part1", "Part2")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}})
	updates := executionPortConsume(t, a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)))
	if len(updates) != 2 {
		t.Fatalf("updates = %d", len(updates))
	}
	executionPortMessages(t, captured, "Hello", "Extra context")
}

func TestExecutionPort_MessageAIContextProviderAgent_RunStreamingAsync_OnSuccess_InvokedAsyncCalledAfterAllUpdatesAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Response")}, agent.Config{
		Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)},
	})
	updates := 0
	for _, err := range a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		updates++
		if len(p.invoked) != 0 {
			t.Fatal("provider was notified before updates were consumed")
		}
	}
	if updates != 1 || len(p.invoked) != 1 || p.invoked[0].Err != nil {
		t.Fatalf("updates = %d, completion = %#v", updates, p.invoked)
	}
}

func TestExecutionPort_MessageAIContextProviderAgent_RunStreamingAsync_OnSuccess_InvokedContextContainsAccumulatedResponseAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Hello ", "World")}, agent.Config{
		Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)},
	})
	executionPortConsume(t, a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)))
	if len(p.invoked) != 1 {
		t.Fatalf("completion notifications = %d", len(p.invoked))
	}
	executionPortMessages(t, p.invoked[0].ResponseMessages, "Hello World")
}

func TestExecutionPort_MessageAIContextProviderAgent_RunStreamingAsync_OnFailure_InvokedAsyncCalledWithExceptionAsync(t *testing.T) {
	failure := errors.New("Stream failed")
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(nil, failure) }
	}
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}})
	_, err := a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)).Collect()
	if !errors.Is(err, failure) || len(p.invoked) != 1 || p.invoked[0].Err != failure {
		t.Fatalf("error = %v, completion = %#v", err, p.invoked)
	}
}

func TestExecutionPort_MessageAIContextProviderAgent_RunStreamingAsync_MultipleProviders_CalledInSequenceAsync(t *testing.T) {
	first := executionPortProvider("first", []*message.Message{executionPortText(message.RoleSystem, "From provider 1")})
	second := executionPortProvider("second", []*message.Message{executionPortText(message.RoleSystem, "From provider 2")})
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("Response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(first), agent.ContextProviderMiddleware(second)},
	})
	executionPortConsume(t, a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)))
	executionPortMessages(t, captured, "Hello", "From provider 1", "From provider 2")
}

func TestExecutionPort_MessageAIContextProviderAgent_UseExtension_CreatesWorkingPipelineAsync(t *testing.T) {
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "Pipeline context")})
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("Response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}})
	if _, err := a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, captured, "Hello", "Pipeline context")
}

func TestExecutionPort_MessageAIContextProviderAgent_UseExtension_MultipleProviders_AllAppliedAsync(t *testing.T) {
	first := executionPortProvider("first", []*message.Message{executionPortText(message.RoleSystem, "P1")})
	second := executionPortProvider("second", []*message.Message{executionPortText(message.RoleSystem, "P2")})
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("Response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(first), agent.ContextProviderMiddleware(second)},
	})
	if _, err := a.RunText(t.Context(), "Hello", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, captured, "Hello", "P1", "P2")
}

func TestExecutionPort_AIContextProviderChatClient_GetResponseAsync_SingleProvider_EnrichesMessagesAsync(t *testing.T) {
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "Extra context")})
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("Response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	if _, err := a.RunText(t.Context(), "Hello").Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, captured, "Hello", "Extra context")
}

func TestExecutionPort_AIContextProviderChatClient_GetResponseAsync_MultipleProviders_CalledInSequenceAsync(t *testing.T) {
	first := executionPortProvider("first", []*message.Message{executionPortText(message.RoleSystem, "From P1")})
	second := executionPortProvider("second", []*message.Message{executionPortText(message.RoleSystem, "From P2")})
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("Response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{
		agent.ContextProviderMiddleware(first), agent.ContextProviderMiddleware(second),
	}}, agent.Config{})
	if _, err := a.RunText(t.Context(), "Hello").Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, captured, "Hello", "From P1", "From P2")
}

func TestExecutionPort_AIContextProviderChatClient_GetResponseAsync_Provider_EnrichesToolsAndInstructionsAsync(t *testing.T) {
	tl := stubTool{name: "context tool"}
	p := executionPortProvider("context", nil, agent.WithInstructions("Extra instructions"), agent.WithTool(tl))
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		if got := slices.Collect(agent.AllOptions(options, agent.WithInstructions)); !slices.Equal(got, []string{"Extra instructions"}) {
			t.Fatalf("instructions = %q", got)
		}
		if got := slices.Collect(agent.AllOptions(options, agent.WithTool)); !slices.Equal(got, []tool.Tool{tl}) {
			t.Fatalf("tools = %v", got)
		}
		return executionPortRun("Response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	if _, err := a.RunText(t.Context(), "Hello").Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestExecutionPort_AIContextProviderChatClient_GetResponseAsync_OnSuccess_InvokedAsyncCalledAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Response"), Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	if _, err := a.RunText(t.Context(), "Hello").Collect(); err != nil {
		t.Fatal(err)
	}
	if len(p.invoked) != 1 || p.invoked[0].Err != nil || p.invoked[0].ResponseMessages == nil {
		t.Fatalf("completion = %#v", p.invoked)
	}
}

func TestExecutionPort_AIContextProviderChatClient_GetResponseAsync_OnFailure_InvokedAsyncCalledWithExceptionAsync(t *testing.T) {
	failure := errors.New("Chat failed")
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(nil, failure) }
	}
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	_, err := a.RunText(t.Context(), "Hello").Collect()
	if !errors.Is(err, failure) || len(p.invoked) != 1 || p.invoked[0].Err != failure {
		t.Fatalf("error = %v, completion = %#v", err, p.invoked)
	}
}

func TestExecutionPort_AIContextProviderChatClient_GetStreamingResponseAsync_SingleProvider_EnrichesAndStreamsAsync(t *testing.T) {
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "Extra context")})
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("Part1", "Part2")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	updates := executionPortConsume(t, a.RunText(t.Context(), "Hello", agent.Stream(true)))
	if len(updates) != 2 {
		t.Fatalf("updates = %d", len(updates))
	}
	executionPortMessages(t, captured, "Hello", "Extra context")
}

func TestExecutionPort_AIContextProviderChatClient_GetStreamingResponseAsync_OnSuccess_InvokedAsyncCalledAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Response"), Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	executionPortConsume(t, a.RunText(t.Context(), "Hello", agent.Stream(true)))
	if len(p.invoked) != 1 || p.invoked[0].Err != nil {
		t.Fatalf("completion = %#v", p.invoked)
	}
}

func TestExecutionPort_AIContextProviderChatClient_GetResponseAsync_SharedOptions_ProviderToolsDoNotAccumulateAcrossCallsAsync(t *testing.T) {
	p := executionPortProvider("context", nil, agent.WithTool(stubTool{name: "provider"}))
	var counts []int
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		counts = append(counts, len(slices.Collect(agent.AllOptions(options, agent.WithTool))))
		return executionPortRun("Response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	shared := []agent.Option{agent.WithTool(stubTool{name: "baseline"})}
	for range 3 {
		if _, err := a.RunText(t.Context(), "Hello", shared...).Collect(); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(counts, []int{2, 2, 2}) {
		t.Fatalf("tool counts = %v", counts)
	}
}

func TestExecutionPort_AIContextProviderChatClient_GetResponseAsync_SharedOptions_OriginalToolsNotMutatedAsync(t *testing.T) {
	p := executionPortProvider("context", nil, agent.WithTool(stubTool{name: "provider"}))
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Response"), Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	baseline := &stubTool{name: "baseline"}
	shared := make([]agent.Option, 1, 4)
	shared[0] = agent.WithTool(baseline)
	original := shared
	if _, err := a.RunText(t.Context(), "Hello", shared...).Collect(); err != nil {
		t.Fatal(err)
	}
	if &original[0] != &shared[0] || len(shared) != 1 {
		t.Fatal("original option slice changed")
	}
	if got := slices.Collect(agent.AllOptions(shared, agent.WithTool)); !slices.Equal(got, []tool.Tool{baseline}) {
		t.Fatalf("original tools = %v", got)
	}
}

func TestExecutionPort_AIContextProviderChatClient_UseExtension_CreatesWorkingPipelineAsync(t *testing.T) {
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "Pipeline context")})
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("Response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{agent.ContextProviderMiddleware(p)}}, agent.Config{})
	if _, err := a.RunText(t.Context(), "Hello").Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, captured, "Hello", "Pipeline context")
}

func TestExecutionPort_BackgroundResponses_RunAsync_WhenContinuationTokenProvided_SkipsSessionMessagePopulationAsync(t *testing.T) {
	history := &executionPortHistory{HistoryProvider: agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			return []*message.Message{message.NewText("Message from chat history provider")}, nil
		},
	})}
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "Message from AI context")}, agent.WithInstructions("context instructions"))
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		executionPortMessages(t, messages)
		return executionPortRun("continued response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{HistoryProvider: history, ContextProviders: []agent.ContextProvider{p}})
	if _, err := a.Run(t.Context(), nil, agent.WithSession(executionPortSession(t, a)), agent.WithContinuationToken(agenttest.NewContinuationToken(t, "inner"))).Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(history.invoking) != 0 || len(p.invoking) != 0 {
		t.Fatalf("provider/history/context calls = %d/%d/%d", calls, len(history.invoking), len(p.invoking))
	}
}

func TestExecutionPort_BackgroundResponses_RunStreamingAsync_WhenContinuationTokenProvided_SkipsSessionMessagePopulationAsync(t *testing.T) {
	history := &executionPortHistory{HistoryProvider: agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			return []*message.Message{message.NewText("Message from chat history provider")}, nil
		},
	})}
	p := executionPortProvider("context", []*message.Message{executionPortText(message.RoleSystem, "Message from AI context")}, agent.WithInstructions("context instructions"))
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		executionPortMessages(t, messages)
		return executionPortRun("continued response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{HistoryProvider: history, ContextProviders: []agent.ContextProvider{p}})
	token := agenttest.EncodeContinuationToken(t, agenttest.ContinuationToken{
		Type: agenttest.ContinuationTokenType, InnerToken: "inner", InputMessages: []*message.Message{{}},
	})
	executionPortConsume(t, a.Run(t.Context(), nil, agent.WithSession(executionPortSession(t, a)), agent.WithContinuationToken(token), agent.Stream(true)))
	if calls != 1 || len(history.invoking) != 0 || len(p.invoking) != 0 {
		t.Fatalf("provider/history/context calls = %d/%d/%d", calls, len(history.invoking), len(p.invoking))
	}
}

func TestExecutionPort_BackgroundResponses_RunStreamingAsync_WhenMessagesProvidedWithContinuationToken_ThrowsInvalidOperationExceptionAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		return executionPortRun()(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{})
	_, err := a.RunText(t.Context(), "test message", agent.WithContinuationToken(agenttest.NewContinuationToken(t, "inner")), agent.Stream(true)).Collect()
	if err == nil || !strings.Contains(err.Error(), "messages are not allowed") || calls != 0 {
		t.Fatalf("error = %v, provider calls = %d", err, calls)
	}
}

func executionPortConversationRun(id string, parts ...string) agent.RunFunc {
	return func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, text := range parts {
				update := executionPortUpdate(text)
				update.ConversationID = new(id)
				if !yield(update, nil) {
					return
				}
			}
		}
	}
}

func TestExecutionPort_BackgroundResponses_RunStreamingAsync_WhenInputMessagesPresentInContinuationToken_ResumesStreamingAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		return executionPortConversationRun("conversation-id", "continuation")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{})
	session := executionPortSession(t, a)
	session.SetServiceID("conversation-id")
	token := agenttest.EncodeContinuationToken(t, agenttest.ContinuationToken{
		Type: agenttest.ContinuationTokenType, InnerToken: "inner",
		InputMessages: []*message.Message{message.NewText("previous message")},
	})
	updates := executionPortConsume(t, a.Run(t.Context(), nil, agent.WithSession(session), agent.WithContinuationToken(token), agent.Stream(true)))
	if calls != 1 || len(updates) != 1 {
		t.Fatalf("provider calls = %d, updates = %d", calls, len(updates))
	}
}

func TestExecutionPort_BackgroundResponses_RunStreamingAsync_WhenResponseUpdatesPresentInContinuationToken_ResumesStreamingAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		return executionPortConversationRun("conversation-id", "continuation")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{})
	session := executionPortSession(t, a)
	session.SetServiceID("conversation-id")
	token := agenttest.EncodeContinuationToken(t, agenttest.ContinuationToken{
		Type: agenttest.ContinuationTokenType, InnerToken: "inner",
		ResponseUpdates: []*agent.ResponseUpdate{executionPortUpdate("previous update")},
	})
	updates := executionPortConsume(t, a.Run(t.Context(), nil, agent.WithSession(session), agent.WithContinuationToken(token), agent.Stream(true)))
	if calls != 1 || len(updates) != 1 {
		t.Fatalf("provider calls = %d, updates = %d", calls, len(updates))
	}
}

func TestExecutionPort_BackgroundResponses_RunStreamingAsync_WhenResumingStreaming_UsesInputMessagesFromInitialRunForContextProviderAndChatHistoryProviderAsync(t *testing.T) {
	history, p := &executionPortHistory{}, &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun()}, agent.Config{HistoryProvider: history, ContextProviders: []agent.ContextProvider{p}})
	token := agenttest.EncodeContinuationToken(t, agenttest.ContinuationToken{
		Type: agenttest.ContinuationTokenType, InnerToken: "inner",
		InputMessages: []*message.Message{message.NewText("Tell me a story")},
	})
	executionPortConsume(t, a.Run(t.Context(), nil, agent.WithSession(executionPortSession(t, a)), agent.WithContinuationToken(token), agent.Stream(true)))
	if len(history.invoked) != 1 || len(p.invoked) != 1 {
		t.Fatalf("history/context completions = %d/%d", len(history.invoked), len(p.invoked))
	}
	executionPortMessages(t, history.invoked[0].RequestMessages, "Tell me a story")
	executionPortMessages(t, p.invoked[0].RequestMessages, "Tell me a story")
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_Throws_WhenSpecifyingTwoDifferentConversationIdsAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		return executionPortRun()(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	session := executionPortSession(t, a)
	session.SetServiceID("ThreadId")
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session), agent.WithServiceID("ConvId")).Collect(); err == nil || calls != 0 {
		t.Fatalf("error = %v, provider calls = %d", err, calls)
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_Throws_ForMissingConversationIdWithConversationIdSessionAsync(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("response")}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	session := executionPortSession(t, a)
	session.SetServiceID("ConvId")
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err == nil {
		t.Fatal("run succeeded without returning the session's conversation ID")
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_UsesDefaultInMemoryChatHistoryProvider_WhenNoConversationIdReturnedByChatClientAsync(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("response")}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	session := executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, history, "test", "response")
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_DoesNotThrow_WhenNoChatHistoryProviderInOptionsAndConversationIdReturnedAsync(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortConversationRun("ConvId", "response")}, agent.Config{
		RunOptions: []agent.Option{agent.WithInstructions("test instructions")},
	})
	session := executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if session.ServiceID() != "ConvId" {
		t.Fatalf("ServiceID = %q", session.ServiceID())
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_DoesNotUseDefaultInMemoryChatHistoryProvider_WhenConversationIdReturnedAsync(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortConversationRun("ConvId", "response")}, agent.Config{
		RunOptions: []agent.Option{agent.WithInstructions("test instructions")},
	})
	session := executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if session.ServiceID() != "ConvId" || len(history) != 0 {
		t.Fatalf("ServiceID = %q, local history = %v", session.ServiceID(), history)
	}
}

func TestExecutionPort_ChatHistoryManagement_RunStreamingAsync_DoesNotUseDefaultInMemoryChatHistoryProvider_WhenConversationIdReturnedAsync(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortConversationRun("ConvId", "response")}, agent.Config{
		RunOptions: []agent.Option{agent.WithInstructions("test instructions")},
	})
	session := executionPortSession(t, a)
	executionPortConsume(t, a.RunText(t.Context(), "test", agent.WithSession(session), agent.Stream(true)))
	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if session.ServiceID() != "ConvId" || len(history) != 0 {
		t.Fatalf("ServiceID = %q, local history = %v", session.ServiceID(), history)
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_MultiTurnServiceStoredHistory_DoesNotPopulateDefaultInMemoryProviderAsync(t *testing.T) {
	var requests [][]*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		requests = append(requests, slices.Clone(messages))
		return executionPortConversationRun("ConvId", "response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	session := executionPortSession(t, a)
	for _, text := range []string{"first", "second"} {
		if _, err := a.RunText(t.Context(), text, agent.WithSession(session)).Collect(); err != nil {
			t.Fatal(err)
		}
	}
	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if session.ServiceID() != "ConvId" || len(history) != 0 || len(requests) != 2 {
		t.Fatalf("ServiceID = %q, history = %v, calls = %d", session.ServiceID(), history, len(requests))
	}
	executionPortMessages(t, requests[1], "second")
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_PerServiceCallPersistence_SimpleResponse_PersistsCorrectHistoryAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		return executionPortRun("Hi there")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, RunOptions: []agent.Option{agent.WithInstructions("Be helpful")},
	})
	session := executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "Hello", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, history, "Hello", "Hi there")
	if calls != 1 || history[0].Role != message.RoleUser || history[1].Role != message.RoleAssistant {
		t.Fatalf("calls = %d, history = %v", calls, history)
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_EndOfRunPersistence_SimpleResponse_PersistsCorrectHistoryAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		return executionPortRun("Hi there")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("Be helpful")}})
	session := executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "Hello", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, history, "Hello", "Hi there")
	if calls != 1 || history[0].Role != message.RoleUser || history[1].Role != message.RoleAssistant {
		t.Fatalf("calls = %d, history = %v", calls, history)
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_ServiceStoredHistory_SetsConversationIdAndCompletesWithoutErrorAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		return executionPortConversationRun("thread-123", "Hi there")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("Be helpful")}})
	session := executionPortSession(t, a)
	response, err := a.RunText(t.Context(), "Hello", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || session.ServiceID() != "thread-123" {
		t.Fatalf("calls = %d, ServiceID = %q", calls, session.ServiceID())
	}
	executionPortMessages(t, response.Messages, "Hi there")
}

type executionPortWeather struct {
	agent     *agent.Agent
	session   *agent.Session
	requests  [][]*message.Message
	calls     int
	toolCalls int
}

func executionPortNewWeather(t *testing.T, cfg agent.Config, approval bool, serviceID, finalText string) *executionPortWeather {
	t.Helper()
	w := &executionPortWeather{}
	fn := functool.MustNew(functool.Config{Name: "GetWeather", Description: "Gets the weather"}, func(context.Context, struct {
		City string `json:"city"`
	},
	) (string, error) {
		w.toolCalls++
		return "Sunny, 22°C", nil
	})
	if approval {
		fn = tool.ApprovalRequiredFunc(fn)
	}
	cfg.Tools = []tool.Tool{fn}
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			w.requests = append(w.requests, slices.Clone(messages))
			w.calls++
			update := executionPortUpdate(finalText)
			update.MessageID = fmt.Sprintf("response-%d", w.calls)
			if w.calls == 1 {
				update.Contents = []message.Content{&message.FunctionCallContent{CallID: "call1", Name: "GetWeather", Arguments: `{"city":"Amsterdam"}`}}
			}
			if serviceID != "" {
				update.ConversationID = new(serviceID)
			}
			yield(update, nil)
		}
	}
	w.agent = agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, cfg)
	w.session = executionPortSession(t, w.agent)
	return w
}

func executionPortOnlyApproval(t *testing.T, response *agent.Response) *message.ToolApprovalRequestContent {
	t.Helper()
	var requests []*message.ToolApprovalRequestContent
	for c := range response.Contents() {
		if request, ok := c.(*message.ToolApprovalRequestContent); ok {
			requests = append(requests, request)
		}
	}
	if len(requests) != 1 {
		t.Fatalf("approval requests = %d, want 1", len(requests))
	}
	return requests[0]
}

func executionPortHistoryTypes(t *testing.T, messages []*message.Message, roles []message.Role, types []reflect.Type) {
	t.Helper()
	if len(messages) != len(roles) || len(types) != len(roles) {
		t.Fatalf("history = %d messages, want %d", len(messages), len(roles))
	}
	for i, m := range messages {
		if m.Role != roles[i] {
			t.Fatalf("history[%d].Role = %q, want %q", i, m.Role, roles[i])
		}
		if types[i] != nil && !slices.ContainsFunc(m.Contents, func(c message.Content) bool { return reflect.TypeOf(c) == types[i] }) {
			t.Fatalf("history[%d] does not contain %v: %v", i, types[i], m.Contents)
		}
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_PerServiceCallPersistence_FunctionCallingLoop_PersistsCorrectHistoryAsync(t *testing.T) {
	w := executionPortNewWeather(t, agent.Config{RequirePerServiceCallHistoryPersistence: true}, false, "", "The weather in Amsterdam is sunny and 22°C.")
	if _, err := w.agent.RunText(t.Context(), "What's the weather?", agent.WithSession(w.session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := w.session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortHistoryTypes(t, history,
		[]message.Role{message.RoleUser, message.RoleAssistant, message.RoleTool, message.RoleAssistant},
		[]reflect.Type{nil, reflect.TypeFor[*message.FunctionCallContent](), reflect.TypeFor[*message.FunctionResultContent](), nil})
	if w.calls != 2 || history[0].String() != "What's the weather?" || !strings.Contains(history[3].String(), "sunny and 22°C") {
		t.Fatalf("calls = %d, history = %v", w.calls, history)
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_EndOfRunPersistence_FunctionCallingLoop_PersistsCorrectHistoryAsync(t *testing.T) {
	w := executionPortNewWeather(t, agent.Config{}, false, "", "The weather in Amsterdam is sunny and 22°C.")
	if _, err := w.agent.RunText(t.Context(), "What's the weather?", agent.WithSession(w.session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := w.session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortHistoryTypes(t, history,
		[]message.Role{message.RoleUser, message.RoleAssistant, message.RoleTool, message.RoleAssistant},
		[]reflect.Type{nil, reflect.TypeFor[*message.FunctionCallContent](), reflect.TypeFor[*message.FunctionResultContent](), nil})
	if w.calls != 2 || history[0].String() != "What's the weather?" || !strings.Contains(history[3].String(), "sunny and 22°C") {
		t.Fatalf("calls = %d, history = %v", w.calls, history)
	}
}

func TestExecutionPort_Approvals_RunAsync_ApprovalRequired_PerServiceCallPersistence_PersistsCorrectHistoryAsync(t *testing.T) {
	w := executionPortNewWeather(t, agent.Config{RequirePerServiceCallHistoryPersistence: true}, true, "", "The weather in Amsterdam is sunny and 22°C.")
	first, err := w.agent.RunText(t.Context(), "What's the weather?", agent.WithSession(w.session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	request := executionPortOnlyApproval(t, first)
	if w.calls != 1 || len(w.requests) != 1 {
		t.Fatalf("first-turn calls = %d", w.calls)
	}
	executionPortMessages(t, w.requests[0], "What's the weather?")
	if w.requests[0][0].Role != message.RoleUser {
		t.Fatal("initial request lost user role")
	}
	if _, err := w.agent.RunMessage(t.Context(), message.New(request.CreateResponse(true, "")), agent.WithSession(w.session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := w.session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortHistoryTypes(t, history,
		[]message.Role{message.RoleUser, message.RoleAssistant, message.RoleTool, message.RoleAssistant},
		[]reflect.Type{nil, reflect.TypeFor[*message.FunctionCallContent](), reflect.TypeFor[*message.FunctionResultContent](), nil})
	if w.calls != 2 || len(w.requests) != 2 || !strings.Contains(history[3].String(), "sunny and 22°C") || history[0].String() != "What's the weather?" {
		t.Fatalf("calls = %d, history = %v", w.calls, history)
	}
	executionPortHistoryTypes(t, w.requests[1],
		[]message.Role{message.RoleUser, message.RoleAssistant, message.RoleTool},
		[]reflect.Type{nil, reflect.TypeFor[*message.FunctionCallContent](), reflect.TypeFor[*message.FunctionResultContent]()})
}

func TestExecutionPort_Approvals_RunAsync_ApprovalRequired_EndOfRunPersistence_PersistsCorrectHistoryAsync(t *testing.T) {
	w := executionPortNewWeather(t, agent.Config{}, true, "", "The weather in Amsterdam is sunny and 22°C.")
	first, err := w.agent.RunText(t.Context(), "What's the weather?", agent.WithSession(w.session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	request := executionPortOnlyApproval(t, first)
	if _, err := w.agent.RunMessage(t.Context(), message.New(request.CreateResponse(true, "")), agent.WithSession(w.session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := w.session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	executionPortHistoryTypes(t, history,
		[]message.Role{message.RoleUser, message.RoleAssistant, message.RoleUser, message.RoleAssistant, message.RoleTool, message.RoleAssistant},
		[]reflect.Type{nil, reflect.TypeFor[*message.ToolApprovalRequestContent](), reflect.TypeFor[*message.ToolApprovalResponseContent](), reflect.TypeFor[*message.FunctionCallContent](), reflect.TypeFor[*message.FunctionResultContent](), nil})
	if w.calls != 2 || history[0].String() != "What's the weather?" || !strings.Contains(history[5].String(), "sunny and 22°C") {
		t.Fatalf("calls = %d, history = %v", w.calls, history)
	}
}

func TestExecutionPort_Approvals_RunAsync_ApprovalRequired_ServiceStoredHistory_CompletesWithoutErrorAsync(t *testing.T) {
	const finalText = "The weather in Amsterdam is sunny and 22°C."
	w := executionPortNewWeather(t, agent.Config{}, true, "thread-456", finalText)
	first, err := w.agent.RunText(t.Context(), "What's the weather?", agent.WithSession(w.session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	request := executionPortOnlyApproval(t, first)
	if w.session.ServiceID() != "thread-456" {
		t.Fatalf("first-turn ServiceID = %q", w.session.ServiceID())
	}
	second, err := w.agent.RunMessage(t.Context(), message.New(request.CreateResponse(true, "")), agent.WithSession(w.session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if w.calls != 2 || w.session.ServiceID() != "thread-456" {
		t.Fatalf("calls = %d, ServiceID = %q", w.calls, w.session.ServiceID())
	}
	if !slices.ContainsFunc(second.Messages, func(m *message.Message) bool { return m.String() == finalText }) {
		t.Fatalf("response does not contain %q: %v", finalText, second.Messages)
	}
}

func TestExecutionPort_Approvals_RunAsync_ApprovalRejected_PersistsRejectionInHistoryAsync(t *testing.T) {
	const finalText = "I'm sorry, I cannot check the weather without your approval."
	w := executionPortNewWeather(t, agent.Config{RequirePerServiceCallHistoryPersistence: true}, true, "", finalText)
	first, err := w.agent.RunText(t.Context(), "What's the weather?", agent.WithSession(w.session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	request := executionPortOnlyApproval(t, first)
	if _, err := w.agent.RunMessage(t.Context(), message.New(request.CreateResponse(false, "User declined")), agent.WithSession(w.session)).Collect(); err != nil {
		t.Fatal(err)
	}
	history, err := w.session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	rejected := func(m *message.Message) bool {
		for _, c := range m.Contents {
			if result, ok := c.(*message.FunctionResultContent); ok && strings.Contains(fmt.Sprint(result.Result), "rejected") {
				return true
			}
		}

		return false
	}
	if len(history) < 3 || !slices.ContainsFunc(history, func(m *message.Message) bool {
		return m.Role == message.RoleUser && m.String() == "What's the weather?"
	}) ||
		!slices.ContainsFunc(history, rejected) || !slices.ContainsFunc(history, func(m *message.Message) bool { return m.Role == message.RoleAssistant && m.String() == finalText }) {
		t.Fatalf("incomplete rejection history: %v", history)
	}
	if len(w.requests) != 2 || !slices.ContainsFunc(w.requests[1], rejected) || w.toolCalls != 0 {
		t.Fatalf("provider calls = %d, tool calls = %d", len(w.requests), w.toolCalls)
	}
}

func executionPortUsage(call int) message.UsageDetails {
	return []message.UsageDetails{
		{InputTokenCount: 2, OutputTokenCount: 3, TotalTokenCount: 5},
		{InputTokenCount: 11, OutputTokenCount: 5, TotalTokenCount: 16},
		{InputTokenCount: 29, OutputTokenCount: 7, TotalTokenCount: 36},
	}[call-1]
}

func executionPortAssertUsage(t *testing.T, got message.UsageDetails, input, output, total int64) {
	t.Helper()
	if got.InputTokenCount != input || got.OutputTokenCount != output || got.TotalTokenCount != total {
		t.Fatalf("usage = %#v, want input/output/total = %d/%d/%d", got, input, output, total)
	}
}

func TestExecutionPort_MessageInjectingChatClient_RunAsync_IncludesInjectedMessages_WhenEnqueuedBeforeCallAsync(t *testing.T) {
	injector := &agent.MessageInjector{}
	history := &executionPortHistory{}
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = slices.Clone(messages)
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		MessageInjector: injector, HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true,
	})
	session := executionPortSession(t, a)
	if err := injector.EnqueueMessages(session, message.NewText("injected message")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunText(t.Context(), "original", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortMessages(t, captured, "original", "injected message")
}

func TestExecutionPort_MessageInjectingChatClient_RunAsync_LoopsInternally_WhenNoActionableFCCButPendingMessagesAsync(t *testing.T) {
	injector := &agent.MessageInjector{}
	var session *agent.Session
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		if calls == 1 {
			if err := injector.EnqueueMessages(session, message.NewText("injected during first call")); err != nil {
				t.Fatal(err)
			}
		}
		return executionPortRun(fmt.Sprintf("response %d", calls))(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		MessageInjector: injector, HistoryProvider: &executionPortHistory{}, RequirePerServiceCallHistoryPersistence: true,
	})
	session = executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "original", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("provider calls = %d", calls)
	}
}

func TestExecutionPort_MessageInjectingChatClient_RunAsync_DoesNotLoopInternally_WhenActionableFCCPresentAsync(t *testing.T) {
	injector := &agent.MessageInjector{}
	var session *agent.Session
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		if calls == 1 {
			if err := injector.EnqueueMessages(session, message.NewText("injected")); err != nil {
				t.Fatal(err)
			}
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{Role: message.RoleAssistant, MessageID: "call", Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call1", Name: "myTool", Arguments: "{}"},
				}}, nil)
			}
		}
		return executionPortRun("final")(ctx, messages, options...)
	}
	fn := functool.MustNew(functool.Config{Name: "myTool", Description: "A test tool"}, func(context.Context, struct{}) (string, error) { return "tool result", nil })
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
		MessageInjector: injector, HistoryProvider: &executionPortHistory{}, RequirePerServiceCallHistoryPersistence: true,
		Tools: []tool.Tool{fn},
	})
	session = executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "original", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("provider calls = %d", calls)
	}
}

func TestExecutionPort_MessageInjectingChatClient_RunAsync_ActionableFCCOnLaterIteration_AggregatesUsageAcrossInjectedLoopExitAsync(t *testing.T) {
	injector := &agent.MessageInjector{}
	var session *agent.Session
	calls := 0
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			calls++
			update := executionPortUpdate("final")
			update.MessageID = fmt.Sprint(calls)
			switch calls {
			case 1:
				if err := injector.EnqueueMessages(session, message.NewText("injected")); err != nil {
					t.Fatal(err)
				}
				update.Contents = []message.Content{&message.TextContent{Text: "queued"}}
			case 2:
				update.Contents = []message.Content{&message.FunctionCallContent{CallID: "call1", Name: "myTool", Arguments: "{}"}}
			}
			update.Contents = append(update.Contents, &message.UsageContent{Details: executionPortUsage(calls)})
			yield(update, nil)
		}
	}
	fn := functool.MustNew(functool.Config{Name: "myTool", Description: "A test tool"}, func(context.Context, struct{}) (string, error) { return "tool result", nil })
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
		MessageInjector: injector, RequirePerServiceCallHistoryPersistence: true, Tools: []tool.Tool{fn},
	})
	session = executionPortSession(t, a)
	response, err := a.RunText(t.Context(), "original", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || response.String() != "final" {
		t.Fatalf("provider calls = %d, response = %q", calls, response.String())
	}
	executionPortAssertUsage(t, response.Usage(), 42, 15, 57)
}

func TestExecutionPort_MessageInjectingChatClient_RunAsync_LoopsInternally_WhenOnlyInformationalOnlyFCCAndPendingMessagesAsync(t *testing.T) {
	injector := &agent.MessageInjector{}
	var session *agent.Session
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		if calls == 1 {
			if err := injector.EnqueueMessages(session, message.NewText("injected")); err != nil {
				t.Fatal(err)
			}
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call1", Name: "myTool", Arguments: "{}", InformationalOnly: true},
				}}, nil)
			}
		}
		return executionPortRun("final")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		MessageInjector: injector, HistoryProvider: &executionPortHistory{}, RequirePerServiceCallHistoryPersistence: true,
	})
	session = executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "original", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("provider calls = %d", calls)
	}
}

func TestExecutionPort_MessageInjectingChatClient_RunAsync_DeliversInjectedMessages_AfterSessionSerializationRoundTripAsync(t *testing.T) {
	injector := &agent.MessageInjector{}
	var session *agent.Session
	secondRun := false
	var captured []*message.Message
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		if !secondRun {
			if err := injector.EnqueueMessages(session, message.NewText("injected before serialization")); err != nil {
				t.Fatal(err)
			}
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call1", Name: "myTool", Arguments: "{}"},
				}}, nil)
			}
		}
		captured = append(captured, messages...)
		return executionPortRun("final response")(ctx, messages, options...)
	}
	fn := functool.MustNew(functool.Config{Name: "myTool", Description: "A test tool"}, func(context.Context, struct{}) (string, error) { return "tool result", nil })
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
		MessageInjector: injector, HistoryProvider: &executionPortHistory{}, RequirePerServiceCallHistoryPersistence: true, Tools: []tool.Tool{fn},
	})
	session = executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "first run message", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	session, secondRun = &restored, true
	if _, err := a.RunText(t.Context(), "second run message", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(captured, func(m *message.Message) bool { return m.String() == "injected before serialization" }) ||
		!slices.ContainsFunc(captured, func(m *message.Message) bool { return m.String() == "second run message" }) {
		t.Fatalf("restored session did not deliver pending input: %v", captured)
	}
}

func TestExecutionPort_MessageInjectingChatClient_RunAsync_LoopsInternally_AggregatesUsageAcrossIterationsAsync(t *testing.T) {
	injector := &agent.MessageInjector{}
	var session *agent.Session
	calls := 0
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			calls++
			if calls < 3 {
				if err := injector.EnqueueMessages(session, message.NewText(fmt.Sprintf("injected %d", calls))); err != nil {
					t.Fatal(err)
				}
			}
			update := executionPortUpdate(fmt.Sprintf("response %d", calls))
			update.Contents = append(update.Contents, &message.UsageContent{Details: executionPortUsage(calls)})
			yield(update, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{MessageInjector: injector})
	session = executionPortSession(t, a)
	response, err := a.RunText(t.Context(), "original", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("provider calls = %d", calls)
	}
	executionPortAssertUsage(t, response.Usage(), 42, 15, 57)
}

func TestExecutionPort_MessageInjectingChatClient_RunStreamingAsync_LoopsInternally_SurfacesUsageFromEveryIterationAsync(t *testing.T) {
	injector := &agent.MessageInjector{}
	var session *agent.Session
	calls := 0
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			calls++
			if calls < 3 {
				if err := injector.EnqueueMessages(session, message.NewText(fmt.Sprintf("injected %d", calls))); err != nil {
					t.Fatal(err)
				}
			}
			if !yield(executionPortUpdate(fmt.Sprintf("response %d", calls)), nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.UsageContent{Details: executionPortUsage(calls)}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{MessageInjector: injector})
	session = executionPortSession(t, a)
	var response agent.Response
	for update, err := range a.RunText(t.Context(), "original", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		response.Update(update)
	}
	response.Coalesce()
	if calls != 3 {
		t.Fatalf("provider calls = %d", calls)
	}
	executionPortAssertUsage(t, response.Usage(), 42, 15, 57)
}

func executionPortFailure(failure error) agent.RunFunc {
	return func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(nil, failure) }
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_PersistsMessagesPerServiceCall_ByDefaultAsync(t *testing.T) {
	history := &executionPortHistory{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("response")}, agent.Config{
		HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true,
	})
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(history.invoked) != 1 {
		t.Fatalf("history completions = %d", len(history.invoked))
	}
	executionPortMessages(t, history.invoked[0].RequestMessages, "test")
	executionPortMessages(t, history.invoked[0].ResponseMessages, "response")
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_PersistsMessagesAtEndOfRun_WhenOptionEnabledAsync(t *testing.T) {
	history := &executionPortHistory{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("response")}, agent.Config{
		HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true,
	})
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(history.invoked) != 1 {
		t.Fatalf("history completions = %d", len(history.invoked))
	}
	executionPortMessages(t, history.invoked[0].RequestMessages, "test")
	executionPortMessages(t, history.invoked[0].ResponseMessages, "response")
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_NotifiesProviderOfFailure_WhenPerServiceCallPersistenceActiveAsync(t *testing.T) {
	failure := errors.New("Service failed")
	history := &executionPortHistory{}
	a := agent.New(agent.ProviderConfig{Run: executionPortFailure(failure)}, agent.Config{
		HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true,
	})
	_, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect()
	if !errors.Is(err, failure) || len(history.invoked) != 1 || history.invoked[0].Err != failure {
		t.Fatalf("error = %v, history completions = %#v", err, history.invoked)
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_PersistsPerServiceCall_DuringFunctionInvocationLoopAsync(t *testing.T) {
	history := &executionPortHistory{}
	fn := functool.MustNew(functool.Config{Name: "myTool", Description: "A test tool"}, func(context.Context, struct{}) (string, error) { return "tool result", nil })
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().AddFunctionCall("call1", "myTool", "{}").NewTurn().AddText("final response").Build()}
	a := agent.New(agent.ProviderConfig{Run: runner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
		HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true, Tools: []tool.Tool{fn},
	})
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(history.invoked) != 2 {
		t.Fatalf("history completions = %d", len(history.invoked))
	}
	executionPortMessages(t, history.invoked[0].RequestMessages, "test")
	if !slices.ContainsFunc(history.invoked[0].ResponseMessages, func(m *message.Message) bool {
		return slices.ContainsFunc(m.Contents, func(c message.Content) bool { _, ok := c.(*message.FunctionCallContent); return ok })
	}) {
		t.Fatal("first notification omitted the function call")
	}
	if slices.ContainsFunc(history.invoked[1].RequestMessages, func(m *message.Message) bool { return m.String() == "test" }) {
		t.Fatal("second notification repeated the already persisted user message")
	}
	executionPortMessages(t, history.invoked[1].ResponseMessages, "final response")
}

func TestExecutionPort_PerServiceCallHistory_RunStreamingAsync_PersistsMessagesPerServiceCall_ByDefaultAsync(t *testing.T) {
	history := &executionPortHistory{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("streaming ", "response")}, agent.Config{
		HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true,
	})
	executionPortConsume(t, a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)))
	if len(history.invoked) != 1 {
		t.Fatalf("history completions = %d", len(history.invoked))
	}
	executionPortMessages(t, history.invoked[0].RequestMessages, "test")
	executionPortMessages(t, history.invoked[0].ResponseMessages, "streaming response")
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_NotifiesAIContextProviders_ByDefaultAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("response")}, agent.Config{
		ContextProviders: []agent.ContextProvider{p}, RequirePerServiceCallHistoryPersistence: true,
	})
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(p.invoked) != 1 {
		t.Fatalf("context completions = %d", len(p.invoked))
	}
	executionPortMessages(t, p.invoked[0].ResponseMessages, "response")
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_NotifiesAIContextProvidersOfFailure_ByDefaultAsync(t *testing.T) {
	failure := errors.New("Service failed")
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortFailure(failure)}, agent.Config{
		ContextProviders: []agent.ContextProvider{p}, RequirePerServiceCallHistoryPersistence: true,
	})
	_, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect()
	if !errors.Is(err, failure) || len(p.invoked) != 1 || p.invoked[0].Err != failure {
		t.Fatalf("error = %v, context completions = %#v", err, p.invoked)
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_NotifiesBothProviders_ByDefaultAsync(t *testing.T) {
	history, p := &executionPortHistory{}, &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("response")}, agent.Config{
		HistoryProvider: history, ContextProviders: []agent.ContextProvider{p}, RequirePerServiceCallHistoryPersistence: true,
	})
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(history.invoked) != 1 || len(p.invoked) != 1 {
		t.Fatalf("history/context completions = %d/%d", len(history.invoked), len(p.invoked))
	}
	executionPortMessages(t, history.invoked[0].ResponseMessages, "response")
	executionPortMessages(t, p.invoked[0].ResponseMessages, "response")
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_DeduplicatesRequestMessages_OnFailureDuringFicLoopAsync(t *testing.T) {
	history := &executionPortHistory{}
	fn := functool.MustNew(functool.Config{Name: "myTool", Description: "A test tool"}, func(context.Context, struct{}) (string, error) { return "tool result", nil })
	failure := errors.New("Service failure on second call")
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().AddFunctionCall("call1", "myTool", "{}").NewTurn().AddError(failure).Build()}
	a := agent.New(agent.ProviderConfig{Run: runner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
		HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true, Tools: []tool.Tool{fn},
	})
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect(); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if len(history.invoked) != 2 || history.invoked[0].Err != nil || history.invoked[1].Err != failure {
		t.Fatalf("history completions = %#v", history.invoked)
	}
	executionPortMessages(t, history.invoked[0].RequestMessages, "test")
	if slices.ContainsFunc(history.invoked[1].RequestMessages, func(m *message.Message) bool { return m.String() == "test" }) {
		t.Fatal("failure notification repeated the already persisted user message")
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_UpdatesSessionConversationId_WhenServiceReturnsOneAsync(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortConversationRun("conv-123", "response")}, agent.Config{})
	session := executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if session.ServiceID() != "conv-123" {
		t.Fatalf("ServiceID = %q", session.ServiceID())
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_PassesUsageThroughUnchangedAsync(t *testing.T) {
	calls := 0
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			calls++
			update := executionPortUpdate("response")
			update.Contents = append(update.Contents, &message.UsageContent{Details: message.UsageDetails{InputTokenCount: 21, OutputTokenCount: 9, TotalTokenCount: 30}})
			yield(update, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RequirePerServiceCallHistoryPersistence: true})
	response, err := a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a))).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d", calls)
	}
	executionPortAssertUsage(t, response.Usage(), 21, 9, 30)
}

func TestExecutionPort_PerServiceCallHistory_RunStreamingAsync_PassesUsageThroughUnchangedAsync(t *testing.T) {
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(executionPortUpdate("response"), nil) {
				return
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{
				&message.UsageContent{Details: message.UsageDetails{InputTokenCount: 21, OutputTokenCount: 9, TotalTokenCount: 30}},
			}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RequirePerServiceCallHistoryPersistence: true})
	var response agent.Response
	for update, err := range a.RunText(t.Context(), "test", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		response.Update(update)
	}
	response.Coalesce()
	executionPortAssertUsage(t, response.Usage(), 21, 9, 30)
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_SetsAndStripsSentinelConversationId_WhenPerServiceCallPersistenceActiveAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		if id, _ := agent.GetOption(options, agent.WithServiceID); id != "" {
			t.Fatalf("provider received local history ID %q", id)
		}
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, RunOptions: []agent.Option{agent.WithInstructions("test")},
	})
	if _, err := a.RunText(t.Context(), "test").Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d", calls)
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_DoesNotSetSentinel_WhenEndOfRunPersistenceEnabledAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		if id, _ := agent.GetOption(options, agent.WithServiceID); id != "" {
			t.Fatalf("provider received local history ID %q", id)
		}
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, RunOptions: []agent.Option{agent.WithInstructions("test")},
	})
	if _, err := a.RunText(t.Context(), "test").Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d", calls)
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_DoesNotSetSentinel_WhenRealConversationIdExistsAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		if id, _ := agent.GetOption(options, agent.WithServiceID); id != "real-conv-123" {
			t.Fatalf("provider ServiceID = %q", id)
		}
		return executionPortConversationRun("real-conv-123", "response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RequirePerServiceCallHistoryPersistence: true})
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("real-conv-123"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d", calls)
	}
}

func TestExecutionPort_PerServiceCallHistory_RunStreamingAsync_SetsAndStripsSentinelConversationId_WhenPerServiceCallPersistenceActiveAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		if id, _ := agent.GetOption(options, agent.WithServiceID); id != "" {
			t.Fatalf("provider received local history ID %q", id)
		}
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, RunOptions: []agent.Option{agent.WithInstructions("test")},
	})
	executionPortConsume(t, a.RunText(t.Context(), "test", agent.Stream(true)))
	if calls != 1 {
		t.Fatalf("provider calls = %d", calls)
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_SetsSentinelOnSession_WhenRequirePerServiceCallChatHistoryPersistenceActiveAsync(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("response")}, agent.Config{RequirePerServiceCallHistoryPersistence: true})
	session := executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if session.ServiceID() != "_agent_local_chat_history" {
		t.Fatalf("ServiceID = %q, want local-history sentinel", session.ServiceID())
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_NotifiesProvidersAndUpdatesSession_WhenRequestHasRealConversationIdAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortConversationRun("real-conv-response", "response")}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, ContextProviders: []agent.ContextProvider{p},
	})
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("real-conv-request"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(p.invoked) != 1 || session.ServiceID() != "real-conv-response" {
		t.Fatalf("completions = %d, ServiceID = %q", len(p.invoked), session.ServiceID())
	}
	executionPortMessages(t, p.invoked[0].RequestMessages, "test")
	executionPortMessages(t, p.invoked[0].ResponseMessages, "response")
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_NotifiesProvidersOfFailure_WhenRequestHasRealConversationIdAsync(t *testing.T) {
	p := &executionPortContext{}
	failure := errors.New("Service error")
	a := agent.New(agent.ProviderConfig{Run: executionPortFailure(failure)}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, ContextProviders: []agent.ContextProvider{p},
	})
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("real-conv-failure"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if len(p.invoked) != 1 || p.invoked[0].Err != failure {
		t.Fatalf("failure notifications = %#v", p.invoked)
	}
}

func TestExecutionPort_PerServiceCallHistory_RunStreamingAsync_NotifiesProvidersAndUpdatesSession_WhenRequestHasRealConversationIdAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortConversationRun("service-conv-streaming", "streamed")}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, ContextProviders: []agent.ContextProvider{p},
	})
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("real-conv-streaming"))
	if err != nil {
		t.Fatal(err)
	}
	executionPortConsume(t, a.RunText(t.Context(), "test", agent.WithSession(session), agent.Stream(true)))
	if len(p.invoked) != 1 || session.ServiceID() != "service-conv-streaming" {
		t.Fatalf("completions = %d, ServiceID = %q", len(p.invoked), session.ServiceID())
	}
}

func TestExecutionPort_PerServiceCallHistory_RunAsync_NotifiesProvidersAndUpdatesSession_WhenServiceReturnsUnexpectedConversationIdAsync(t *testing.T) {
	p := &executionPortContext{}
	a := agent.New(agent.ProviderConfig{Run: executionPortConversationRun("unexpected-conv-id", "response")}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, ContextProviders: []agent.ContextProvider{p},
	})
	session := executionPortSession(t, a)
	if _, err := a.RunText(t.Context(), "test", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(p.invoked) != 1 || session.ServiceID() != "unexpected-conv-id" {
		t.Fatalf("completions = %d, ServiceID = %q", len(p.invoked), session.ServiceID())
	}
	executionPortMessages(t, p.invoked[0].ResponseMessages, "response")
}

func TestExecutionPort_PerServiceCallHistory_RunStreamingAsync_NotifiesProvidersAndUpdatesSession_WhenServiceReturnsUnexpectedConversationIdAsync(t *testing.T) {
	p := &executionPortContext{}
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(executionPortUpdate("part1"), nil) {
				return
			}
			yield(&agent.ResponseUpdate{ConversationID: new("unexpected-stream-conv"), MessageID: "response", Contents: []message.Content{&message.TextContent{Text: "part2"}}}, nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{
		RequirePerServiceCallHistoryPersistence: true, ContextProviders: []agent.ContextProvider{p},
	})
	session := executionPortSession(t, a)
	executionPortConsume(t, a.RunText(t.Context(), "test", agent.WithSession(session), agent.Stream(true)))
	if len(p.invoked) != 1 || session.ServiceID() != "unexpected-stream-conv" {
		t.Fatalf("completions = %d, ServiceID = %q", len(p.invoked), session.ServiceID())
	}
}

func TestExecutionPort_PerServiceCallHistory_RunStreamingAsync_SkipsSimulation_WhenAllowBackgroundResponsesAsync(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("response")}, agent.Config{RequirePerServiceCallHistoryPersistence: true})
	session := executionPortSession(t, a)
	updates := executionPortConsume(t, a.RunText(t.Context(), "test", agent.WithSession(session), agent.AllowBackgroundResponses(true), agent.Stream(true)))
	if len(updates) == 0 || session.ServiceID() == "_agent_local_chat_history" {
		t.Fatalf("updates = %d, ServiceID = %q", len(updates), session.ServiceID())
	}
	for _, update := range updates {
		if update.ConversationID != nil && *update.ConversationID == "_agent_local_chat_history" {
			t.Fatal("background update carried a local-history sentinel")
		}
	}
}

func TestExecutionPort_PerServiceCallHistory_RunStreamingAsync_PersistsInputMessages_WhenConsumerAbandonsEnumerationAsync(t *testing.T) {
	history := &executionPortHistory{}
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("first ", "second ", "third")}, agent.Config{
		HistoryProvider: history, RequirePerServiceCallHistoryPersistence: true,
	})
	updates := 0
	for _, err := range a.RunText(t.Context(), "frc-input", agent.WithSession(executionPortSession(t, a)), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		updates++
		break
	}
	if updates != 1 || len(history.invoked) != 1 {
		t.Fatalf("updates = %d, history completions = %d", updates, len(history.invoked))
	}
	executionPortMessages(t, history.invoked[0].RequestMessages, "frc-input")
	if len(history.invoked[0].ResponseMessages) != 0 || history.invoked[0].Err != nil {
		t.Fatalf("abandoned-stream completion = %#v", history.invoked[0])
	}
}

func executionPortFunctionCalls(calls ...*message.FunctionCallContent) agent.RunFunc {
	return func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			contents := make([]message.Content, len(calls))
			for i, call := range calls {
				clonedCall := *call
				contents[i] = &clonedCall
			}
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: contents}, nil)
		}
	}
}

func executionPortTraceMiddleware(trace *[]string, before, after string) agent.FunctionInvocationMiddleware {
	return func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		*trace = append(*trace, before)
		result, err := next(ctx, in)
		if err == nil {
			*trace = append(*trace, after)
		}
		return result, err
	}
}

func executionPortTraceOrder(t *testing.T, trace []string, want ...string) {
	t.Helper()
	last := -1
	for _, step := range want {
		index := slices.Index(trace, step)
		if index <= last {
			t.Fatalf("trace = %q; want %q in order", trace, want)
		}
		last = index
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_WithFunctionCall_NoOptions_InvokesMiddlewareAsync(t *testing.T) {
	var trace []string
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) {
		trace = append(trace, "Function-Executed")
		return "Function result", nil
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{
		Tools: []tool.Tool{fn}, FunctionMiddlewares: []agent.FunctionInvocationMiddleware{executionPortTraceMiddleware(&trace, "Middleware-Pre", "Middleware-Post")},
	})
	if _, err := a.RunText(t.Context(), "Test message").Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortTraceOrder(t, trace, "Middleware-Pre", "Function-Executed", "Middleware-Post")
}

func TestExecutionPort_FunctionInvocation_RunAsync_WithFunctionCall_AgentRunOptions_InvokesMiddlewareAsync(t *testing.T) {
	var trace []string
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) {
		trace = append(trace, "Function-Executed")
		return "Function result", nil
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{
		Tools: []tool.Tool{fn}, FunctionMiddlewares: []agent.FunctionInvocationMiddleware{executionPortTraceMiddleware(&trace, "Middleware-Pre", "Middleware-Post")},
	})
	options := []agent.Option{}
	if _, err := a.RunText(t.Context(), "Test message", options...).Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortTraceOrder(t, trace, "Middleware-Pre", "Function-Executed", "Middleware-Post")
}

func TestExecutionPort_FunctionInvocation_RunAsync_WithFunctionCall_InvokesMiddlewareAsync(t *testing.T) {
	var trace []string
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) {
		trace = append(trace, "Function-Executed")
		return "Function result", nil
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{executionPortTraceMiddleware(&trace, "Middleware-Pre", "Middleware-Post")}})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect(); err != nil {
		t.Fatal(err)
	}
	executionPortTraceOrder(t, trace, "Middleware-Pre", "Function-Executed", "Middleware-Post")
}

func TestExecutionPort_FunctionInvocation_RunAsync_WithMultipleFunctionCalls_InvokesMiddlewareForEachAsync(t *testing.T) {
	var trace []string
	first := functool.MustNew(functool.Config{Name: "Function1", Description: "First test function"}, func(context.Context, struct{}) (string, error) {
		trace = append(trace, "Function1-Executed")
		return "Function1 result", nil
	})
	second := functool.MustNew(functool.Config{Name: "Function2", Description: "Second test function"}, func(context.Context, struct{}) (string, error) {
		trace = append(trace, "Function2-Executed")
		return "Function2 result", nil
	})
	middleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		trace = append(trace, "Middleware-Pre-"+in.Function.Name())
		result, err := next(ctx, in)
		if err == nil {
			trace = append(trace, "Middleware-Post-"+in.Function.Name())
		}
		return result, err
	})
	a := agent.New(agent.ProviderConfig{
		Run: executionPortFunctionCalls(
			&message.FunctionCallContent{CallID: "call_1", Name: "Function1", Arguments: "{}"},
			&message.FunctionCallContent{CallID: "call_2", Name: "Function2", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{middleware}})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(first), agent.WithTool(second)).Collect(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Function1", "Function2"} {
		executionPortTraceOrder(t, trace, "Middleware-Pre-"+name, name+"-Executed", "Middleware-Post-"+name)
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_MiddlewareContext_ContainsCorrectValuesAsync(t *testing.T) {
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct {
		Param string `json:"param"`
	},
	) (string, error) {
		return "Function result", nil
	})
	var captured *agent.FunctionInvocationContext
	var capturedAgent *agent.Agent
	middleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		captured = in
		capturedAgent, _ = agent.AgentFromContext(ctx)
		return next(ctx, in)
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: `{"param":"value"}`}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{middleware}})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect(); err != nil {
		t.Fatal(err)
	}
	if captured == nil || captured.Function.Name() != "TestFunction" || capturedAgent != a || captured.Arguments == "" {
		t.Fatalf("invocation = %#v, agent identity matched = %v", captured, capturedAgent == a)
	}
}

func TestExecutionPort_FunctionInvocation_AIAgentBuilder_Use_FunctionInvocationMiddleware_WorksCorrectlyAsync(t *testing.T) {
	var trace []string
	fn := functool.MustNew(functool.Config{Name: "TestFunction"}, func(context.Context, struct{}) (string, error) { return "test result", nil })
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{executionPortTraceMiddleware(&trace, "Middleware-Pre", "Middleware-Post")}})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect(); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"Middleware-Pre", "Middleware-Post"} {
		if !slices.Contains(trace, step) {
			t.Fatalf("missing %q in %q", step, trace)
		}
	}
}

func TestExecutionPort_FunctionInvocation_AIAgentBuilder_Use_MultipleFunctionMiddleware_BothExecuteAsync(t *testing.T) {
	first, second := false, false
	fn := functool.MustNew(functool.Config{Name: "TestFunction"}, func(context.Context, struct{}) (string, error) { return "test result", nil })
	m1 := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		first = true
		return next(ctx, in)
	})
	m2 := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		second = true
		return next(ctx, in)
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{m1, m2}})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect(); err != nil {
		t.Fatal(err)
	}
	if !first || !second {
		t.Fatalf("middleware executed = %v/%v", first, second)
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_FunctionThrowsException_MiddlewareCanHandleAsync(t *testing.T) {
	failure := errors.New("Function error")
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) { return "", failure })
	handled := false
	middleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		result, err := next(ctx, in)
		if errors.Is(err, failure) {
			handled = true
			return "Error handled by middleware", nil
		}
		return result, err
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{middleware}})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect(); err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("middleware did not handle the function error")
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_MiddlewareModifiesResult_ModifiedResultUsedAsync(t *testing.T) {
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) { return "Original result", nil })
	middleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		if _, err := next(ctx, in); err != nil {
			return nil, err
		}
		return "Modified by middleware", nil
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{middleware}})
	response, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var first *message.FunctionResultContent
	for c := range response.Contents() {
		if result, ok := c.(*message.FunctionResultContent); ok {
			first = result
			break
		}
	}
	if first == nil || first.Result != "Modified by middleware" {
		t.Fatalf("first function result = %#v", first)
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_MultipleFunctionMiddleware_ExecutesInCorrectOrderAsync(t *testing.T) {
	var trace []string
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) {
		trace = append(trace, "Function-Executed")
		return "Function result", nil
	})
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().AddFunctionCall("call_123", "TestFunction", "{}").NewTurn().AddText("Final response").Build()}
	a := agent.New(agent.ProviderConfig{Run: runner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
		FunctionMiddlewares: []agent.FunctionInvocationMiddleware{
			executionPortTraceMiddleware(&trace, "First-Pre", "First-Post"),
			executionPortTraceMiddleware(&trace, "Second-Pre", "Second-Post"),
		},
	})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect(); err != nil {
		t.Fatal(err)
	}
	want := []string{"First-Pre", "Second-Pre", "Function-Executed", "Second-Post", "First-Post"}
	if !slices.Equal(trace, want) {
		t.Fatalf("trace = %q, want %q", trace, want)
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_FunctionMiddlewareWithRunningMiddleware_BothExecuteAsync(t *testing.T) {
	var trace []string
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) {
		trace = append(trace, "Function-Executed")
		return "Function result", nil
	})
	running := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			trace = append(trace, "Running-Pre")
			for update, err := range next(ctx, messages, options...) {
				if !yield(update, err) {
					return
				}
			}
			trace = append(trace, "Running-Post")
		}
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{
		Middlewares:         []agent.Middleware{running},
		FunctionMiddlewares: []agent.FunctionInvocationMiddleware{executionPortTraceMiddleware(&trace, "Function-Pre", "Function-Post")},
	})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect(); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"Running-Pre", "Running-Post", "Function-Pre", "Function-Post", "Function-Executed"} {
		if !slices.Contains(trace, step) {
			t.Fatalf("missing %q in %q", step, trace)
		}
	}
}

func TestExecutionPort_FunctionInvocation_RunStreamingAsync_WithFunctionCall_InvokesMiddlewareAsync(t *testing.T) {
	var trace []string
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) {
		trace = append(trace, "Function-Executed")
		return "Function result", nil
	})
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for update, err := range executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"})(ctx, messages, options...) {
				if !yield(update, err) {
					return
				}
			}
			yield(executionPortUpdate("Streaming response"), nil)
		}
	}
	a := agent.New(agent.ProviderConfig{Run: run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
		FunctionMiddlewares: []agent.FunctionInvocationMiddleware{executionPortTraceMiddleware(&trace, "Middleware-Pre", "Middleware-Post")},
	})
	updates := executionPortConsume(t, a.RunText(t.Context(), "Test message", agent.WithTool(fn), agent.Stream(true)))
	if len(updates) == 0 {
		t.Fatal("no streaming response")
	}
	executionPortTraceOrder(t, trace, "Middleware-Pre", "Function-Executed", "Middleware-Post")
}

func TestExecutionPort_FunctionInvocation_RunAsync_NoFunctionCalls_MiddlewareNotInvokedAsync(t *testing.T) {
	invoked := false
	middleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		invoked = true
		return next(ctx, in)
	})
	a := agent.New(agent.ProviderConfig{Run: executionPortRun("Regular response"), Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
		FunctionMiddlewares: []agent.FunctionInvocationMiddleware{middleware},
	})
	if _, err := a.RunText(t.Context(), "Test message").Collect(); err != nil {
		t.Fatal(err)
	}
	if invoked {
		t.Fatal("function middleware ran without a function call")
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_CancellationToken_PropagatedToMiddlewareAsync(t *testing.T) {
	expected, cancel := context.WithCancel(t.Context())
	defer cancel()
	var captured context.Context
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) { return "Function result", nil })
	middleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
		captured = ctx
		return next(ctx, in)
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{middleware}})
	if _, err := a.RunText(expected, "Test message", agent.WithTool(fn)).Collect(); err != nil {
		t.Fatal(err)
	}
	if captured == nil || captured.Done() != expected.Done() {
		t.Fatal("middleware did not receive the caller's cancellation signal")
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_MiddlewareDoesNotCallNext_FunctionNotExecutedAsync(t *testing.T) {
	executed := false
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) {
		executed = true
		return "Function result", nil
	})
	middleware := agent.FunctionInvocationMiddleware(func(func(context.Context, *agent.FunctionInvocationContext) (any, error), context.Context, *agent.FunctionInvocationContext) (any, error) {
		return "Blocked by middleware", nil
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{middleware}})
	response, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var first *message.FunctionResultContent
	for c := range response.Contents() {
		if result, ok := c.(*message.FunctionResultContent); ok {
			first = result
			break
		}
	}
	if executed || first == nil || first.Result != "Blocked by middleware" {
		t.Fatalf("function executed = %v, first result = %#v", executed, first)
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_NestedAgent_IsolatesFunctionMiddlewareAsync(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, nestedStreaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("outer=%t/inner=%t", streaming, nestedStreaming), func(t *testing.T) {
				var innerInvocations, outerInvocations []string
				innerFn := functool.MustNew(functool.Config{Name: "InnerFunction"}, func(context.Context, struct{}) (string, error) { return "Inner result", nil })
				innerCalls := 0
				innerRunner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(context.Context, []*message.Message, ...agent.Option) { innerCalls++ }).
					AddFunctionCall("inner", "InnerFunction", "{}").
					NewTurn(func(context.Context, []*message.Message, ...agent.Option) { innerCalls++ }).AddText("Inner complete").Build()}
				innerMiddleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
					innerInvocations = append(innerInvocations, in.Function.Name())
					return next(ctx, in)
				})
				inner := agent.New(agent.ProviderConfig{Run: innerRunner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
					Tools: []tool.Tool{innerFn}, FunctionMiddlewares: []agent.FunctionInvocationMiddleware{innerMiddleware},
				})
				outerFn := functool.MustNew(functool.Config{Name: "OuterFunction"}, func(ctx context.Context, _ struct{}) (string, error) {
					response, err := inner.RunText(ctx, "Run the inner function", agent.Stream(nestedStreaming)).Collect()
					if err != nil {
						return "", err
					}
					return response.String(), nil
				})
				outerCalls := 0
				outerRunner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(context.Context, []*message.Message, ...agent.Option) { outerCalls++ }).
					AddFunctionCall("outer", "OuterFunction", "{}").
					NewTurn(func(context.Context, []*message.Message, ...agent.Option) { outerCalls++ }).AddText("Outer complete").Build()}
				outerMiddleware := agent.FunctionInvocationMiddleware(func(next func(context.Context, *agent.FunctionInvocationContext) (any, error), ctx context.Context, in *agent.FunctionInvocationContext) (any, error) {
					outerInvocations = append(outerInvocations, in.Function.Name())
					return next(ctx, in)
				})
				outer := agent.New(agent.ProviderConfig{Run: outerRunner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})}}, agent.Config{
					Tools: []tool.Tool{outerFn}, FunctionMiddlewares: []agent.FunctionInvocationMiddleware{outerMiddleware},
				})
				response, err := outer.RunText(t.Context(), "Run the outer function", agent.Stream(streaming)).Collect()
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(innerInvocations, []string{"InnerFunction"}) || !slices.Equal(outerInvocations, []string{"OuterFunction"}) ||
					innerCalls != 2 || outerCalls != 2 {
					t.Fatalf("inner/outer invocations = %q/%q; calls = %d/%d", innerInvocations, outerInvocations, innerCalls, outerCalls)
				}
				var results []*message.FunctionResultContent
				for c := range response.Contents() {
					if result, ok := c.(*message.FunctionResultContent); ok && result.CallID == "outer" {
						results = append(results, result)
					}
				}
				if len(results) != 1 || results[0].Result != "Inner complete" {
					t.Fatalf("outer results = %#v", results)
				}
			})
		}
	}
}

func TestExecutionPort_ChatClientAgent_RunAsyncPassesChatOptionsWhenUsingChatClientAgentRunOptionsAsync(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		params, ok := agent.GetOption(options, openaiprovider.ResponsesNewParams)
		if !ok || params.MaxOutputTokens.Value != 100 {
			t.Fatalf("provider options = %#v, present = %v", params, ok)
		}
		return executionPortRun("response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	if _, err := a.RunText(t.Context(), "test", openaiprovider.ResponsesNewParams(responses.ResponseNewParams{MaxOutputTokens: openai.Int(100)})).Collect(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d", calls)
	}
}

func TestExecutionPort_ChatHistoryManagement_RunAsync_ClonesChatOptions_ToAddConversationIdAsync(t *testing.T) {
	params := responses.ResponseNewParams{MaxOutputTokens: openai.Int(100)}
	options := make([]agent.Option, 1, 4)
	options[0] = openaiprovider.ResponsesNewParams(params)
	calls := 0
	run := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		got, ok := agent.GetOption(options, openaiprovider.ResponsesNewParams)
		if !ok || got.MaxOutputTokens.Value != 100 {
			t.Fatalf("provider options = %#v, present = %v", got, ok)
		}
		if id, _ := agent.GetOption(options, agent.WithServiceID); id != "ConvId" {
			t.Fatalf("provider ServiceID = %q", id)
		}
		return executionPortConversationRun("ConvId", "response")(ctx, messages, options...)
	}
	a := agent.New(agent.ProviderConfig{Run: run}, agent.Config{RunOptions: []agent.Option{agent.WithInstructions("test instructions")}})
	session := executionPortSession(t, a)
	session.SetServiceID("ConvId")
	options = append(options, agent.WithSession(session))
	if _, err := a.RunText(t.Context(), "test", options...).Collect(); err != nil {
		t.Fatal(err)
	}
	got, ok := agent.GetOption(options, openaiprovider.ResponsesNewParams)
	if !ok || !reflect.DeepEqual(got, params) || len(options) != 2 || calls != 1 {
		t.Fatalf("caller options mutated: %#v; calls = %d", options, calls)
	}
	if id, present := agent.GetOption(options, agent.WithServiceID); present || id != "" {
		t.Fatalf("caller options gained ServiceID = %q, present = %v", id, present)
	}
}

func TestExecutionPort_FunctionInvocation_RunAsync_MiddlewareThrowsPreInvocation_ExceptionSurfacesAsync(t *testing.T) {
	failure := errors.New("Pre-invocation error")
	fn := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"}, func(context.Context, struct{}) (string, error) {
		return "Function result", nil
	})
	middleware := agent.FunctionInvocationMiddleware(func(func(context.Context, *agent.FunctionInvocationContext) (any, error), context.Context, *agent.FunctionInvocationContext) (any, error) {
		return nil, failure
	})
	a := agent.New(agent.ProviderConfig{
		Run:         executionPortFunctionCalls(&message.FunctionCallContent{CallID: "call_123", Name: "TestFunction", Arguments: "{}"}),
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, agent.Config{FunctionMiddlewares: []agent.FunctionInvocationMiddleware{middleware}})
	if _, err := a.RunText(t.Context(), "Test message", agent.WithTool(fn)).Collect(); err != failure {
		t.Fatalf("error = %v, want the original middleware error %v", err, failure)
	}
}
