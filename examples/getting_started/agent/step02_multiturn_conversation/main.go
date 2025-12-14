// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use a simple Agent with a multi-turn conversation.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
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

	// Invoke the agent with a multi-turn conversation, where the context is preserved in the thread object.
	thread := a.NewThread()
	fmt.Println(agent.RunText(ctx, a, "Tell me a joke about a pirate.", agentopt.Thread(thread)))
	fmt.Println(agent.RunText(ctx, a, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Thread(thread)))

	// Invoke the agent with a multi-turn conversation and streaming, where the context is preserved in the thread object.
	thread2 := a.NewThread()
	for update, err := range agent.RunTextStream(ctx, a, "Tell me a joke about a pirate.", agentopt.Thread(thread2)) {
		if err != nil {
			panic(err)
		}
		fmt.Println(update)
	}
	for update, err := range agent.RunTextStream(ctx, a, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Thread(thread2)) {
		if err != nil {
			panic(err)
		}
		fmt.Println(update)
	}
}
