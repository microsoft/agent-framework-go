// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use an Agent as a function tool.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Create the agent and provide the function tool.
	weatherAgent := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, chatagent.Options{
		Instructions: "You answer questions about the weather.",
		Name:         "WeatherAgent",
		Description:  "An agent that answers questions about the weather.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	// Create the main agent, and provide the weather agent as a function tool.
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, chatagent.Options{
		Instructions: "You are a helpful assistant who responds in French.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{agent.FuncTool(weatherAgent, nil)},
		},
	})

	fmt.Println(agent.RunText(context.Background(), a, "What is the weather like in Amsterdam?"))
}
