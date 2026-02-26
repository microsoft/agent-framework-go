// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = os.Getenv("AZURE_OPENAI_API_VERSION")
var apiKey = os.Getenv("AZURE_OPENAI_API_KEY")

var logger = demo.NewLogger(
	"Multi-Turn Conversation",
	"Demonstrates how to preserve conversation context with sessions.",
	"Model", deployment,
)

func main() {
	// Create Azure OpenAI agent.
	a := openaichat.NewAgent(openaichat.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithAPIKey(apiKey),
		),
		Model: deployment,
		Agent: agent.Config{
			Instructions: "You are good at telling jokes.",
			Name:         "Joker",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		},
	})

	ctx := context.Background()

	// Invoke the agent with a multi-turn conversation, where the context is preserved in the session object.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText("Tell me a joke about a pirate.", agentopt.Session(session)).Collect(ctx)
	demo.Response(resp, err)
	resp, err = a.RunText("Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Session(session)).Collect(ctx)
	demo.Response(resp, err)

	// Invoke the agent with a multi-turn conversation and streaming, where the context is preserved in the session object.
	session2, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for update, err := range a.RunText("Tell me a joke about a pirate.", agentopt.Session(session2)).All(ctx) {
		demo.Response(update, err)
	}
	for update, err := range a.RunText("Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Session(session2)).All(ctx) {
		demo.Response(update, err)
	}
}
