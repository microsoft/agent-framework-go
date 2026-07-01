// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var logger = demo.NewLogger(
	"Azure OpenAI Responses",
	"Demonstrates an Azure OpenAI responses-backed agent.",
	"Model", demo.Deployment,
)

func main() {
	token := demo.AzureTokenCredential()

	a := openaiprovider.NewResponsesAgent(
		openai.NewClient(
			azure.WithEndpoint(demo.Endpoint, demo.APIVersion),
			azure.WithTokenCredential(token),
		),
		openaiprovider.AgentConfig{
			Model:        demo.Deployment,
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
