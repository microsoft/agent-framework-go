// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
)

var logger = demo.NewLogger(
	"Persisted Conversation",
	"Demonstrates how to persist a conversation to disk and resume it later.",
	"Model", "gpt-4o-mini",
)

func main() {
	// Create the agent.
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Config{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
		Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
	})

	ctx := context.Background()

	// Start a new thread for the agent conversation.
	thread, err := a.NewThread(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with a new thread.
	demo.Response(agent.RunText(ctx, a, "Tell me a joke about a pirate.", agentopt.Thread(thread)))

	// Serialize the thread state so it can be stored for later use.
	serializedThread, err := thread.MarshalBinary()
	if err != nil {
		demo.Panic(err)
	}

	// Save the serialized thread to a temporary file (for demonstration purposes).
	tmpDir, err := os.MkdirTemp("", "agent_thread")
	if err != nil {
		demo.Panic(err)
	}
	tmpPath := filepath.Join(tmpDir, "thread.json")
	if err := os.WriteFile(tmpPath, serializedThread, 0644); err != nil {
		demo.Panic(err)
	}

	// Load the serialized thread from the temporary file (for demonstration purposes).
	loadedData, err := os.ReadFile(tmpPath)
	if err != nil {
		demo.Panic(err)
	}

	// Deserialize the thread state after loading from storage.
	resumedThread, err := a.UnmarshalThread(loadedData)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent again with the resumed thread.
	demo.Response(agent.RunText(ctx, a, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agentopt.Thread(resumedThread)))
}
