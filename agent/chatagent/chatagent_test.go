// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"context"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

func noopRunFunc(_ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		yield(&message.ResponseUpdate{
			Role:     message.RoleAssistant,
			Contents: []message.Content{&message.TextContent{Text: "response"}},
		}, nil)
	}
}

func TestCreateSession_WithConversationID(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	session, err := a.CreateSession(t.Context(), agentopt.ServiceID("conv-abc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := session.ServiceID; got != "conv-abc" {
		t.Fatalf("expected ConversationID conv-abc, got %q", got)
	}
}

func TestMarshalUnmarshalSession(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	session, err := a.CreateSession(t.Context(), agentopt.ServiceID("conv-test"))
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	data, err := a.MarshalSession(t.Context(), session)
	if err != nil {
		t.Fatalf("failed to marshal session: %v", err)
	}

	restored, err := a.UnmarshalSession(t.Context(), data)
	if err != nil {
		t.Fatalf("failed to unmarshal session: %v", err)
	}
	if got := restored.ServiceID; got != "conv-test" {
		t.Fatalf("expected ConversationID conv-test, got %q", got)
	}
}
