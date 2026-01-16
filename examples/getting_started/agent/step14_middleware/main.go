// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use an Agent as a function tool.

package main

import (
	"context"
	"iter"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
)

var logger = demo.NewLogger(
	"Agent As Function Tool",
	"Demonstrates how to create and use an Agent as a function tool.",
	"Model", "gpt-4o-mini",
)

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, chatagent.Config{
		Instructions: "You are an AI assistant that helps people find information.",
		Middlewares:  []middleware.Middleware{logger, middleware.Func(guardrailsMiddleware)},
	})

	demo.Response(agent.RunText(context.Background(), a, "Tell me something that contains the word harmful."))
}

// guardrailsMiddleware enforces guardrails by redacting certain keywords from input and output messages.
func guardrailsMiddleware(next middleware.RunFunc, ctx context.Context, a agent.Agent, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
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
		for update, err := range next(ctx, a, messages, opts...) {
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
