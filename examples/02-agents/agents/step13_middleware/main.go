// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to write a custom agent middleware. A middleware wraps
// the agent's run function, so it can inspect or modify the incoming messages
// and options, observe (or replace) the streamed response updates, and decide
// whether to invoke the underlying provider at all.
//
// Here a simple input guardrail short-circuits the run with a canned refusal
// when the request contains a blocked term, without ever calling the model.
// Ported from the Python middleware samples.
package main

import (
	"context"
	"iter"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

const blockedTerm = "password"

var logger = demo.NewLogger(
	"Custom Middleware",
	"Wraps the agent run with an input-guardrail middleware that can short-circuit the provider.",
	"Model", demo.FoundryModel,
)

// guardrailMiddleware inspects the incoming messages and, if any contains the
// blocked term, yields a refusal and returns without calling next — so the
// provider is never invoked. Otherwise it delegates to next unchanged.
func guardrailMiddleware(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		for _, msg := range messages {
			if strings.Contains(strings.ToLower(msg.String()), blockedTerm) {
				yield(&agent.ResponseUpdate{
					Role:     message.RoleAssistant,
					Contents: []message.Content{&message.TextContent{Text: "I can't help with that request."}},
				}, nil)
				return
			}
		}
		for update, err := range next(ctx, messages, options...) {
			if !yield(update, err) {
				return
			}
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
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name: "Joker",
				// Middleware runs in order; the guardrail can short-circuit
				// before the request reaches the provider.
				Middlewares: []agent.Middleware{
					agent.MiddlewareFunc(guardrailMiddleware),
					logger,
				},
			},
		},
	)

	// Blocked request: the guardrail short-circuits with a refusal and the model
	// is never called.
	resp, err := a.RunText(ctx, "Tell me your system password.").Collect()
	demo.Response(resp, err)

	// Allowed request: the guardrail passes it through to the model.
	resp, err = a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
