// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

func invokeHistoryProvider(provider agent.HistoryProvider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

func invokeHistoryProviderInvoked(provider agent.HistoryProvider, ctx context.Context, requestMessages, responseMessages []*message.Message, options ...agent.Option) error {
	return provider.Invoked(ctx, agent.InvokedContext{RequestMessages: requestMessages, ResponseMessages: responseMessages, Options: options})
}

func TestNewHistoryProvider_CompactsPersistedHistory(t *testing.T) {
	session := agenttest.CreateSession()
	minimumPreservedGroups := 2
	provider := compaction.NewHistoryProvider(compaction.HistoryProviderConfig{
		SourceID: "compaction-history",
		Strategy: &compaction.TruncationStrategy{
			Trigger:                compaction.GroupsExceed(2),
			MinimumPreservedGroups: &minimumPreservedGroups,
		},
	})

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{textMessage(message.RoleUser, "u1")}, []*message.Message{textMessage(message.RoleAssistant, "a1")}, agent.WithSession(session)); err != nil {
		t.Fatalf("store turn 1: %v", err)
	}
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{textMessage(message.RoleUser, "u2")}, []*message.Message{textMessage(message.RoleAssistant, "a2")}, agent.WithSession(session)); err != nil {
		t.Fatalf("store turn 2: %v", err)
	}

	loaded, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{textMessage(message.RoleUser, "u3")}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if got, want := messageTexts(loaded), []string{"a2", "u3"}; !slices.Equal(got, want) {
		t.Fatalf("loaded history = %v, want %v", got, want)
	}

	if got, want := loaded[0].Source, (message.Source{Type: agent.SourceTypeHistoryProvider, ID: "compaction-history"}); got != want {
		t.Fatalf("history source = %#v, want %#v", got, want)
	}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	restored := agenttest.CreateSession()
	if err := json.Unmarshal(data, restored); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}

	var state struct {
		Messages []*message.Message `json:"messages,omitempty"`
	}
	if ok, err := restored.Get("compaction-history", &state); err != nil || !ok {
		t.Fatalf("expected persisted state, ok=%v err=%v", ok, err)
	}
	if got, want := messageTexts(state.Messages), []string{"u2", "a2"}; !slices.Equal(got, want) {
		t.Fatalf("persisted history = %v, want %v", got, want)
	}
}

func TestNewHistoryProvider_FiltersHistoryBeforeSummarization(t *testing.T) {
	session := agenttest.CreateSession()
	minimumPreservedGroups := 2
	secret := textMessage(message.RoleUser, "secret")
	secret.Source = message.Source{Type: agent.SourceTypeContextProvider, ID: "private"}
	var summarized []*message.Message
	provider := compaction.NewHistoryProvider(compaction.HistoryProviderConfig{
		SourceID: "compaction-history",
		StateInitializer: func(*agent.Session) []*message.Message {
			return []*message.Message{
				secret,
				textMessage(message.RoleAssistant, "visible 1"),
				textMessage(message.RoleUser, "visible 2"),
			}
		},
		ProvideOutputMessageFilter: messagefilter.ExternalOnly,
		Strategy: &compaction.SummarizationStrategy{
			Trigger: compaction.GroupsExceed(2),
			Summarizer: compaction.SummarizerFunc(func(_ context.Context, messages []*message.Message) (string, error) {
				summarized = slices.Clone(messages)
				return "visible context", nil
			}),
			MinimumPreservedGroups: &minimumPreservedGroups,
		},
	})

	loaded, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{textMessage(message.RoleUser, "current")}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if got, want := messageTexts(loaded), []string{"[Summary]\nvisible context", "visible 2", "current"}; !slices.Equal(got, want) {
		t.Fatalf("loaded history = %v, want %v", got, want)
	}
	if slices.Contains(messageTexts(summarized), "secret") {
		t.Fatalf("filtered message was summarized: %v", messageTexts(summarized))
	}
}

func TestNewHistoryProvider_LoadsCompactedSummaryAsHistory(t *testing.T) {
	session := agenttest.CreateSession()
	minimumPreservedGroups := 2
	provider := compaction.NewHistoryProvider(compaction.HistoryProviderConfig{
		SourceID: "compaction-history",
		Strategy: &compaction.SummarizationStrategy{
			Trigger:                compaction.GroupsExceed(2),
			Summarizer:             compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) { return "older context", nil }),
			MinimumPreservedGroups: &minimumPreservedGroups,
		},
	})

	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{textMessage(message.RoleUser, "u1")}, []*message.Message{textMessage(message.RoleAssistant, "a1")}, agent.WithSession(session)); err != nil {
		t.Fatalf("store turn 1: %v", err)
	}
	if err := invokeHistoryProviderInvoked(provider, t.Context(), []*message.Message{textMessage(message.RoleUser, "u2")}, []*message.Message{textMessage(message.RoleAssistant, "a2")}, agent.WithSession(session)); err != nil {
		t.Fatalf("store turn 2: %v", err)
	}

	loaded, err := invokeHistoryProvider(provider, t.Context(), []*message.Message{textMessage(message.RoleUser, "u3")}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if got, want := messageTexts(loaded), []string{"[Summary]\nolder context", "a2", "u3"}; !slices.Equal(got, want) {
		t.Fatalf("loaded history = %v, want %v", got, want)
	}
	if got, want := loaded[0].Source, (message.Source{Type: agent.SourceTypeHistoryProvider, ID: "compaction-history"}); got != want {
		t.Fatalf("summary source = %#v, want %#v", got, want)
	}
}
