// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = os.Getenv("AZURE_OPENAI_API_VERSION")
var apiKey = os.Getenv("AZURE_OPENAI_API_KEY")

var logger = demo.NewLogger(
	"Persisted Conversation",
	"Demonstrates how to persist a conversation to disk and resume it later.",
	"Model", deployment,
)

func main() {
	// Create Azure OpenAI agent.
	a := openaichat.NewAgent(openaichat.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithAPIKey(apiKey),
		),
		Model: deployment,
		Agent: agent.Config{
			Instructions: "You are good at telling jokes.",
			Name:         "Joker",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		},
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
	if err := os.WriteFile(tmpPath, serializedSession, 0644); err != nil {
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
	resp, err = a.RunText("Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agentopt.Session(resumedSession)).Collect(ctx)
	demo.Response(resp, err)
}
