// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

type assistantPlugin struct{}

var logger = demo.NewLogger(
	"Foundry Plugin-Style Tools",
	"Demonstrates grouping methods as tools for a Foundry agent.",
	"Model", demo.FoundryModel,
)

func (assistantPlugin) tools() []tool.Tool {
	weather := functool.MustNew(functool.Config{
		Name:        "GetWeather",
		Description: "Gets the current weather for a location.",
	}, func(_ context.Context, location string) (string, error) {
		return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
	})
	now := functool.MustNew(functool.Config{
		Name:        "GetCurrentTime",
		Description: "Gets the current time.",
	}, func(context.Context, struct{}) (string, error) {
		return time.Now().Format(time.RFC1123), nil
	})
	return []tool.Tool{weather, now}
}

func main() {
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant with weather and clock tools.",
			Config: agent.Config{
				Name:        "PluginAssistant",
				Middlewares: []agent.Middleware{logger},
				Tools:       assistantPlugin{}.tools(),
			},
		},
	)

	resp, err := a.RunText(context.Background(), "Tell me current time and weather in Seattle.").Collect()
	demo.Response(resp, err)
}
