// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"iter"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
)

var logger = demo.NewLogger(
	"Custom Middleware",
	"Demonstrates an agent with custom middleware to enforce guardrails.",
	"Model", "gpt-4o-mini",
)

func main() {
	a := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-5-nano",
		Agent: agent.Config{
			Instructions: "You are an AI assistant that helps people find information.",
			Middlewares:  []middleware.Middleware{logger, middleware.Func(guardrailsMiddleware)},
		},
	})

	resp, err := a.RunText(context.Background(), "Tell me something that contains the word harmful.").Collect()
	demo.Response(resp, err)
}

// guardrailsMiddleware enforces guardrails by redacting certain keywords from input and output messages.
func guardrailsMiddleware(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	redact := func(contents message.Contents) message.Contents {
		// Simple redaction logic for demonstration purposes.
		for _, c := range contents {
			switch content := c.(type) {
			case *message.TextContent:
				content.Text = strings.ReplaceAll(content.Text, "harmful", "[REDACTED: Forbidden content]")
			}
		}
		return contents
	}
	return func(yield func(*message.ResponseUpdate, error) bool) {
		// Redact keywords from input messages
		for _, msg := range messages {
			msg.Contents = redact(msg.Contents)
		}
		// Call the next middleware/agent in the chain.
		for update, err := range next(ctx, messages, opts...) {
			if err == nil && update != nil {
				// Redact keywords from output messages
				update.Contents = redact(update.Contents)
			}
			if !yield(update, err) {
				return
			}
		}
	}
}
