// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
)

func invokeHistoryProvider(provider agent.HistoryProvider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

func invokeHistoryProviderInvoked(provider agent.HistoryProvider, ctx context.Context, requestMessages, responseMessages []*message.Message, options ...agent.Option) error {
	return provider.Invoked(ctx, agent.InvokedContext{RequestMessages: requestMessages, ResponseMessages: responseMessages, Options: options})
}

func TestSessionHistory_TryGet_WithNullSession_ReturnsError(t *testing.T) {
	var session *agent.Session
	if messages, err := session.InMemoryHistory(""); err == nil || messages != nil {
		t.Fatalf("InMemoryHistory(nil) = (%v, %v), want error", messages, err)
	}
}

func TestSessionHistory_TryGet_WhenStateExists_ReturnsTrueAndMessages(t *testing.T) {
	session := agenttest.CreateSession()
	expected := []*message.Message{message.NewText("Hello"), message.NewText("Hi there!")}
	expected[1].Role = message.RoleAssistant
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), expected, nil, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}

	got, err := session.InMemoryHistory("")
	if err != nil || len(got) != 2 || got[0] != expected[0] || got[1] != expected[1] {
		t.Fatalf("history = (%v, %v), want original messages", got, err)
	}
}

func TestSessionHistory_TryGet_WhenStateDoesNotExist_ReturnsFalse(t *testing.T) {
	session := agenttest.CreateSession()
	got, err := session.InMemoryHistory("")
	if err != nil || got != nil {
		t.Fatalf("history = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestSessionHistory_TryGet_WithCustomStateKey_UsesCustomKey(t *testing.T) {
	session := agenttest.CreateSession()
	expected := message.NewText("Test message")
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{StateKey: "custom-history-key"})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{expected}, nil, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}

	got, err := session.InMemoryHistory("custom-history-key")
	if err != nil || len(got) != 1 || got[0] != expected {
		t.Fatalf("custom history = (%v, %v), want original message", got, err)
	}
}

