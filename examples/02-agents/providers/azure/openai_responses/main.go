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

var logger = demo.NewLogger(
	"Azure OpenAI Responses",
	"Demonstrates an Azure OpenAI responses-backed agent.",
	"Model", demo.Deployment,
)

func main() {
	token := demo.AzureTokenCredential()

	client := openai.NewClient(
		option.WithBaseURL(demo.Endpoint),
		azure.WithTokenCredential(token),
	)

	a := openaiprovider.NewResponsesAgent(
		client,
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

	// Create a responses based agent with "store"=false.
	// This means that chat history is managed locally by Agent Framework
	// instead of being stored in the service (default).
	storeDisabledAgent := openaiprovider.NewResponsesAgent(
		openai.NewClient(
			option.WithBaseURL(demo.Endpoint),
			azure.WithTokenCredential(token),
		),
		openaiprovider.AgentConfig{
			Model:              demo.Deployment,
			Instructions:       "You are good at telling jokes.",
			DisableStoreOutput: true,
			Config: agent.Config{
				Name:        "Joker",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	resp, err = storeDisabledAgent.RunText(context.Background(), "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
