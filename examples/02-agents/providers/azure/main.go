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
	"github.com/microsoft/agent-framework-go/middleware"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
var deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple agent run.",
	"Model", deployment,
)

func main() {
	// Create a token credential using Azure Identity.
	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	// Create Azure OpenAI agent
	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions: "You are good at telling jokes.", Name: "Joker",
				Middlewares: []middleware.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	// Invoke the agent and output the text result.
	resp, err := a.RunText(context.Background(), "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
