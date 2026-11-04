// Copyright (c) Microsoft. All rights reserved.

// This sample recalls chat messages from earlier sessions for the same user.
package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
)

var logger = demo.NewLogger(
	"Chat History Memory",
	"Demonstrates scoped recall across separate conversations.",
	"Model", demo.FoundryModel,
	"Embedding Model", demo.FoundryEmbeddingModel,
)

func main() {
	ctx := context.Background()
	credential := demo.FoundryTokenCredential()
	generator := foundryprovider.NewEmbeddingGenerator(demo.FoundryProjectEndpoint, credential,
		foundryprovider.EmbeddingGeneratorConfig{Model: demo.FoundryEmbeddingModel})
	store := inmemory.New(inmemory.Config{EmbeddingGenerator: embedding.Bind(generator)})

	memories, err := memory.NewHistoryProvider(store.Store, "chathistory", 3072,
		func(*agent.Session) *memory.State {
			return &memory.State{
				StorageScope: memory.Scope{UserID: new("UID1"), SessionID: new(uuid.NewString())},
				SearchScope:  &memory.Scope{UserID: new("UID1")},
			}
		}, memory.Config{Logger: slog.New(logger)})
	if err != nil {
		demo.Panic(err)
	}

	a := foundryprovider.NewAgent(demo.FoundryProjectEndpoint, credential,
		foundryprovider.ModelDeployment(demo.FoundryModel), foundryprovider.AgentConfig{
			Instructions:       "You are good at telling jokes.",
			DisableStoreOutput: true,
			Config: agent.Config{
				Name:             "Joker",
				ContextProviders: []agent.ContextProvider{memories},
				Middlewares:      []agent.Middleware{logger},
			},
		})

	for i, question := range []string{
		"I like jokes about Pirates. Tell me a joke about a pirate.",
		"Tell me a joke that I might like.",
	} {
		session, err := a.CreateSession(ctx)
		if err != nil {
			demo.Panic(err)
		}
		fmt.Printf(">> Session %d\n\n", i+1)
		response, err := a.RunText(ctx, question, agent.WithSession(session)).Collect()
		if err != nil {
			demo.Panic(err)
		}
		demo.Response(response, nil)
	}
}