func TestSessionHistory_TryGet_WithCustomStateKey_DoesNotFindDefaultKey(t *testing.T) {
	session := agenttest.CreateSession()
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{message.NewText("Test message")}, nil, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}

	got, err := session.InMemoryHistory("other-key")
	if err != nil || got != nil {
		t.Fatalf("other-key history = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestSessionHistory_TryGet_WhenStateExistsWithNullMessages_ReturnsFalse(t *testing.T) {
	var session agent.Session
	if err := json.Unmarshal([]byte(`{"State":{"in-memory":{"messages":null}}}`), &session); err != nil {
		t.Fatal(err)
	}
	got, err := session.InMemoryHistory("")
	if err != nil || got != nil {
		t.Fatalf("null history = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestSessionHistory_Set_WithNullSession_ReturnsError(t *testing.T) {
	var session *agent.Session
	if err := session.SetInMemoryHistory("", []*message.Message{}); err == nil {
		t.Fatal("SetInMemoryHistory(nil) succeeded, want error")
	}
}

func TestSessionHistory_Set_WhenNoExistingState_CreatesNewState(t *testing.T) {
	session := agenttest.CreateSession()
	messages := []*message.Message{message.NewText("Hello"), message.NewText("Hi!")}
	messages[1].Role = message.RoleAssistant

	if err := session.SetInMemoryHistory("", messages); err != nil {
		t.Fatal(err)
	}
	got, err := session.InMemoryHistory("")
	if err != nil || len(got) != 2 || &got[0] != &messages[0] {
		t.Fatalf("history = (%v, %v), want original messages", got, err)
	}
}

func TestSessionHistory_Set_WhenExistingState_ReplacesMessages(t *testing.T) {
	session := agenttest.CreateSession()
	if err := session.SetInMemoryHistory("", []*message.Message{message.NewText("Original")}); err != nil {
		t.Fatal(err)
	}
	messages := []*message.Message{message.NewText("New message"), message.NewText("New response")}
	messages[1].Role = message.RoleAssistant

	if err := session.SetInMemoryHistory("", messages); err != nil {
		t.Fatal(err)
	}
	got, err := session.InMemoryHistory("")
	if err != nil || len(got) != 2 || &got[0] != &messages[0] {
		t.Fatalf("replaced history = (%v, %v), want new messages", got, err)
	}
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	loaded, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil || len(loaded) != 2 || loaded[0].String() != "New message" || loaded[1].String() != "New response" {
		t.Fatalf("provider history = (%v, %v), want replacement", loaded, err)
	}
}

func TestSessionHistory_Set_WithCustomStateKey_UsesCustomKey(t *testing.T) {
	session := agenttest.CreateSession()
	messages := []*message.Message{message.NewText("Test")}
	if err := session.SetInMemoryHistory("custom-history-key", messages); err != nil {
		t.Fatal(err)
	}
	got, err := session.InMemoryHistory("custom-history-key")
	if err != nil || len(got) != 1 || &got[0] != &messages[0] {
		t.Fatalf("custom history = (%v, %v), want original message", got, err)
	}
	if got, err := session.InMemoryHistory(""); err != nil || got != nil {
		t.Fatalf("default history = (%v, %v), want missing", got, err)
	}
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{StateKey: "custom-history-key"})
	loaded, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil || len(loaded) != 1 || loaded[0].String() != "Test" {
		t.Fatalf("provider history = (%v, %v), want custom history", loaded, err)
	}
}

func TestSessionHistory_Set_WithEmptyList_SetsEmptyList(t *testing.T) {
	session := agenttest.CreateSession()
	if err := session.SetInMemoryHistory("", []*message.Message{}); err != nil {
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
	got, err := restored.InMemoryHistory("")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty history = (%v, %v), want non-nil empty slice", got, err)
	}
}

func TestSessionHistory_Set_EditsToDeserializedHistorySurviveSerialization(t *testing.T) {
	original := agenttest.CreateSession()
	if err := original.SetInMemoryHistory("", []*message.Message{message.NewText("original")}); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var session agent.Session
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}

	history, err := session.InMemoryHistory("")
	if err != nil || len(history) != 1 || history[0].String() != "original" {
		t.Fatalf("loaded history = %v, err = %v; want original", messageStrings(history), err)
	}
	replacement := slices.Clone(history)
	replacement[0] = message.NewText("updated")
	replacement = append(replacement, message.NewText("added"))
	if err := session.SetInMemoryHistory("", replacement); err != nil {
		t.Fatal(err)
	}

	data, err = json.Marshal(&session)
	if err != nil {
		t.Fatal(err)
	}
	var resumed agent.Session
	if err := json.Unmarshal(data, &resumed); err != nil {
		t.Fatal(err)
	}
	got, err := resumed.InMemoryHistory("")
	if err != nil || !slices.Equal(messageStrings(got), []string{"updated", "added"}) {
		t.Fatalf("resumed history = %v, err = %v; want updated and added", messageStrings(got), err)
	}
}

func TestSessionHistory_Set_NilHistoryPreservesOmittedMessages(t *testing.T) {
	session := agenttest.CreateSession()
	if err := session.SetInMemoryHistory("", nil); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ State map[string]json.RawMessage }
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if got := string(payload.State["in-memory"]); got != "{}" {
		t.Fatalf("nil history JSON = %s, want {}", got)
	}
}

func TestNewInMemoryHistoryProvider_DefaultConfig_RoundTripsHistory(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})

	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	newRequest := message.NewText("new request")
	messages, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{newRequest}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error loading history: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 2 history messages plus request, got %d", len(messages))
	}
	if messages[2] != newRequest {
		t.Fatal("expected original request message to be preserved last")
	}
	if messages[0].String() != "request" || messages[1].String() != "response" {
		t.Fatalf("unexpected output order/content")
	}
	if messages[0].Source.ID != "in-memory" || messages[1].Source.ID != "in-memory" {
		t.Fatal("expected history messages to have in-memory source ID")
	}

	newResponse := message.NewText("new response")
	if err := invokeHistoryProviderInvoked(provider, t.Context(), messages, []*message.Message{newResponse}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing next turn: %v", err)
	}
	messages, err = invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error loading updated history: %v", err)
	}
	if got, want := len(messages), 4; got != want {
		t.Fatalf("expected history to append only new messages, got %d want %d", got, want)
	}
	if messages[0].String() != "request" || messages[1].String() != "response" || messages[2].String() != "new request" || messages[3].String() != "new response" {
		t.Fatalf("unexpected updated history order/content")
	}
}

