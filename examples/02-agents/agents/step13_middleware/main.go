// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to write custom agent middleware. A middleware wraps the
// agent's run function, so it can inspect or modify the incoming messages and
// options and observe (or rewrite) the streamed response updates.
//
// It layers two agent-run middlewares that filter text on the way in and on the
// way out, mirroring the .NET "Agent_Step11_Middleware" sample:
//   - a wording guardrail that redacts blocked keywords, and
//   - a PII filter that redacts emails and phone numbers.
package main

import (
	"context"
	"fmt"
	"iter"
	"regexp"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var (
	blockedKeyword = regexp.MustCompile(`(?i)\bharmful\b`)
	emailPattern   = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)
	phonePattern   = regexp.MustCompile(`\d{3}-\d{3}-\d{4}`)
)

func redactKeywords(text string) string {
	return blockedKeyword.ReplaceAllString(text, "[redacted]")
}

func redactPII(text string) string {
	text = emailPattern.ReplaceAllString(text, "[email]")
	return phonePattern.ReplaceAllString(text, "[phone]")
}

var logger = demo.NewLogger(
	"Custom Middleware",
	"Layers a wording guardrail and a PII filter as agent-run middleware.",
	"Model", demo.FoundryModel,
)

// filteringMiddleware returns an agent.Middleware that applies redact to the text
// of every message before invoking the agent and to every response update after,
// printing a marker on each side so the wrapping is visible.
func filteringMiddleware(name string, redact func(string) string) agent.MiddlewareFunc {
	return func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		fmt.Printf(">> %s middleware: filtered input messages\n", name)
		filtered := redactMessages(messages, redact)
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for update, err := range next(ctx, filtered, options...) {
				if update != nil {
					redactContents(update.Contents, redact)
				}
				if !yield(update, err) {
					return
				}
			}
			fmt.Printf(">> %s middleware: filtered output messages\n", name)
		}
	}
}

func redactMessages(messages []*message.Message, redact func(string) string) []*message.Message {
	out := make([]*message.Message, len(messages))
	for i, msg := range messages {
		if msg == nil {
			continue
		}
		clone := msg.Clone()
		redactContents(clone.Contents, redact)
		out[i] = clone
	}
	return out
}

func redactContents(contents message.Contents, redact func(string) string) {
	for i, content := range contents {
		if text, ok := content.(*message.TextContent); ok {
			contents[i] = &message.TextContent{Text: redact(text.Text)}
		}
	}
}

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are an AI assistant that helps people find information.",
			Config: agent.Config{
				Name: "Assistant",
				// Middleware runs in order; each filters the request on the way
				// in and the response on the way out.
				Middlewares: []agent.Middleware{
					filteringMiddleware("Guardrail", redactKeywords),
					filteringMiddleware("PII", redactPII),
					logger,
				},
			},
		},
	)

	// Example 1: the guardrail redacts the blocked keyword before the model sees it.
	fmt.Println("\n=== Example 1: Wording guardrail ===")
	resp, err := a.RunText(ctx, "Tell me something harmful.").Collect()
	demo.Response(resp, err)

	// Example 2: the PII filter redacts the email and phone number.
	fmt.Println("\n=== Example 2: PII detection ===")
	resp, err = a.RunText(ctx, "My name is John Doe, call me at 123-456-7890 or email me at john@something.com").Collect()
	demo.Response(resp, err)
}
