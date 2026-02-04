// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use an Agent as a function tool.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
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
	weatherAgent := openai.NewChatAgentAzure(openai.ClientConfig{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
	}, chatagent.Config{
		Instructions: "You answer questions about the weather.",
		Name:         "WeatherAgent",
		Description:  "An agent that answers questions about the weather.",
		RunOptions: []agentopt.RunOption{
			agentopt.Tool(weatherTool),
			middleware.With(logger), // for logging agent interactions
		},
	})

	// Create the main Azure OpenAI agent, and provide the weather agent as a function tool.
	// Note that the main agent is instructed to respond in French.
	a := openai.NewChatAgentAzure(openai.ClientConfig{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
	}, chatagent.Config{
		Instructions: "You are a helpful assistant who responds in French.",
		RunOptions: []agentopt.RunOption{
			agentopt.Tool(weatherAgent.AsFuncTool()),
		},
	})

	resp, err := a.RunText("What is the weather like in Amsterdam?").Collect(context.Background())
	demo.Response(resp, err)
}
