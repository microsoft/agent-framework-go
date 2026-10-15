// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/embedding"
)

type TextSearchDocument struct {
	Text       string
	SourceID   string
	SourceName string
	SourceLink string
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

type storedDocument struct {
	document TextSearchDocument
	vector   []float32
}

// TextSearchStore implements the in-memory path used by Basic Text RAG:
// batch document embedding, persisted source text and cosine top-k search.
type TextSearchStore struct {
	generator embedding.Generator[string, *embedding.Of[float32]]
	documents []storedDocument
}

func NewTextSearchStore(generator embedding.Generator[string, *embedding.Of[float32]]) *TextSearchStore {
	if generator == nil {
		panic("an embedding generator and positive dimensions are required")
	}
	return &TextSearchStore{generator: generator}
}

func (s *TextSearchStore) UpsertDocuments(ctx context.Context, documents []TextSearchDocument) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	texts := make([]string, len(documents))
	for i, document := range documents {
		if strings.TrimSpace(document.Text) == "" {
			return fmt.Errorf("document %d must have text", i)
		}
		texts[i] = document.Text
	}
	pairs, err := embedding.GenerateAndZip(ctx, s.generator, texts, nil)
	if err != nil {
		return err
	}
	batch := make([]storedDocument, len(documents))
	for i, pair := range pairs {
		if pair.Embedding == nil {
			return fmt.Errorf("document %d has no embedding", i)
		}
		vector := pair.Embedding.Vector
		if len(vector) == 0 {
			return fmt.Errorf("document %d requires a non-empty float32 vector", i)
		}
		batch[i] = storedDocument{document: documents[i], vector: slices.Clone(vector)}
	}
	// The upstream default generates fresh keys, even for repeated source IDs.
	s.documents = append(s.documents, batch...)
	return nil
}

func (s *TextSearchStore) Search(ctx context.Context, query string, top int) ([]TextSearchDocument, error) {
	if strings.TrimSpace(query) == "" {
		return []TextSearchDocument{}, nil
	}
	if top < 1 {
		return nil, fmt.Errorf("top must be positive")
	}
	vector, err := embedding.GenerateVector(ctx, s.generator, query, nil)
	if err != nil {
		return nil, err
	}
	if len(vector) == 0 {
		return nil, fmt.Errorf("query requires a non-empty float32 vector")
	}
	type match struct {
		document TextSearchDocument
		score    float64
	}
	matches := make([]match, len(s.documents))
	for i, record := range s.documents {
		var dot, x, y float64
		for j, value := range vector {
			other := float64(record.vector[j])
			dot += float64(value) * other
			x += float64(value) * float64(value)
			y += other * other
		}
		matches[i] = match{record.document, dot / (math.Sqrt(x) * math.Sqrt(y))}
	}
	slices.SortStableFunc(matches, func(a, b match) int {
		if math.IsNaN(a.score) {
			if math.IsNaN(b.score) {
				return 0
			}
			return 1
		}
		if math.IsNaN(b.score) || a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return 0
	})
	results := make([]TextSearchDocument, min(top, len(matches)))
	for i := range results {
		results[i] = matches[i].document
	}
	return results, nil
}