func TestNewHistoryProvider_PanicsWithoutSourceID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	agent.NewHistoryProvider(agent.HistoryProviderConfig{})
}

func TestNewInMemoryHistoryProvider_PanicsWithBlankStateKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config agent.InMemoryHistoryProviderConfig
	}{
		{name: "explicit state key", config: agent.InMemoryHistoryProviderConfig{StateKey: " "}},
		{name: "default state key from source ID", config: agent.InMemoryHistoryProviderConfig{SourceID: "\t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected blank history state key to panic at construction")
				}
			}()
			agent.NewInMemoryHistoryProvider(tc.config)
		})
	}
}

func TestNewInMemoryHistoryProvider_AddsRequestsAndResponses(t *testing.T) {
	session := agenttest.CreateSession()
	if err := session.SetInMemoryHistory("", []*message.Message{message.NewText("original instructions")}); err != nil {
		t.Fatal(err)
	}
	user := message.NewText("Hello")
	contextMessage := message.NewText("additional context")
	contextMessage.Role = message.RoleSystem
	contextMessage.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "TestSource"}
	response := message.NewText("Hi there!")
	response.Role = message.RoleAssistant
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{user, contextMessage}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	got, err := session.InMemoryHistory("")
	if err != nil || !slices.Equal(messageStrings(got), []string{"original instructions", "Hello", "additional context", "Hi there!"}) {
		t.Fatalf("history = %v, err = %v; want original and three added messages", messageStrings(got), err)
	}
}

func TestNewInMemoryHistoryProvider_InvokedWithEmptyMessages(t *testing.T) {
	session := agenttest.CreateSession()
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), nil, nil, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	got, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil || len(got) != 0 {
		t.Fatalf("history = %v, err = %v; want empty", messageStrings(got), err)
	}
}

func TestNewInMemoryHistoryProvider_InvokingReturnsAllMessages(t *testing.T) {
	session := agenttest.CreateSession()
	history := []*message.Message{message.NewText("Test1"), message.NewText("Test2")}
	history[1].Role = message.RoleAssistant
	if err := session.SetInMemoryHistory("", history); err != nil {
		t.Fatal(err)
	}
	request := message.NewText("Hello")
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	got, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(session))
	if err != nil || !slices.Equal(messageStrings(got), []string{"Test1", "Test2", "Hello"}) {
		t.Fatalf("messages = %v, err = %v; want history then request", messageStrings(got), err)
	}
	if got[0].Source.Type != agent.SourceTypeHistoryProvider || got[1].Source.Type != agent.SourceTypeHistoryProvider ||
		got[2].Source.Type != message.SourceTypeExternal {
		t.Fatalf("sources = [%+v %+v %+v], want history, history, external", got[0].Source, got[1].Source, got[2].Source)
	}
}

func TestNewInMemoryHistoryProvider_SetMessagesUpdatesState(t *testing.T) {
	session := agenttest.CreateSession()
	messages := []*message.Message{message.NewText("Hello"), message.NewText("World")}
	messages[1].Role = message.RoleAssistant
	if err := session.SetInMemoryHistory("", messages); err != nil {
		t.Fatal(err)
	}
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	got, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil || !slices.Equal(messageStrings(got), []string{"Hello", "World"}) {
		t.Fatalf("history = %v, err = %v; want Hello, World", messageStrings(got), err)
	}
}

