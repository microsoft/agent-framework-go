// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/filememory"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"File Memory",
	"This sample saves travel preferences in one conversation and recalls them "+
		"in a new conversation with no shared chat history.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()
	memoryRoot, err := filepath.Abs("agent-memory")
	if err != nil {
		demo.Panic(err)
	}

	// A stable per-user folder shares memories across that user's sessions.
	// A unique folder per session would isolate those memories instead.
	const workingFolder = "users/UID1"
	memory, err := filememory.New(memoryRoot, filememory.Config{
		StateInitializer: func(*agent.Session) filememory.State {
			return filememory.State{WorkingFolder: workingFolder}
		},
	})
	if err != nil {
		demo.Panic(err)
	}
	defer func() {
		if err := memory.Close(); err != nil {
			demo.Panic(err)
		}
	}()
	fmt.Printf("Memory files: %s\n\n", filepath.Join(memoryRoot, workingFolder))

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful travel assistant. Remember what the user tells you " +
				"about themselves so that you can give better recommendations later.",
			Config: agent.Config{
				Name:             "TravelAssistant",
				Middlewares:      []agent.Middleware{logger},
				ContextProviders: []agent.ContextProvider{memory},
			},
		},
	)

	ctx := context.Background()
	firstSession, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	first, err := a.RunText(ctx,
		"I'm vegetarian and I always travel with my dog. Please remember this for future trips.",
		agent.WithSession(firstSession)).Collect()
	if err != nil {
		demo.Panic(err)
	}
	demo.Response(first, nil)

	fmt.Println("Memory files on disk:")
	entries, err := os.ReadDir(filepath.Join(memoryRoot, workingFolder))
	if err != nil {
		demo.Panic(err)
	}
	for _, entry := range entries {
		fmt.Println(entry.Name())
	}
	fmt.Println()

	// The new session has no history from the first conversation. The memory
	// provider supplies the index and tools to recall the stored preferences.
	secondSession, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	second, err := a.RunText(ctx,
		"Suggest a hotel and a restaurant for my trip to Paris next week.",
		agent.WithSession(secondSession)).Collect()
	if err != nil {
		demo.Panic(err)
	}
	demo.Response(second, nil)
}
