// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to use an Agent with function tools.
// It shows both non-streaming and streaming agent interactions using menu-related tools.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/chatagent/chatclient"
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
	// Create the agent, and provide the function tool to the agent.
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		Instructions: "You are a helpful assistant",
		ChatOptions: &chatclient.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	ctx := context.Background()

	// Non-streaming agent interaction with function tools.
	fmt.Println(agent.RunText(ctx, a, "What is the weather like in Amsterdam?"))

	// Invoke the agent with streaming support.
	for update, err := range agent.RunTextStream(ctx, a, "What is the weather like in Amsterdam?") {
		if err != nil {
			panic(err)
		}
		fmt.Println(update)
	}
}
