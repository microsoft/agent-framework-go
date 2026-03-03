// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/tool"
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
}, func(_ tool.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Create the agent, and provide the function tool to the agent.
	a := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-4o-mini",
		Agent: agent.Config{
			Instructions: "You are a helpful assistant",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
			RunOptions: []agentopt.Option{
				agentopt.Tool(weatherTool),
			},
		},
	})

	ctx := context.Background()

	// Non-streaming agent interaction with function tools.
	resp, err := a.RunText(ctx, "What is the weather like in Amsterdam?").Collect()
	demo.Response(resp, err)

	// Invoke the agent with streaming support.
	for update, err := range a.RunText(ctx, "What is the weather like in Amsterdam?", agentopt.Stream(true)) {
		demo.Response(update, err)
	}
}
