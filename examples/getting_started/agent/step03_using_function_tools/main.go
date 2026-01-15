// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Function Tools",
	"Demonstrates how to use function tools.",
	"Model", "gpt-4o-mini",
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Create the agent, and provide the function tool to the agent.
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Config{
		Instructions: "You are a helpful assistant",
		Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		RunOptions: []agentopt.RunOption{
			agentopt.Tool(weatherTool),
		},
	})

	ctx := context.Background()

	// Non-streaming agent interaction with function tools.
	demo.Response(agent.RunText(ctx, a, "What is the weather like in Amsterdam?"))

	// Invoke the agent with streaming support.
	for update, err := range agent.RunTextStream(ctx, a, "What is the weather like in Amsterdam?") {
		demo.Response(update, err)
	}
}
