// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
)

var logger = demo.NewLogger(
	"Persisted Conversation",
	"Demonstrates how to persist a conversation to disk and resume it later.",
	"Model", deployment,
)

func main() {
	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	// Create Azure OpenAI agent.
	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions: "You are good at telling jokes.",
				Name:         "Joker",
				Middlewares:  []agent.Middleware{logger}, // for logging agent interactions
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
	serializedSession, err := a.MarshalSession(ctx, session)
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
	resumedSession, err := a.UnmarshalSession(ctx, loadedData)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent again with the resumed session.
	resp, err = a.RunText(ctx, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agent.WithSession(resumedSession)).Collect()
	demo.Response(resp, err)
}
