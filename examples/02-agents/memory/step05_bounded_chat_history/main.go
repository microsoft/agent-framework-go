// Copyright (c) Microsoft. All rights reserved.

// This sample archives older chat messages and recalls them from a vector store.
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
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
)

var logger = demo.NewLogger(
	"Bounded Chat History",
	"Demonstrates a four-message window with vector-backed overflow memory.",
	"Model", demo.FoundryModel,
	"Embedding Model", demo.FoundryEmbeddingModel,
)

func main() {
	ctx := context.Background()
	credential := demo.FoundryTokenCredential()
	generator := foundryprovider.NewEmbeddingGenerator(demo.FoundryProjectEndpoint, credential,
		foundryprovider.EmbeddingGeneratorConfig{Model: demo.FoundryEmbeddingModel})
	store := inmemory.New(inmemory.Config{EmbeddingGenerator: embedding.Bind(generator)})

	sessionID := uuid.NewString()
	memories, err := memory.NewHistoryProvider(store.Store, "chathistory-overflow", 3072,
		func(*agent.Session) *memory.State {
			return &memory.State{
				StorageScope: memory.Scope{UserID: new("UID1"), SessionID: &sessionID},
				SearchScope:  &memory.Scope{UserID: new("UID1")},
			}
		}, memory.Config{
			SearchInputMessageFilter: messagefilter.PassThrough,
			// Overflow includes assistant messages, not just external user input.
			StorageInputRequestMessageFilter: messagefilter.PassThrough,
			Logger:                           slog.New(logger),
		})
	if err != nil {
		demo.Panic(err)
	}

	a := foundryprovider.NewAgent(demo.FoundryProjectEndpoint, credential,
		foundryprovider.ModelDeployment(demo.FoundryModel), foundryprovider.AgentConfig{
			Instructions:       "You are a helpful assistant. Answer questions concisely.",
			DisableStoreOutput: true,
			Config: agent.Config{
				Name:            "Assistant",
				HistoryProvider: newBoundedHistoryProvider(4, memories),
				Middlewares:     []agent.Middleware{logger},
			},
		})

	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for _, turn := range []struct{ heading, question string }{
		{"Filling the session window (4 messages max)", "My favorite color is blue."},
		{"", "I have a dog named Max."},
		{"Next exchange will trigger overflow to vector store", "What is the capital of France?"},
		{"Asking about overflowed information", "What is my favorite color?"},
	} {
		if turn.heading != "" {
			fmt.Printf(">> %s\n\n", turn.heading)
		}
		response, err := a.RunText(ctx, turn.question, agent.WithSession(session)).Collect()
		if err != nil {
			demo.Panic(err)
		}
		demo.Response(response, nil)
	}
}
