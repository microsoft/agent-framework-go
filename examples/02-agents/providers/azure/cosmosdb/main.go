// Copyright (c) Microsoft. All rights reserved.

// This sample stores generated embeddings in a temporary Cosmos DB container.
// It uses an existing database and deletes only its own container before exit.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

type document struct {
	ID      uuid.UUID `vectordata:"key"`
	Topic   string    `vectordata:"data,indexed,storage=topic"`
	Text    string    `vectordata:"data,storage=text"`
	Content string    `vectordata:"vector,dimensions=3072,index=disk_ann,storage=vector"`
}

func main() {
	ctx := context.Background()
	demo.NewLogger("Azure Cosmos DB", "Demonstrates generated embeddings and scoped vector search.",
		"Embedding Model", demo.FoundryEmbeddingModel)
	endpoint := strings.TrimSpace(os.Getenv("AZURE_COSMOS_ENDPOINT"))
	database := strings.TrimSpace(os.Getenv("AZURE_COSMOS_DATABASE"))
	if endpoint == "" || database == "" {
		demo.Panic("AZURE_COSMOS_ENDPOINT and AZURE_COSMOS_DATABASE must be set")
	}
	credential := demo.FoundryTokenCredential()
	client, err := azcosmos.NewClient(endpoint, credential, nil)
	if err != nil {
		demo.Panic(err)
	}
	defer client.Close()
	generator := foundryprovider.NewEmbeddingGenerator(demo.FoundryProjectEndpoint, credential,
		foundryprovider.EmbeddingGeneratorConfig{Model: demo.FoundryEmbeddingModel})
	store, err := azureprovider.NewCosmosDB(client, database, azureprovider.CosmosDBConfig{
		PartitionKeyProperties: []string{"Topic"},
		EmbeddingGenerator:     embedding.Bind(generator),
	})
	if err != nil {
		demo.Panic(err)
	}
	name := "afgo-example-" + uuid.NewString()
	collection, err := vectordata.GetCollection[azureprovider.CosmosKey, *document](store.Store, name, nil)
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
	fmt.Printf("Temporary container: %s\n\n", name)
	if err = collection.EnsureExists(ctx); err != nil {
		return
	}
	if err = collection.Upsert(ctx,
		&document{Topic: "humor", Text: "I enjoy pirate jokes about parrots and ships.", Content: "I enjoy pirate jokes about parrots and ships."},
		&document{Topic: "travel", Text: "I like hiking in the mountains.", Content: "I like hiking in the mountains."},
	); err != nil {
		return
	}
	filter := vectordata.Filter{Operator: vectordata.Equal, Property: "Topic", Value: "humor"}
	for result, searchErr := range collection.Search(ctx, "Tell me a pirate joke.", 2, &vectordata.SearchOptions{Filter: &filter}) {
		if searchErr != nil {
			err = searchErr
			return
		}
		fmt.Println(result.Record.Text)
	}
}
