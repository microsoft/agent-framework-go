// Copyright (c) Microsoft. All rights reserved.

// This sample uses a custom Qdrant schema to retrieve Agent Framework
// documentation, including recent conversation context in each search.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/textsearch"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/qdrant/go-client/qdrant"
)

var logger = demo.NewLogger(
	"Custom Vector Store RAG",
	"Demonstrates retrieval from Qdrant using a custom documentation schema.",
	"Model", demo.FoundryModel,
	"Embedding Model", demo.FoundryEmbeddingModel,
)

func main() {
	if err := run(context.Background()); err != nil {
		demo.Panic(err)
	}
}

func run(ctx context.Context) (err error) {
	credential := demo.FoundryTokenCredential()
	qdrantClient, err := qdrant.NewClient(&qdrant.Config{Host: "localhost"})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, qdrantClient.Close()) }()
	store := &documentationStore{
		client: qdrantClient,
		generator: foundryprovider.NewEmbeddingGenerator(demo.FoundryProjectEndpoint, credential,
			foundryprovider.EmbeddingGeneratorConfig{Model: demo.FoundryEmbeddingModel}),
	}
	// Like the upstream sample, this resets the "documentation" collection.
	if err := store.reset(ctx); err != nil {
		return err
	}
	for _, source := range []struct{ url, name string }{
		{
			"https://raw.githubusercontent.com/MicrosoftDocs/semantic-kernel-docs/refs/heads/main/agent-framework/overview/index.md",
			"Microsoft Agent Framework Overview",
		},
		{
			"https://raw.githubusercontent.com/MicrosoftDocs/semantic-kernel-docs/refs/heads/main/agent-framework/migration-guide/from-semantic-kernel/index.md",
			"Semantic Kernel to Microsoft Agent Framework Migration Guide",
		},
	} {
		if err := store.uploadMarkdown(ctx, source.url, source.name); err != nil {
			return err
		}
	}
	provider := textsearch.NewContextProvider(store.search, textsearch.Config{
		SearchTime: textsearch.BeforeInvoke, RecentMessageMemoryLimit: 5,
	})
	a := foundryprovider.NewAgent(demo.FoundryProjectEndpoint, credential, foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions:       "You are a helpful support specialist for the Microsoft Agent Framework. Answer questions using the provided context and cite the source document when available. Keep responses brief.",
			DisableStoreOutput: true,
			Config: agent.Config{
				Middlewares:      []agent.Middleware{logger},
				ContextProviders: []agent.ContextProvider{provider},
				HistoryProvider: agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
					StoreInputRequestMessageFilter: messagefilter.NotSourceTypes(
						agent.SourceTypeHistoryProvider, agent.SourceTypeContextProvider,
					),
				}),
			},
		},
	)
	session, err := a.CreateSession(ctx)
	if err != nil {
		return err
	}
	for i, question := range []struct{ heading, text string }{
		{"Asking about SK sessions", "Hi! How do I create a thread/session in Semantic Kernel?"},
		{"Asking about AF sessions", "and in Agent Framework?"},
		{"Contrasting Approaches", "Please contrast the two approaches"},
		{"Asking about ancestry", "What are the predecessors to the Agent Framework?"},
	} {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf(">> %s\n\n", question.heading)
		response, err := a.RunText(ctx, question.text, agent.WithSession(session)).Collect()
		if err != nil {
			return err
		}
		demo.Response(response, nil)
	}
	return nil
}