func TestNewInMemoryHistoryProvider_InvokedErrorDoesNotAddMessages(t *testing.T) {
	session := agenttest.CreateSession()
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	if err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: []*message.Message{message.NewText("Hello")},
		Options:         []agent.Option{agent.WithSession(session)},
		Err:             fmt.Errorf("test exception"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil || len(got) != 0 {
		t.Fatalf("history after failed run = %v, err = %v; want empty", messageStrings(got), err)
	}
}

func TestNewInMemoryHistoryProvider_CustomRequestFilterOverridesDefault(t *testing.T) {
	session := agenttest.CreateSession()
	external := message.NewText("External message")
	history := message.NewText("From history")
	history.Role = message.RoleSystem
	history.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "HistorySource"}
	contextMessage := message.NewText("From context provider")
	contextMessage.Role = message.RoleSystem
	contextMessage.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "ContextSource"}
	response := message.NewText("Response")
	response.Role = message.RoleAssistant
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		StoreInputRequestMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.Source.Type != message.SourceTypeExternal }), nil
		},
	})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{external, history, contextMessage}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	got, err := session.InMemoryHistory("")
	if err != nil || !slices.Equal(messageStrings(got), []string{"External message", "Response"}) {
		t.Fatalf("stored history = %v, err = %v; want only external and response", messageStrings(got), err)
	}
}

func TestNewInMemoryHistoryProvider_OutputFilterSelectsUserMessages(t *testing.T) {
	session := agenttest.CreateSession()
	messages := []*message.Message{message.NewText("User message"), message.NewText("Assistant message"), message.NewText("System message")}
	messages[1].Role = message.RoleAssistant
	messages[2].Role = message.RoleSystem
	if err := session.SetInMemoryHistory("", messages); err != nil {
		t.Fatal(err)
	}
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		ProvideOutputMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.Role != message.RoleUser }), nil
		},
	})
	got, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil || !slices.Equal(messageStrings(got), []string{"User message"}) {
		t.Fatalf("filtered history = %v, err = %v; want only user message", messageStrings(got), err)
	}
}

// Agent runs may share a conversation session; concurrent stores must preserve
// every turn without racing on session state. Run with -race.
func TestNewInMemoryHistoryProvider_ConcurrentStoresOnSameSession_NoDataRace(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	session := agenttest.CreateSession()

	const stores = 64
	start := make(chan struct{})
	errs := make(chan error, stores)
	var wg sync.WaitGroup
	wg.Add(stores)
	for i := range stores {
		go func(index int) {
			defer wg.Done()
			<-start
			errs <- invokeHistoryProviderInvoked(
				provider,
				context.Background(),
				[]*message.Message{message.NewText(fmt.Sprintf("request-%d", index))},
				nil,
				agent.WithSession(session),
			)
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("store history: %v", err)
		}
	}
	messages, err := invokeHistoryProvider(provider, context.Background(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(messages) != stores {
		t.Fatalf("stored history message count = %d, want %d", len(messages), stores)
	}
}

func TestNewInMemoryHistoryProvider_ProvidePassesOriginalMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "InMemoryHistoryProvider"})
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")
	response.Role = message.RoleAssistant

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	newRequest := message.NewText("new request")
	messages, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{newRequest}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error loading history: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 2 history messages plus request, got %d", len(messages))
	}
	if messages[2] != newRequest {
		t.Fatal("expected original request message to be preserved last")
	}
	if got := messages[2].String(); got != "new request" {
		t.Fatalf("new request text = %q, want new request", got)
	}
	if messages[0].String() != "request" || messages[1].String() != "response" {
		t.Fatalf("unexpected output order/content")
	}
	if messages[0].Source.ID != "InMemoryHistoryProvider" || messages[1].Source.ID != "InMemoryHistoryProvider" {
		t.Fatal("expected history messages to have provider source ID")
	}
	if messages[0].Role != message.RoleUser || messages[1].Role != message.RoleAssistant || messages[2].Role != message.RoleUser {
		t.Fatalf("message roles = [%q %q %q], want [user assistant user]", messages[0].Role, messages[1].Role, messages[2].Role)
	}
	if messages[0].Source.Type != agent.SourceTypeHistoryProvider || messages[1].Source.Type != agent.SourceTypeHistoryProvider {
		t.Fatal("expected loaded history messages to have history-provider source type")
	}
	if got := messages[2].Source.Type; got != message.SourceTypeExternal {
		t.Fatalf("new request source type = %q, want external", got)
	}
}

