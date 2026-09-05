// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func BenchmarkAgentRunNoUpdates(b *testing.B) {
	benchmarkAgentRun(b, nil)
}

func BenchmarkAgentRunSingleTextUpdate(b *testing.B) {
	benchmarkAgentRun(b, benchmarkTextUpdates(1))
}

func BenchmarkAgentRunTenTextUpdates(b *testing.B) {
	benchmarkAgentRun(b, benchmarkTextUpdates(10))
}

func benchmarkAgentRun(b *testing.B, updates []*agent.ResponseUpdate) {
	a := newBenchmarkAgent(updates)
	session := &agent.Session{}
	session.SetServiceID("benchmark-session")
	options := []agent.Option{agent.WithSession(session)}
	messages := []*message.Message{message.NewText("hello")}

	b.ReportAllocs()
	for b.Loop() {
		for _, err := range a.Run(b.Context(), messages, options...) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func newBenchmarkAgent(updates []*agent.ResponseUpdate) *agent.Agent {
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, update := range updates {
				if !yield(update, nil) {
					return
				}
			}
		}
	}
	return agent.New(agent.ProviderConfig{ProviderName: "fake", Run: run}, agent.Config{
		ID:   "benchmark-agent",
		Name: "Benchmark Agent",
	})
}

func benchmarkTextUpdates(count int) []*agent.ResponseUpdate {
	updates := make([]*agent.ResponseUpdate, count)
	for i := range updates {
		updates[i] = &agent.ResponseUpdate{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.TextContent{Text: "chunk"},
			},
		}
	}
	return updates
}
