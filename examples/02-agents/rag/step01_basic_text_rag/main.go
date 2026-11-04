// Copyright (c) Microsoft. All rights reserved.

// This sample retrieves text from an in-memory vector store before each agent
// invocation and keeps retrieved context out of retained conversation history.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/textsearch"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
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
	collection, err := inmemory.NewCollection[uuid.UUID, TextSearchDocument]("documents", &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
	if err != nil {
		demo.Panic(err)
	}
	if err := collection.EnsureExists(ctx); err != nil {
		demo.Panic(err)
	}
	if err := collection.Upsert(ctx, SampleDocuments()...); err != nil {
		demo.Panic(err)
	}
	searchFunc := func(ctx context.Context, query string) ([]textsearch.Result, error) {
		return search(ctx, query, collection)
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
				ContextProviders: []agent.ContextProvider{textsearch.NewContextProvider(searchFunc, textsearch.Config{})},
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

func search(ctx context.Context, query string, collection *inmemory.Collection[uuid.UUID, TextSearchDocument]) ([]textsearch.Result, error) {
	if strings.TrimSpace(query) == "" {
		return []textsearch.Result{}, nil
	}
	results := make([]textsearch.Result, 0, 1)
	for result, err := range collection.Search(ctx, query, 1, nil) {
		if err != nil {
			return nil, err
		}
		document := result.Record
		results = append(results, textsearch.Result{
			SourceName: document.SourceName, SourceLink: document.SourceLink,
			Text: document.Text, RawRepresentation: document,
		})
	}
	return results, nil
}

type TextSearchDocument struct {
	ID         uuid.UUID `vectordata:"key"`
	Text       string    `vectordata:"vector,dimensions=3072"`
	SourceID   string    `vectordata:"data"`
	SourceName string    `vectordata:"data"`
	SourceLink string    `vectordata:"data"`
}

func SampleDocuments() []TextSearchDocument {
	return []TextSearchDocument{
		{
			SourceID: "return-policy-001", SourceName: "Contoso Outdoors Return Policy",
			SourceLink: "https://contoso.com/policies/returns",
			Text:       "Customers may return any item within 30 days of delivery. Items should be unused and include original packaging. Refunds are issued to the original payment method within 5 business days of inspection.",
		},
		{
			SourceID: "shipping-guide-001", SourceName: "Contoso Outdoors Shipping Guide",
			SourceLink: "https://contoso.com/help/shipping",
			Text:       "Standard shipping is free on orders over $50 and typically arrives in 3-5 business days within the continental United States. Expedited options are available at checkout.",
		},
		{
			SourceID: "tent-care-001", SourceName: "TrailRunner Tent Care Instructions",
			SourceLink: "https://contoso.com/manuals/trailrunner-tent",
			Text:       "Clean the tent fabric with lukewarm water and a non-detergent soap. Allow it to air dry completely before storage and avoid prolonged UV exposure to extend the lifespan of the waterproof coating.",
		},
	}
}
