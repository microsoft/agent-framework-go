// Copyright (c) Microsoft. All rights reserved.

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
	"Function Tools",
	"Demonstrates how to use function tools.",
	"Model", deployment,
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Create Azure OpenAI agent, and provide the function tool to the agent.
	a := openai.NewChatAgentAzure(openai.ClientConfig{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
	}, chatagent.Config{
		Instructions: "You are a helpful assistant",
		RunOptions: []agentopt.RunOption{
			agentopt.Tool(weatherTool),
			middleware.With(logger), // for logging agent interactions
		},
	})

	ctx := context.Background()

	// Non-streaming agent interaction with function tools.
	resp, err := a.RunText("What is the weather like in Amsterdam?").Collect(ctx)
	demo.Response(resp, err)

	// Invoke the agent with streaming support.
	for update, err := range a.RunText("What is the weather like in Amsterdam?").All(ctx) {
		demo.Response(update, err)
	}
}
