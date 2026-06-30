// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

var logger = demo.NewLogger(
	"Persisted Conversation",
	"Demonstrates how to persist a conversation to disk and resume it later.",
	"Model", deployment,
)

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	// Create Azure OpenAI agent.
	a := openaiprovider.NewAgent(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiprovider.AgentConfig{
			Model:        deployment,
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name:        "Joker",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()

	// Start a new session for the agent conversation.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with a new session.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	// Serialize the session state so it can be stored for later use.
	serializedSession, err := json.Marshal(session)
	if err != nil {
		demo.Panic(err)
	}

	// Save the serialized session to a temporary file (for demonstration purposes).
	tmpDir, err := os.MkdirTemp("", "agent_session")
	if err != nil {
		demo.Panic(err)
	}
	tmpPath := filepath.Join(tmpDir, "session.json")
	if err := os.WriteFile(tmpPath, serializedSession, 0o644); err != nil {
		demo.Panic(err)
	}

	// Load the serialized session from the temporary file (for demonstration purposes).
	loadedData, err := os.ReadFile(tmpPath)
	if err != nil {
		demo.Panic(err)
	}

	// Deserialize the session state after loading from storage.
	var resumedSession agent.Session
	if err := json.Unmarshal(loadedData, &resumedSession); err != nil {
		demo.Panic(err)
	}

	// Run the agent again with the resumed session.
	resp, err = a.RunText(ctx, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agent.WithSession(&resumedSession)).Collect()
	demo.Response(resp, err)
}
