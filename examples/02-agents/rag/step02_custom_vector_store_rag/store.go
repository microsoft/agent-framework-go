// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"unicode/utf16"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent/textsearch"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/qdrant/go-client/qdrant"
)

const collectionName = "documentation"

type qdrantBackend interface {
	CollectionExists(context.Context, string) (bool, error)
	DeleteCollection(context.Context, string) error
	CreateCollection(context.Context, *qdrant.CreateCollection) error
	Upsert(context.Context, *qdrant.UpsertPoints) (*qdrant.UpdateResult, error)
	Query(context.Context, *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error)
}

type documentationStore struct {
	client    qdrantBackend
	generator embedding.Generator[string, *embedding.Of[float32]]
}

func (s *documentationStore) reset(ctx context.Context) error {
	exists, err := s.client.CollectionExists(ctx, collectionName)
	if err != nil {
		return err
	}
	if exists {
		if err := s.client.DeleteCollection(ctx, collectionName); err != nil {
			return err
		}
	}
	return s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: collectionName,
		VectorsConfig:  qdrant.NewVectorsConfig(&qdrant.VectorParams{Size: 3072, Distance: qdrant.Distance_Cosine}),
	})
}

func (s *documentationStore) uploadMarkdown(ctx context.Context, sourceURL, sourceName string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download %s: HTTP %d", sourceURL, response.StatusCode)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	chunks := chunkMarkdown(string(data), 2000, 200)
	pairs, err := embedding.GenerateAndZip(ctx, s.generator, chunks, nil)
	if err != nil {
		return err
	}
	points := make([]*qdrant.PointStruct, len(pairs))
	for i, pair := range pairs {
		if pair.Embedding == nil {
			return fmt.Errorf("chunk %d has no embedding", i)
		}
		vector := pair.Embedding.Vector
		if len(vector) != 3072 {
			return fmt.Errorf("chunk %d requires a 3072-dimensional float32 vector", i)
		}
		points[i] = &qdrant.PointStruct{
			Id:      qdrant.NewIDUUID(uuid.NewString()),
			Vectors: qdrant.NewVectorsDense(vector),
			Payload: qdrant.NewValueMap(map[string]any{
				"SourceLink": sourceURL, "SourceName": sourceName, "Text": pair.Value,
			}),
		}
	}
	if len(points) == 0 {
		return nil
	}
	_, err = s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collectionName, Points: points, Wait: new(true),
	})
	return err
}

func (s *documentationStore) search(ctx context.Context, query string) ([]textsearch.Result, error) {
	vector, err := embedding.GenerateVector(ctx, s.generator, query, nil)
	if err != nil {
		return nil, err
	}
	if len(vector) != 3072 {
		return nil, fmt.Errorf("query requires a 3072-dimensional float32 vector")
	}
	points, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collectionName, Query: qdrant.NewQueryDense(vector),
		Limit: new(uint64(5)), WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}
	results := make([]textsearch.Result, len(points))
	for i, point := range points {
		results[i] = textsearch.Result{
			SourceName: point.Payload["SourceName"].GetStringValue(),
			SourceLink: point.Payload["SourceLink"].GetStringValue(),
			Text:       point.Payload["Text"].GetStringValue(), RawRepresentation: point,
		}
	}
	return results, nil
}

func chunkMarkdown(markdown string, size, overlap int) []string {
	text := utf16.Encode([]rune(markdown))
	var chunks []string
	for i := 0; i < len(text); i += size {
		chunks = append(chunks, string(utf16.Decode(text[i:min(i+size+overlap, len(text))])))
	}
	return chunks
}
