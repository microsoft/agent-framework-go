// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/agenttool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Foundry Agent As Function Tool",
	"Demonstrates using one Foundry-backed agent as a function tool for another.",
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

	weatherAgent := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You answer questions about the weather.",
			Config: agent.Config{
				Name:        "WeatherAgent",
				Description: "An agent that answers questions about the weather.",
				Middlewares: []agent.Middleware{logger},
				Tools:       []tool.Tool{weatherTool},
			},
		},
	)

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant who responds in French.",
			Config: agent.Config{
				Tools: []tool.Tool{agenttool.New(weatherAgent, agenttool.Config{})},
			},
		},
	)

	resp, err := a.RunText(context.Background(), "What is the weather like in Amsterdam?").Collect()
	demo.Response(resp, err)
}
