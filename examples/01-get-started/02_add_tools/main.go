// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Add Tools",
	"This sample demonstrates how to use an AI agent with function tools.",
	"Model", demo.FoundryModel,
)

var weatherTool = functool.MustNew(functool.Config{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	token := demo.FoundryTokenCredential()

	// Create Microsoft Foundry agent with the function tool.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
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
