// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"iter"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Sequential Chain-Only Responses",
	"This sample chains three agents and forwards only each agent's own response to the next.",
)

// The seed prompt is sent to the first agent only. With
// WithChainOnlyAgentResponses(true) every agent forwards just its own response
// to the next stage, so the translator and reviewer never see the seed prompt
// or the full accumulated conversation.
const seedPrompt = "Write a one-line greeting."

func main() {
	writer := newStageAgent("writer", "Writer", func(string) string {
		return "Hello, world!"
	})
	translator := newStageAgent("translator", "Translator", func(string) string {
		return "Bonjour, le monde!"
	})
	reviewer := newStageAgent("reviewer", "Reviewer", func(input string) string {
		return "Approved: " + input
	})

	wf, err := agentworkflow.NewSequentialWorkflowBuilder(writer, translator, reviewer).
		WithName("chain-only-sequential").
		WithChainOnlyAgentResponses(true).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()
	demo.Assistantf("Seed prompt: %s", seedPrompt)

	run, err := inproc.Default.RunStreaming(ctx, wf, []*message.Message{message.NewText(seedPrompt)})
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	emitEvents := true
	if err := run.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		demo.Panic(err)
	}

	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			if outputMessages, ok := e.Output.([]*message.Message); ok {
				demo.Assistant("=== Final workflow output ===")
				for _, msg := range outputMessages {
					demo.Assistantf("  [%s] %s", msg.Role, messageText(msg))
				}
			}
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
}

// newStageAgent returns a deterministic agent that prints the messages it
// received before replying with respond(input). Printing the received messages
// makes the chain-only behavior visible: the translator and reviewer only ever
// receive the single message produced by the previous agent.
func newStageAgent(id, name string, respond func(input string) string) *agent.Agent {
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			demo.Assistantf("=== %s ===", name)
			demo.Assistantf("%s received %d message(s):", name, len(messages))
			for _, msg := range messages {
				demo.Assistantf("  [%s] %s", msg.Role, messageText(msg))
			}
			reply := respond(messageText(messages...))
			demo.Assistantf("%s responds: %s", name, reply)
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: name,
				Contents:   []message.Content{&message.TextContent{Text: reply}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "demo-stage", Run: run},
		agent.Config{ID: id, Name: name, DisableFuncAutoCall: true},
	)
}

// messageText concatenates the text content of the given messages.
func messageText(messages ...*message.Message) string {
	var builder strings.Builder
	for _, msg := range messages {
		for _, content := range msg.Contents {
			if textContent, ok := content.(*message.TextContent); ok {
				builder.WriteString(textContent.Text)
			}
		}
	}
	return builder.String()
}
