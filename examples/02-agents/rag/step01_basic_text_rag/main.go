// Copyright (c) Microsoft. All rights reserved.

// This sample retrieves text from an in-memory vector store before each agent
// invocation and keeps retrieved context out of retained conversation history.
package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/textsearch"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Basic Text RAG",
	"Demonstrates retrieval from an in-memory vector store with source citations.",
	"Model", demo.FoundryModel,
	"Embedding Model", demo.FoundryEmbeddingModel,
)

func main() {
	ctx := context.Background()
	credential := demo.FoundryTokenCredential()
	generator := foundryprovider.NewEmbeddingGenerator(demo.FoundryProjectEndpoint, credential,
		foundryprovider.EmbeddingGeneratorConfig{Model: demo.FoundryEmbeddingModel})
	store := NewTextSearchStore(generator)
	if err := store.UpsertDocuments(ctx, SampleDocuments()); err != nil {
		demo.Panic(err)
	}
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		credential,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions:       "You are a helpful support specialist for Contoso Outdoors. Answer questions using the provided context and cite the source document when available.",
			DisableStoreOutput: true,
			Config: agent.Config{
				Middlewares:      []agent.Middleware{logger},
				ContextProviders: []agent.ContextProvider{textsearch.NewContextProvider(store.search, textsearch.Config{})},
				HistoryProvider: agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
					StoreInputRequestMessageFilter: messagefilter.NotSourceTypes(
						agent.SourceTypeContextProvider, agent.SourceTypeHistoryProvider,
					),
				}),
			},
		},
	)

	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for i, question := range []struct{ heading, text string }{
		{"Asking about returns", "Hi! I need help understanding the return policy."},
		{"Asking about shipping", "How long does standard shipping usually take?"},
		{"Asking about product care", "What is the best way to maintain the TrailRunner tent fabric?"},
	} {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf(">> %s\n\n", question.heading)
		response, err := a.RunText(ctx, question.text, agent.WithSession(session)).Collect()
		if err != nil {
			demo.Panic(err)
		}
		demo.Response(response, nil)
	}
}

func (s *TextSearchStore) search(ctx context.Context, query string) ([]textsearch.Result, error) {
	documents, err := s.Search(ctx, query, 1)
	if err != nil {
		return nil, err
	}
	results := make([]textsearch.Result, len(documents))
	for i, document := range documents {
		results[i] = textsearch.Result{
			SourceName: document.SourceName, SourceLink: document.SourceLink,
			Text: document.Text, RawRepresentation: document,
		}
	}
	return results, nil
}
