// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Foundry Persisted Conversations",
	"Demonstrates persisting a local Agent Framework session for a Foundry agent.",
	"Model", demo.FoundryModel,
)

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name:        "Joker",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	data, err := json.Marshal(session)
	if err != nil {
		demo.Panic(err)
	}
	tmpDir, err := os.MkdirTemp("", "foundry_agent_session")
	if err != nil {
		demo.Panic(err)
	}
	path := filepath.Join(tmpDir, "session.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		demo.Panic(err)
	}

	loaded, err := os.ReadFile(path)
	if err != nil {
		demo.Panic(err)
	}
	var resumed agent.Session
	if err := json.Unmarshal(loaded, &resumed); err != nil {
		demo.Panic(err)
	}

	resp, err = a.RunText(ctx, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agent.WithSession(&resumed)).Collect()
	demo.Response(resp, err)
}
