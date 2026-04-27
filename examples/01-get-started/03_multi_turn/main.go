// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
)

var logger = demo.NewLogger(
	"Multi-Turn Conversation",
	"This sample shows how to create and use a simple AI agent with a multi-turn conversation.",
	"Model", deployment,
)

func main() {
	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	// Create Azure OpenAI agent.
	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions: "You are good at telling jokes.", Name: "Joker",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()

	// Invoke the agent with a multi-turn conversation, where the context is preserved in the session object.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)
	resp, err = a.RunText(ctx, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	// Invoke the agent with a multi-turn conversation and streaming, where the context is preserved in the session object.
	session, err = a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for update, err := range a.RunText(ctx, "Tell me a joke about a pirate.", agent.WithSession(session), agent.Stream(true)) {
		demo.Response(update, err)
	}
	for update, err := range a.RunText(ctx, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agent.WithSession(session), agent.Stream(true)) {
		demo.Response(update, err)
	}
}
