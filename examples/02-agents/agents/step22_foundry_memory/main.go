// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/internal/azaiprojects"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var (
	chatDeployment      = demo.FoundryModel
	memoryStoreName     = cmp.Or(os.Getenv("AZURE_AI_MEMORY_STORE_ID"), "memory-store-sample")
	embeddingDeployment = cmp.Or(os.Getenv("AZURE_AI_EMBEDDING_DEPLOYMENT_NAME"), "text-embedding-ada-002")
)

const memoryScope = "sample-user-123"

var logger = demo.NewLogger(
	"Foundry Memory",
	"Demonstrates Microsoft Foundry agent runs with Microsoft Foundry memory.",
	"Model", chatDeployment,
	"Memory Store", memoryStoreName,
)

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	// Connect Agent Framework's context provider pipeline to a Foundry memory store.
	// The scope isolates memories for this demo user; real apps should use a stable
	// user, tenant, or conversation partition key.
	memoryProvider := foundryprovider.NewMemoryProvider(
		demo.FoundryProjectEndpoint,
		token,
		memoryStoreName,
		func(*agent.Session) string { return memoryScope },
		foundryprovider.MemoryProviderConfig{
			Logger:      slog.New(logger),
			UpdateDelay: 0,
		},
	)

	// Attach the memory provider to a Foundry agent. Before each run, the provider can
	// retrieve relevant Foundry memories and add them as context; after each run, it
	// submits the conversation content for memory extraction.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(chatDeployment),
		foundryprovider.AgentConfig{
			Instructions: "You are a friendly travel assistant. Use known memories about the user when responding, and do not invent details.",
			Config: agent.Config{
				Name:             "TravelAssistantWithFoundryMemory",
				Middlewares:      []agent.Middleware{logger},
				ContextProviders: []agent.ContextProvider{memoryProvider},
			},
		},
	)

	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	setupFoundryMemoryStore(ctx, demo.FoundryProjectEndpoint, token)

	resp, err := a.RunText(ctx, "Hi there! My name is Taylor and I'm planning a hiking trip to Patagonia in November.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	resp, err = a.RunText(ctx, "I'm travelling with my sister and we love finding scenic viewpoints.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	resp, err = a.RunText(ctx, "What do you already know about my upcoming trip?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	newSession, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	resp, err = a.RunText(ctx, "Summarize what you already know about me.", agent.WithSession(newSession)).Collect()
	demo.Response(resp, err)
}

func stringPtr(value string) *string {
	return &value
}

// setupFoundryMemoryStore prepares a sample memory store so the demo can run
// repeatedly. Regular apps should provision Foundry resources outside the app;
// this helper uses Agent Framework's temporary internal Foundry SDK for setup.
func setupFoundryMemoryStore(ctx context.Context, foundryEndpoint string, token azcore.TokenCredential) {
	projectClient, err := azaiprojects.NewClient(foundryEndpoint, token, nil)
	if err != nil {
		demo.Panicf("failed to create Foundry project client: %v", err)
	}
	client := projectClient.NewMemoryStoresClient()

	// Ensure the memory store exists (creates it with the specified models if needed).
	if _, err := client.GetMemoryStore(ctx, memoryStoreName, nil); err != nil {
		demo.Assistant("Setting up Foundry memory store")
		var responseErr *azcore.ResponseError
		if !errors.As(err, &responseErr) || responseErr.StatusCode != 404 {
			demo.Panicf("failed to get memory store: %v", err)
		}

		_, err = client.CreateMemoryStore(ctx, memoryStoreName, &azaiprojects.MemoryStoreDefaultDefinition{
			ChatModel:      &chatDeployment,
			EmbeddingModel: &embeddingDeployment,
		}, &azaiprojects.MemoryStoresClientCreateMemoryStoreOptions{
			Description: stringPtr("Sample memory store for travel assistant"),
		})
		if err != nil {
			demo.Panicf("failed to create memory store with chat deployment %q and embedding deployment %q: %v", chatDeployment, embeddingDeployment, err)
		}
	}

	// Clear any existing memories for this scope to demonstrate fresh behavior.
	if _, err := client.DeleteScope(ctx, memoryStoreName, memoryScope, nil); err != nil {
		var responseErr *azcore.ResponseError
		if !errors.As(err, &responseErr) || responseErr.StatusCode != 404 {
			demo.Panicf("failed to clear stored memories: %v", err)
		}
	}
}
