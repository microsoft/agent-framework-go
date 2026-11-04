// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"errors"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
)

func TestAgentMetadata_Constructor_WithNoArguments_SetsProviderNameToEmpty(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: (&agenttest.Runner{}).Run}, agent.Config{})
	if got := a.ProviderName(); got != "" {
		t.Fatalf("ProviderName() = %q, want empty", got)
	}
}

func TestAgentMetadata_Constructor_WithEmptyProviderName_SetsProviderNameToEmpty(t *testing.T) {
	a := agent.New(agent.ProviderConfig{ProviderName: "", Run: (&agenttest.Runner{}).Run}, agent.Config{})
	if got := a.ProviderName(); got != "" {
		t.Fatalf("ProviderName() = %q, want empty", got)
	}
}

func TestAgent_Name_ReturnsEmptyByDefault(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: (&agenttest.Runner{}).Run}, agent.Config{})
	if got := a.Name(); got != "" {
		t.Fatalf("Name() = %q, want empty", got)
	}
}

func TestAgent_Description_ReturnsEmptyByDefault(t *testing.T) {
	a := agent.New(agent.ProviderConfig{Run: (&agenttest.Runner{}).Run}, agent.Config{})
	if got := a.Description(); got != "" {
		t.Fatalf("Description() = %q, want empty", got)
	}
}

func TestHistoryProvider_InvokingContext_RequestMessages_SetterRoundtrips(t *testing.T) {
	initial := []*message.Message{message.NewText("Hello")}
	replacement := []*message.Message{message.NewText("New message")}
	invoking := agent.InvokingContext{Messages: initial}

	invoking.Messages = replacement

	if len(invoking.Messages) != 1 || &invoking.Messages[0] != &replacement[0] {
		t.Fatalf("Messages = %v, want the replacement slice", invoking.Messages)
	}
}

func TestHistoryProvider_InvokedContext_ResponseMessages_Roundtrips(t *testing.T) {
	request := []*message.Message{message.NewText("Hello")}
	response := []*message.Message{message.NewText("Response message")}
	response[0].Role = message.RoleAssistant

	invoked := agent.InvokedContext{RequestMessages: request, ResponseMessages: response}

	if len(invoked.ResponseMessages) != 1 || &invoked.ResponseMessages[0] != &response[0] {
		t.Fatalf("ResponseMessages = %v, want the supplied slice", invoked.ResponseMessages)
	}
}

func TestHistoryProvider_InvokedContext_InvokeException_Roundtrips(t *testing.T) {
	request := []*message.Message{message.NewText("Hello")}
	failure := errors.New("Test exception")

	invoked := agent.InvokedContext{RequestMessages: request, Err: failure}

	if invoked.Err != failure {
		t.Fatalf("Err = %v, want the supplied error", invoked.Err)
	}
}

func TestHistoryProvider_InvokingContext_Session_ReturnsConstructorValue(t *testing.T) {
	session := agenttest.CreateSession()
	invoking := agent.InvokingContext{
		Messages: []*message.Message{message.NewText("Hello")},
		Options:  []agent.Option{agent.WithSession(session)},
	}

	if got, ok := agent.GetOption(invoking.Options, agent.WithSession); !ok || got != session {
		t.Fatalf("session = (%p, %v), want %p", got, ok, session)
	}
}

func TestHistoryProvider_InvokingContext_Session_CanBeNil(t *testing.T) {
	invoking := agent.InvokingContext{
		Messages: []*message.Message{message.NewText("Hello")},
		Options:  []agent.Option{agent.WithSession(nil)},
	}

	if got, ok := agent.GetOption(invoking.Options, agent.WithSession); !ok || got != nil {
		t.Fatalf("session = (%p, %v), want nil", got, ok)
	}
}

func TestHistoryProvider_InvokedContext_Session_ReturnsConstructorValue(t *testing.T) {
	session := agenttest.CreateSession()
	invoked := agent.InvokedContext{
		RequestMessages:  []*message.Message{message.NewText("Hello")},
		ResponseMessages: []*message.Message{},
		Options:          []agent.Option{agent.WithSession(session)},
	}

	if got, ok := agent.GetOption(invoked.Options, agent.WithSession); !ok || got != session {
		t.Fatalf("session = (%p, %v), want %p", got, ok, session)
	}
}

func TestHistoryProvider_InvokedContext_Session_CanBeNil(t *testing.T) {
	invoked := agent.InvokedContext{
		RequestMessages:  []*message.Message{message.NewText("Hello")},
		ResponseMessages: []*message.Message{},
		Options:          []agent.Option{agent.WithSession(nil)},
	}

	if got, ok := agent.GetOption(invoked.Options, agent.WithSession); !ok || got != nil {
		t.Fatalf("session = (%p, %v), want nil", got, ok)
	}
}

func TestInMemoryHistoryProvider_InvokedWithEmptyMessagesDoesNotChangeHistory(t *testing.T) {
	session := agenttest.CreateSession()
	provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	messages := []*message.Message{}

	if err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: messages, ResponseMessages: []*message.Message{},
		Options: []agent.Option{agent.WithSession(session)},
	}); err != nil {
		t.Fatal(err)
	}

	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("history = %v, want empty", history)
	}
}
