// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use an Agent as a function tool.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/agenttool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

var logger = demo.NewLogger(
	"Agent As Function Tool",
	"Demonstrates how to create and use an Agent as a function tool.",
	"Model", deployment,
)

var weatherTool = functool.MustNew(functool.Config{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ tool.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	// Create Azure OpenAI agent with the function tool.
	weatherAgent := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You answer questions about the weather.",
			Config: agent.Config{
				Name:        "WeatherAgent",
				Description: "An agent that answers questions about the weather.",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
				Tools:       []tool.Tool{weatherTool},
			},
		},
	)

	// Create the main Azure OpenAI agent with the weather agent as a function tool.
	// Note that the main agent is instructed to respond in French.
	a := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You are a helpful assistant who responds in French.",
			Config: agent.Config{
				Tools: []tool.Tool{agenttool.New(weatherAgent, agenttool.Config{})},
			},
		},
	)

	resp, err := a.RunText(context.Background(), "What is the weather like in Amsterdam?").Collect()
	demo.Response(resp, err)
}
