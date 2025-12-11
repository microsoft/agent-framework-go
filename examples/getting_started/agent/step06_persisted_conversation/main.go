// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use a simple Agent with a conversation that can be persisted to disk.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
)

func main() {
	// Create the agent.
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Options{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
	})

	ctx := context.Background()

	// Start a new thread for the agent conversation.
	thread := a.NewThread()

	// Run the agent with a new thread.
	fmt.Println(agent.RunText(ctx, a, "Tell me a joke about a pirate.", agent.WithThread(thread)))

	// Serialize the thread state to JSON, so it can be stored for later use.
	serializedThread, err := json.Marshal(thread)
	if err != nil {
		panic(err)
	}

	// Save the serialized thread to a temporary file (for demonstration purposes).
	tmpDir, err := os.MkdirTemp("", "agent_thread")
	if err != nil {
		panic(err)
	}
	tmpPath := filepath.Join(tmpDir, "thread.json")
	if err := os.WriteFile(tmpPath, serializedThread, 0644); err != nil {
		panic(err)
	}

	// Load the serialized thread from the temporary file (for demonstration purposes).
	loadedData, err := os.ReadFile(tmpPath)
	if err != nil {
		panic(err)
	}

	// Deserialize the thread state after loading from storage.
	resumedThread, err := a.UnmarshalThread(loadedData)
	if err != nil {
		panic(err)
	}

	// Run the agent again with the resumed thread.
	fmt.Println(agent.RunText(ctx, a, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agent.WithThread(resumedThread)))
}