func TestNewInMemoryHistoryProvider_SourceID_MapsToSessionStateKey(t *testing.T) {
	session := agenttest.CreateSession()
	customProvider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "custom"})
	defaultProvider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "InMemoryHistoryProvider"})

	if err := invokeHistoryProviderInvoked(customProvider, t.Context(), []*message.Message{message.NewText("custom")}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing custom history: %v", err)
	}
	if err := invokeHistoryProviderInvoked(defaultProvider, t.Context(), []*message.Message{message.NewText("default")}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing default history: %v", err)
	}

	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "custom"})
	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 history message, got %d", len(messages))
	}
	if messages[0].String() != "custom" {
		t.Fatalf("expected custom history message, got %q", messages[0].String())
	}
}

func TestNewInMemoryHistoryProvider_StateKey_CanDifferFromSourceID(t *testing.T) {
	session := agenttest.CreateSession()
	writer := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "writer", StateKey: "history-state"})
	reader := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "reader", StateKey: "history-state"})

	if err := invokeHistoryProviderInvoked(writer, t.Context(), []*message.Message{message.NewText("stored")}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error storing history: %v", err)
	}

	messages, err := invokeHistoryProvider(reader, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading history: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "stored" {
		t.Fatalf("expected stored message, got %v", messageStrings(messages))
	}
	if messages[0].Source.ID != "reader" {
		t.Fatalf("expected reader source ID, got %q", messages[0].Source.ID)
	}
}

func TestNewInMemoryHistoryProvider_StateInitializer_SeedsMissingState(t *testing.T) {
	session := agenttest.CreateSession()
	initializerCalls := 0
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StateInitializer: func(gotSession *agent.Session) []*message.Message {
			if gotSession != session {
				t.Fatalf("expected initializer session %p, got %p", session, gotSession)
			}
			initializerCalls++
			return []*message.Message{message.NewText("seed")}
		},
	})

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading history: %v", err)
	}
	if initializerCalls != 1 {
		t.Fatalf("expected initializer to be called once, got %d", initializerCalls)
	}
	if got := messageStrings(messages); !slices.Equal(got, []string{"seed"}) {
		t.Fatalf("messages = %v, want [seed]", got)
	}
	if messages[0].Source.ID != "k" {
		t.Fatalf("expected seed message source ID k, got %q", messages[0].Source.ID)
	}

	messages, err = invokeHistoryProvider(provider, t.Context(), []*message.Message{message.NewText("request")}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading initialized history: %v", err)
	}
	if initializerCalls != 1 {
		t.Fatalf("expected initialized state to be reused, got %d initializer calls", initializerCalls)
	}
	if got := messageStrings(messages); !slices.Equal(got, []string{"seed", "request"}) {
		t.Fatalf("initialized history = %v, want [seed request]", got)
	}
}

func TestNewInMemoryHistoryProvider_DefaultStoreInputRequestMessageFilter_ExcludesHistoryMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "k"})
	session := agenttest.CreateSession()

	user := message.NewText("user")
	historyMsg := message.NewText("history")
	historyMsg.Role = message.RoleSystem
	historyMsg.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "k"}
	otherHistoryMsg := message.NewText("other history")
	otherHistoryMsg.Role = message.RoleSystem
	otherHistoryMsg.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "other"}
	ctxMsg := message.NewText("ctx")
	ctxMsg.Role = message.RoleSystem
	ctxMsg.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "provider-A"}
	response := message.NewText("response")
	response.Role = message.RoleAssistant

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{user, historyMsg, otherHistoryMsg, ctxMsg}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 3 || messages[0].String() != "user" || messages[1].String() != "ctx" || messages[2].String() != "response" {
		t.Fatalf("stored history = %v, want [user ctx response] excluding both history-provider messages", messageStrings(messages))
	}
	if messages[0].Role != message.RoleUser || messages[1].Role != message.RoleSystem || messages[2].Role != message.RoleAssistant {
		t.Fatalf("stored roles = [%q %q %q], want [user system assistant]", messages[0].Role, messages[1].Role, messages[2].Role)
	}
}

