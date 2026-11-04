// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

const boundedHistoryStateKey = "bounded-chat-history"

func newBoundedHistoryProvider(maxMessages int, memories *memory.HistoryProvider) agent.HistoryProvider {
	if maxMessages <= 0 {
		panic("maxMessages must be positive")
	}
	history := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		StateKey:                       boundedHistoryStateKey,
		StoreInputRequestMessageFilter: messagefilter.PassThrough,
	})
	return agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: boundedHistoryStateKey,
		Provide: func(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			recent, err := history.Invoking(ctx, agent.InvokingContext{Options: invoking.Options})
			if err != nil {
				return nil, err
			}
			result, _, err := memories.Invoking(ctx, invoking)
			if err != nil {
				return nil, err
			}
			var texts []string
			for _, msg := range result {
				if msg != nil && msg.Source.Type == agent.SourceTypeContextProvider && strings.TrimSpace(msg.String()) != "" {
					texts = append(texts, msg.String())
				}
			}
			if len(texts) == 0 {
				return recent, nil
			}
			contextMessage := message.NewText("The following are memories from earlier in this conversation. Use them to inform your responses:\n" +
				strings.Join(texts, "\n"))
			return append([]*message.Message{contextMessage}, recent...), nil
		},
		Store: func(ctx context.Context, invoked agent.InvokedContext) error {
			if err := history.Invoked(ctx, invoked); err != nil {
				return err
			}
			session, _ := agent.GetOption(invoked.Options, agent.WithSession)
			messages, err := session.InMemoryHistory(boundedHistoryStateKey)
			if err != nil {
				return err
			}
			retained, removed := truncateHistory(messages, maxMessages)
			if err := session.SetInMemoryHistory(boundedHistoryStateKey, retained); err != nil {
				return err
			}
			if len(removed) == 0 {
				return nil
			}
			return memories.Invoked(ctx, agent.InvokedContext{
				RequestMessages: removed,
				Options:         invoked.Options,
			})
		},
	})
}

func truncateHistory(messages []*message.Message, maxMessages int) (retained, removed []*message.Message) {
	var system *message.Message
	var conversation []*message.Message
nextMessage:
	for _, msg := range messages {
		if msg.Role == message.RoleSystem {
			if system == nil {
				system = msg
			}
			continue
		}
		for _, content := range msg.Contents {
			switch content.(type) {
			case *message.FunctionCallContent, *message.FunctionResultContent:
				continue nextMessage
			}
		}
		conversation = append(conversation, msg)
	}
	overflow := max(0, len(conversation)-maxMessages)
	removed = conversation[:overflow]
	if system != nil {
		retained = append(retained, system)
	}
	retained = append(retained, conversation[overflow:]...)
	return retained, removed
}
