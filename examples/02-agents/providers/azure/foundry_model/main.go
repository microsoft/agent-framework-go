// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

const azureAIResourceScope = "https://ai.azure.com/.default"

var logger = demo.NewLogger(
	"Azure Foundry Model",
	"Demonstrates a model hosted in Microsoft Foundry through the OpenAI-compatible API.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.AzureTokenCredential()

	a := openaiprovider.NewAgent(
		openai.NewClient(
			option.WithBaseURL(demo.Endpoint),
			azure.WithTokenCredential(token, azure.WithTokenCredentialScopes([]string{azureAIResourceScope})),
		),
		openaiprovider.AgentConfig{
			Model:        demo.FoundryModel,
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name:        "Joker",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	resp, err := a.RunText(context.Background(), "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