func TestHistoryProvider_Invoking_DoesNotMutateProvidedMessageSlice(t *testing.T) {
	provided := message.NewText("provided")
	filtered := message.NewText("filtered")
	providedMessages := make([]*message.Message, 1, 2)
	providedMessages[0] = provided
	sentinel := message.NewText("sentinel")
	backing := providedMessages[:cap(providedMessages)]
	backing[1] = sentinel
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			return providedMessages, nil
		},
		ProvideOutputMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			messages[0] = filtered
			return messages, nil
		},
	})

	request := message.NewText("request")
	messages, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{request})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 || messages[1] != request {
		t.Fatalf("unexpected history output: %#v", messages)
	}
	if messages[0] == provided || messages[0].Source != (message.Source{Type: agent.SourceTypeHistoryProvider, ID: "history"}) {
		t.Fatal("expected provided message to be cloned and source-stamped")
	}
	if providedMessages[0] != provided || backing[1] != sentinel {
		t.Fatal("expected history provider not to modify the provided message backing array")
	}
	if messages[0] == filtered {
		t.Fatal("expected replacement message to be cloned")
	}
	if got := messageStrings(messages); !slices.Equal(got, []string{"filtered", "request"}) {
		t.Fatalf("output messages = %v, want [filtered request]", got)
	}
	if got := provided.String(); got != "provided" {
		t.Fatalf("original provided text = %q, want provided", got)
	}
}

func TestHistoryProvider_Invoking_ProvidesHistoryAndRequest(t *testing.T) {
	history := message.NewText("History message")
	request := message.NewText("Request message")
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			return []*message.Message{history}, nil
		},
	})
	got, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{request})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].String() != "History message" || got[1] != request {
		t.Fatalf("messages = %v, want history then request", messageStrings(got))
	}
}

func TestHistoryProvider_Invoking_HistoryPrecedesRequest(t *testing.T) {
	hist1 := message.NewText("Hist1")
	hist2 := message.NewText("Hist2")
	hist2.Role = message.RoleAssistant
	request := message.NewText("Req1")
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			return []*message.Message{hist1, hist2}, nil
		},
	})
	got, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{request})
	if err != nil {
		t.Fatal(err)
	}
	if strings := messageStrings(got); !slices.Equal(strings, []string{"Hist1", "Hist2", "Req1"}) {
		t.Fatalf("messages = %v, want [Hist1 Hist2 Req1]", strings)
	}
}

func TestHistoryProvider_Invoking_StampsHistorySource(t *testing.T) {
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			return []*message.Message{message.NewText("History")}, nil
		},
	})
	got, err := invokeHistoryProvider(provider, t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source.Type != agent.SourceTypeHistoryProvider {
		t.Fatalf("history source = %v, want history-provider", got)
	}
}

func TestHistoryProvider_Invoking_WithoutOutputFilterKeepsAllHistory(t *testing.T) {
	user := message.NewText("User msg")
	system := message.NewText("System msg")
	system.Role = message.RoleSystem
	assistant := message.NewText("Assistant msg")
	assistant.Role = message.RoleAssistant
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			return []*message.Message{user, system, assistant}, nil
		},
	})
	got, err := invokeHistoryProvider(provider, t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if texts := messageStrings(got); !slices.Equal(texts, []string{"User msg", "System msg", "Assistant msg"}) {
		t.Fatalf("history = %v, want all three messages", texts)
	}
}

func TestHistoryProvider_Invoking_AppliesOutputFilter(t *testing.T) {
	user := message.NewText("User msg")
	system := message.NewText("System msg")
	system.Role = message.RoleSystem
	assistant := message.NewText("Assistant msg")
	assistant.Role = message.RoleAssistant
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, error) {
			return []*message.Message{user, system, assistant}, nil
		},
		ProvideOutputMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.Role != message.RoleUser }), nil
		},
	})
	got, err := invokeHistoryProvider(provider, t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "User msg" {
		t.Fatalf("history = %v, want only User msg", messageStrings(got))
	}
}

