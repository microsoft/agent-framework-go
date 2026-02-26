// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use an Agent as a function tool.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = os.Getenv("AZURE_OPENAI_API_VERSION")
var apiKey = os.Getenv("AZURE_OPENAI_API_KEY")

var logger = demo.NewLogger(
	"Agent As Function Tool",
	"Demonstrates how to create and use an Agent as a function tool.",
	"Model", deployment,
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Create Azure OpenAI agent and provide the function tool.
	weatherAgent := openaichat.NewAgent(openaichat.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithAPIKey(apiKey),
		),
		Model: deployment,
		Agent: agent.Config{
			Instructions: "You answer questions about the weather.",
			Name:         "WeatherAgent",
			Description:  "An agent that answers questions about the weather.",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
			RunOptions: []agentopt.Option{
				agentopt.Tool(weatherTool),
			},
		},
	})

	// Create the main Azure OpenAI agent, and provide the weather agent as a function tool.
	// Note that the main agent is instructed to respond in French.
	a := openaichat.NewAgent(openaichat.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithAPIKey(apiKey),
		),
		Model: deployment,
		Agent: agent.Config{
			Instructions: "You are a helpful assistant who responds in French.",
			RunOptions: []agentopt.Option{
				agentopt.Tool(weatherAgent.AsFuncTool()),
			},
		},
	})

	resp, err := a.RunText("What is the weather like in Amsterdam?").Collect(context.Background())
	demo.Response(resp, err)
}
