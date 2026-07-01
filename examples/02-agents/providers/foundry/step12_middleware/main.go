// Copyright (c) Microsoft. All rights reserved.

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

var logger = demo.NewLogger(
	"Foundry Middleware",
	"Demonstrates custom middleware with a Foundry agent.",
	"Model", demo.FoundryModel,
)

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
				Middlewares: []agent.Middleware{
					guardrailMiddleware{},
					logger,
				},
			},
		},
	)

	resp, err := a.RunText(ctx, "Tell me something harmful.").Collect()
	demo.Response(resp, err)

	resp, err = a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}

type guardrailMiddleware struct{}

func (guardrailMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	for _, msg := range messages {
		if strings.Contains(strings.ToLower(msg.String()), "harmful") {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: message.Contents{
						&message.TextContent{Text: "I can't help with harmful requests."},
					},
				}, nil)
			}
		}
	}
	return next(ctx, messages, options...)
}