func TestHistoryProvider_Invoking_WithoutProvideReturnsRequest(t *testing.T) {
	request := message.NewText("Hello")
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{SourceID: "history"})
	got, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{request})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != request {
		t.Fatalf("messages = %v, want only original request", messageStrings(got))
	}
}

func TestHistoryProvider_Invoked_FiltersDoNotMutateInputSlices(t *testing.T) {
	historyMessage := message.NewText("history")
	historyMessage.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "history"}
	externalMessage := message.NewText("external")
	requestMessages := []*message.Message{historyMessage, externalMessage}
	responseMessage := message.NewText("response")
	replacementResponse := message.NewText("replacement")
	responseMessages := []*message.Message{responseMessage, replacementResponse}
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		StoreInputResponseMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			messages[0] = replacementResponse
			return messages[:1], nil
		},
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			if len(invoked.ResponseMessages) != 1 || invoked.ResponseMessages[0] != replacementResponse {
				return fmt.Errorf("unexpected filtered response messages")
			}
			return nil
		},
	})

	if err := invokeHistoryProviderInvoked(provider, t.Context(), requestMessages, responseMessages); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestMessages[0] != historyMessage || requestMessages[1] != externalMessage {
		t.Fatal("expected request filtering not to mutate InvokedContext input")
	}
	if responseMessages[0] != responseMessage || responseMessages[1] != replacementResponse {
		t.Fatal("expected response filtering not to mutate InvokedContext input")
	}
}

func TestHistoryProvider_Invoked_StoresFilteredRequestAndResponse(t *testing.T) {
	external := message.NewText("External")
	history := message.NewText("From history")
	history.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "source"}
	response := message.NewText("Response")
	response.Role = message.RoleAssistant
	var stored agent.InvokedContext
	called := false
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			called = true
			stored = invoked
			return nil
		},
	})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{external, history}, []*message.Message{response}); err != nil {
		t.Fatal(err)
	}
	if !called || !slices.Equal(stored.RequestMessages, []*message.Message{external}) ||
		!slices.Equal(stored.ResponseMessages, []*message.Message{response}) {
		t.Fatalf("stored request/response = %v / %v, want External / Response", messageStrings(stored.RequestMessages), messageStrings(stored.ResponseMessages))
	}
}

func TestHistoryProvider_Invoked_SkipsStorageOnRunError(t *testing.T) {
	called := false
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Store: func(context.Context, agent.InvokedContext) error {
			called = true
			return nil
		},
	})
	err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: []*message.Message{message.NewText("msg")},
		Err:             fmt.Errorf("failed"),
	})
	if err != nil || called {
		t.Fatalf("Invoked error = %v, Store called = %v; want nil and false", err, called)
	}
}

func TestHistoryProvider_Invoked_UsesCustomStoreFilters(t *testing.T) {
	user := message.NewText("User msg")
	system := message.NewText("System msg")
	system.Role = message.RoleSystem
	assistant := message.NewText("Response")
	assistant.Role = message.RoleAssistant
	toolMessage := message.NewText("Response")
	toolMessage.Role = message.RoleTool
	var stored agent.InvokedContext
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		StoreInputRequestMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.Role != message.RoleSystem }), nil
		},
		StoreInputResponseMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.Role != message.RoleAssistant }), nil
		},
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			stored = invoked
			return nil
		},
	})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{user, system}, []*message.Message{assistant, toolMessage}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored.RequestMessages, []*message.Message{system}) ||
		!slices.Equal(stored.ResponseMessages, []*message.Message{assistant}) {
		t.Fatalf("stored request/response = %v / %v, want System msg / Response", messageStrings(stored.RequestMessages), messageStrings(stored.ResponseMessages))
	}
}

