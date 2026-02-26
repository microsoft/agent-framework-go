// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use an Agent as a function tool.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Agent As Function Tool",
	"Demonstrates how to create and use an Agent as a function tool.",
	"Model", "gpt-4o-mini",
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Create the agent and provide the function tool.
	weatherAgent := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-5-nano",
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

	// Create the main agent, and provide the weather agent as a function tool.
	a := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-5-nano",
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
