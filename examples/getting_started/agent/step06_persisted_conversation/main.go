// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"
	"path/filepath"

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
		RunOptions:   []agentopt.RunOption{middleware.With(logger)}, // for logging agent interactions
	})

	ctx := context.Background()

	// Start a new session for the agent conversation.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with a new session.
	resp, err := a.RunText("Tell me a joke about a pirate.", agentopt.Session(session)).Collect(ctx)
	demo.Response(resp, err)

	// Serialize the session state so it can be stored for later use.
	serializedSession, err := a.MarshalSession(session)
	if err != nil {
		demo.Panic(err)
	}

	// Save the serialized session to a temporary file (for demonstration purposes).
	tmpDir, err := os.MkdirTemp("", "agent_session")
	if err != nil {
		demo.Panic(err)
	}
	tmpPath := filepath.Join(tmpDir, "session.json")
	if err := os.WriteFile(tmpPath, serializedSession, 0644); err != nil {
		demo.Panic(err)
	}

	// Load the serialized session from the temporary file (for demonstration purposes).
	loadedData, err := os.ReadFile(tmpPath)
	if err != nil {
		demo.Panic(err)
	}

	// Deserialize the session state after loading from storage.
	resumedSession, err := a.UnmarshalSession(loadedData)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent again with the resumed session.
	resp, err = a.RunText("Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agentopt.Session(resumedSession)).Collect(ctx)
	demo.Response(resp, err)
}
