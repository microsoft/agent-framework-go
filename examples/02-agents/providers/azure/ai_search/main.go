// Copyright (c) Microsoft. All rights reserved.

// This sample stores generated embeddings in Azure AI Search and runs scoped
// vector and hybrid queries. Its temporary index is deleted before exit.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

type document struct {
	ID      uuid.UUID `vectordata:"key,storage=id"`
	Topic   string    `vectordata:"data,indexed,storage=topic"`
	Text    string    `vectordata:"data,fulltext,storage=text"`
	Content string    `vectordata:"vector,dimensions=3072,storage=vector"`
}

func main() {
	ctx := context.Background()
	demo.NewLogger(
		"Azure AI Search",
		"Demonstrates generated embeddings, metadata filters, and hybrid search.",
		"Embedding Model", demo.FoundryEmbeddingModel,
	)
	endpoint := strings.TrimSpace(os.Getenv("AZURE_AI_SEARCH_ENDPOINT"))
	if endpoint == "" {
		demo.Panic("AZURE_AI_SEARCH_ENDPOINT must be set")
	}
	credential := demo.FoundryTokenCredential()
	generator := foundryprovider.NewEmbeddingGenerator(demo.FoundryProjectEndpoint, credential,
		foundryprovider.EmbeddingGeneratorConfig{Model: demo.FoundryEmbeddingModel})
	search, err := azureprovider.NewAISearch(endpoint, azureprovider.AISearchConfig{
		Credential: credential, EmbeddingGenerator: embedding.Bind(generator),
	})
	if err != nil {
		demo.Panic(err)
	}
	name := "afgo-example-" + uuid.NewString()
	collection, err := vectordata.GetCollection[uuid.UUID, *document](search.Store, name, nil)
	if err != nil {
		demo.Panic(err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		err = errors.Join(err, collection.EnsureDeleted(cleanupCtx))
		cancel()
		if err != nil {
			demo.Panic(err)
		}
	}()
	fmt.Printf("Temporary index: %s\n\n", name)
	if err = collection.EnsureExists(ctx); err != nil {
		return
	}
	var records []*document
	for _, seed := range []struct{ topic, text string }{
		{"humor", "I like pirate jokes, especially jokes about parrots and ships."},
		{"travel", "I enjoy hiking trips in the mountains and scenic viewpoints."},
		{"cooking", "I like cooking pasta with fresh tomatoes and basil."},
	} {
		records = append(records, &document{Topic: seed.topic, Text: seed.text, Content: seed.text})
	}
	if err = collection.Upsert(ctx, records...); err != nil {
		return
	}
	filter := vectordata.Filter{Operator: vectordata.Equal, Property: "Topic", Value: "humor"}
	if err = waitForIndex(ctx, collection, filter); err != nil {
		return
	}

	fmt.Println("Vector search, scoped to humor:")
	for result, searchErr := range collection.Search(ctx, "Tell me a pirate joke.", 2, &vectordata.SearchOptions{Filter: &filter}) {
		if searchErr != nil {
			err = searchErr
			return
		}
		fmt.Println(result.Record.Text)
	}
	fmt.Println("\nHybrid search, scoped to humor:")
	hybrid := collection.(vectordata.KeywordHybridSearcher[*document])
	for result, searchErr := range hybrid.HybridSearch(ctx, "pirate jokes", []string{"pirate", "parrot"}, 2,
		&vectordata.HybridSearchOptions{Filter: &filter}) {
		if searchErr != nil {
			err = searchErr
			return
		}
		fmt.Println(result.Record.Text)
	}
}

func waitForIndex(ctx context.Context, collection vectordata.Collection[uuid.UUID, *document], filter vectordata.Filter) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, err := range collection.GetFiltered(ctx, filter, 1, nil) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for document indexing: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
