// Copyright (c) Microsoft. All rights reserved.

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
	"Function Tools",
	"Demonstrates how to use function tools.",
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
	a := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You are a helpful assistant",
			Config: agent.Config{
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
				Tools:       []tool.Tool{weatherTool},
			},
		},
	)

	ctx := context.Background()

	// Non-streaming agent interaction with function tools.
	resp, err := a.RunText(ctx, "What is the weather like in Amsterdam?").Collect()
	demo.Response(resp, err)

	// Invoke the agent with streaming support.
	for update, err := range a.RunText(ctx, "What is the weather like in Amsterdam?", agent.Stream(true)) {
		demo.Response(update, err)
	}
}