func TestHistoryProvider_Invoked_DefaultFilterExcludesHistoryMessages(t *testing.T) {
	external := message.NewText("External")
	history := message.NewText("History")
	history.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "src"}
	contextMessage := message.NewText("Context")
	contextMessage.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "src"}
	var stored []*message.Message
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			stored = invoked.RequestMessages
			return nil
		},
	})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{external, history, contextMessage}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored, []*message.Message{external, contextMessage}) {
		t.Fatalf("stored requests = %v, want External and Context", messageStrings(stored))
	}
}

func TestHistoryProvider_Invoked_PassesResponseMessagesToStore(t *testing.T) {
	responses := []*message.Message{message.NewText("Resp1"), message.NewText("Resp2")}
	responses[0].Role = message.RoleAssistant
	responses[1].Role = message.RoleAssistant
	var stored []*message.Message
	provider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "history",
		Store: func(_ context.Context, invoked agent.InvokedContext) error {
			stored = invoked.ResponseMessages
			return nil
		},
	})
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{message.NewText("msg")}, responses); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored, responses) || &stored[0] != &responses[0] {
		t.Fatalf("stored responses = %v, want original slice", messageStrings(stored))
	}
}

func TestNewInMemoryHistoryProvider_SkipStoreRequestMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StoreInputRequestMessageFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			return nil, nil
		},
	})
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "response" {
		t.Fatal("expected only response message to be stored")
	}
}

func TestNewInMemoryHistoryProvider_SkipStoreResponseMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StoreInputResponseMessageFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			return nil, nil
		},
	})
	session := agenttest.CreateSession()
	request := message.NewText("request")
	response := message.NewText("response")

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "request" {
		t.Fatal("expected only request message to be stored")
	}
}

func TestNewInMemoryHistoryProvider_StoreContextMessages(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StoreInputRequestMessageFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			filtered := make([]*message.Message, 0, len(messages))
			for _, msg := range messages {
				if msg.Source.ID == "" || msg.Source.ID == "provider-A" {
					filtered = append(filtered, msg)
				}
			}
			return filtered, nil
		},
	})
	session := agenttest.CreateSession()
	user := message.NewText("user")
	ctxMsg := message.NewText("ctx")
	ctxMsg.Source = message.Source{ID: "provider-A"}

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{user, ctxMsg}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 stored request messages, got %d", len(messages))
	}
}

func TestNewInMemoryHistoryProvider_StoreContextMessagesFrom(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		SourceID: "k",
		StoreInputRequestMessageFilter: func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
			filtered := make([]*message.Message, 0, len(messages))
			for _, msg := range messages {
				if msg.Source.ID == "provider-A" {
					filtered = append(filtered, msg)
				}
			}
			return filtered, nil
		},
	})
	session := agenttest.CreateSession()
	ctxA := message.NewText("ctx-a")
	ctxA.Source = message.Source{ID: "provider-A"}
	ctxB := message.NewText("ctx-b")
	ctxB.Source = message.Source{ID: "provider-B"}

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{ctxA, ctxB}, nil, agent.WithSession(session)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "ctx-a" {
		t.Fatal("expected only context messages from allowed SourceIDs to be stored")
	}
}

func TestNewInMemoryHistoryProvider_Invoking_IgnoresUnreadableState(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "k"})
	session := agenttest.CreateSession()
	session.Set("k", "not-a-history-state")
	request := []*message.Message{message.NewText("req")}

	messages, err := invokeHistoryProvider(provider, t.Context(), request, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error when state is unreadable: %v", err)
	}
	if len(messages) != 1 || messages[0].String() != "req" {
		t.Fatal("expected unreadable state to be ignored")
	}
}

func TestNewInMemoryHistoryProvider_Invoked_OverwritesUnreadableState(t *testing.T) {
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{SourceID: "k"})
	session := agenttest.CreateSession()
	session.Set("k", "not-a-history-state")
	request := message.NewText("r1")
	response := message.NewText("a1")

	err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{request}, []*message.Message{response}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error when state is unreadable: %v", err)
	}

	messages, err := invokeHistoryProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error reading stored history: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 stored messages, got %d", len(messages))
	}
}
