// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use a simple Agent with OpenAI as the backend.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
)

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Options{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
	})

	ctx := context.Background()

	// Invoke the agent and output the text result.
	fmt.Println(agent.RunText(ctx, a, "Tell me a joke about a pirate."))

	// Invoke the agent with streaming support.
	for update, err := range agent.RunTextStream(ctx, a, "Tell me a joke about a pirate.") {
		if err != nil {
			panic(err)
		}
		fmt.Println(update)
	}
}
