// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

type searchRequest struct {
	Search           *string       `json:"search,omitempty"`
	SearchFields     string        `json:"searchFields,omitempty"`
	Filter           string        `json:"filter,omitempty"`
	OrderBy          string        `json:"orderby,omitempty"`
	Select           string        `json:"select,omitempty"`
	Top              int           `json:"top"`
	Skip             int           `json:"skip"`
	VectorFilterMode string        `json:"vectorFilterMode,omitempty"`
	VectorQueries    []vectorQuery `json:"vectorQueries,omitempty"`
}

type vectorQuery struct {
	Kind   string     `json:"kind"`
	Fields string     `json:"fields"`
	K      int        `json:"k"`
	Vector *[]float32 `json:"vector,omitempty"`
	Text   *string    `json:"text,omitempty"`
}

func (c *collection) GetFiltered(ctx context.Context, filter vectordata.Filter, top int, options *vectordata.FilteredRecordRetrievalOptions) iter.Seq2[any, error] {
	return func(yield func(any, error) bool) {
		if err := options.Validate(); err != nil {
			yield(nil, failure(c.name, "Search", err))
			return
		}
		var settings vectordata.FilteredRecordRetrievalOptions
		if options != nil {
			settings = *options
		}
		request, err := c.baseSearchRequest(&filter, top, settings.Skip, settings.IncludeVectors)
		if err != nil {
			yield(nil, failure(c.name, "Search", err))
			return
		}
		ordering := make([]string, len(settings.OrderBy))
		for i, order := range settings.OrderBy {
			name, _, err := c.filterProperty(order.Property)
			if err != nil {
				yield(nil, failure(c.name, "Search", err))
				return
			}
			ordering[i] = name
			if order.Descending {
				ordering[i] += " desc"
			}
		}
		request.OrderBy = strings.Join(ordering, ",")
		for result, err := range c.search(ctx, request, settings.IncludeVectors, nil) {
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(result.Record, nil) {
				return
			}
		}
	}
}

func (c *collection) Search(ctx context.Context, value any, top int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
	return func(yield func(vectordata.SearchResult[any], error) bool) {
		if err := options.Validate(); err != nil {
			yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
			return
		}
		var settings vectordata.SearchOptions
		if options != nil {
			settings = *options
		}
		request, err := c.vectorSearchRequest(ctx, value, top, settings)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
			return
		}
		for result, err := range c.search(ctx, request, settings.IncludeVectors, settings.ScoreThreshold) {
			if !yield(result, err) {
				return
			}
		}
	}
}

func (c *collection) HybridSearch(ctx context.Context, value any, keywords []string, top int, options *vectordata.HybridSearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
	return func(yield func(vectordata.SearchResult[any], error) bool) {
		if keywords == nil {
			yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", fmt.Errorf("keywords must not be nil")))
			return
		}
		if err := options.Validate(); err != nil {
			yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
			return
		}
		var settings vectordata.HybridSearchOptions
		if options != nil {
			settings = *options
		}
		textProperty, err := c.model.FullTextProperty(settings.AdditionalProperty)
		if err != nil {
			yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
			return
		}
		request, err := c.vectorSearchRequest(ctx, value, top, vectordata.SearchOptions{
			Filter: settings.Filter, VectorProperty: settings.VectorProperty,
			Skip: settings.Skip, IncludeVectors: settings.IncludeVectors,
		})
		if err != nil {
			yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
			return
		}
		request.Search = new(strings.Join(keywords, " "))
		request.SearchFields = textProperty.StorageName
		for result, err := range c.search(ctx, request, settings.IncludeVectors, settings.ScoreThreshold) {
			if !yield(result, err) {
				return
			}
		}
	}
}

func (c *collection) baseSearchRequest(filter *vectordata.Filter, top, skip int, includeVectors bool) (*searchRequest, error) {
	if top <= 0 {
		return nil, fmt.Errorf("top must be positive")
	}
	if err := c.validateRetrieval(includeVectors); err != nil {
		return nil, err
	}
	expression, err := c.translateFilter(filter)
	if err != nil {
		return nil, err
	}
	request := &searchRequest{Filter: expression, Top: top, Skip: skip}
	if !includeVectors {
		request.Select = c.selectedFields()
	}
	return request, nil
}

func (c *collection) vectorSearchRequest(ctx context.Context, value any, top int, options vectordata.SearchOptions) (*searchRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, err := c.baseSearchRequest(options.Filter, top, options.Skip, options.IncludeVectors)
	if err != nil {
		return nil, err
	}
	if top > int(^uint(0)>>1)-options.Skip {
		return nil, fmt.Errorf("top plus skip exceeds the supported integer range")
	}
	property, err := c.model.VectorProperty(options.VectorProperty)
	if err != nil {
		return nil, err
	}
	query, err := searchVector(ctx, value, property)
	if err != nil {
		return nil, err
	}
	query.Fields, query.K = property.StorageName, top+options.Skip
	request.VectorQueries = []vectorQuery{query}
	request.VectorFilterMode = "preFilter"
	return request, nil
}

func searchVector(ctx context.Context, value any, property *providerservices.VectorPropertyModel) (vectorQuery, error) {
	if vector, ok := providerservices.AsVector[float32](value); ok || value == nil {
		if vector == nil {
			return vectorQuery{}, fmt.Errorf("search requires a non-nil value")
		}
		return vectorQuery{Kind: "vector", Vector: &vector}, nil
	}
	if property.SearchEmbeddingGenerator != nil {
		output, err := embedding.Generate(ctx, property.SearchEmbeddingGenerator, value, nil)
		if err != nil {
			return vectorQuery{}, err
		}
		vector, ok := providerservices.AsVector[float32](output)
		if !ok || vector == nil {
			return vectorQuery{}, fmt.Errorf("property %q: generator returned an invalid float32 embedding", property.Name)
		}
		return vectorQuery{Kind: "vector", Vector: &vector}, nil
	}
	if text, ok := value.(string); ok && property.EmbeddingGenerator == nil {
		return vectorQuery{Kind: "text", Text: &text}, nil
	}
	return vectorQuery{}, fmt.Errorf("property %q has no compatible embedding generator for search input %T", property.Name, value)
}

func (c *collection) search(ctx context.Context, request *searchRequest, includeVectors bool, threshold *float64) iter.Seq2[vectordata.SearchResult[any], error] {
	return func(yield func(vectordata.SearchResult[any], error) bool) {
		endpoint := c.store.client.url(indexPath(c.name)+"/docs/search", nil)
		var body any = request
		seen := 0
		for {
			var page struct {
				Value              []map[string]json.RawMessage `json:"value"`
				NextLink           string                       `json:"@odata.nextLink"`
				NextPageParameters json.RawMessage              `json:"@search.nextPageParameters"`
			}
			if err := c.store.client.request(ctx, http.MethodPost, endpoint, body, &page, http.StatusOK); err != nil {
				yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
				return
			}
			if page.Value == nil {
				yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", fmt.Errorf("response is missing search results")))
				return
			}
			for _, document := range page.Value {
				if err := ctx.Err(); err != nil {
					yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
					return
				}
				record, err := c.decodeRecord(document, includeVectors)
				if err != nil {
					yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
					return
				}
				var score *float64
				if raw, found := document["@search.score"]; found {
					if err := json.Unmarshal(raw, &score); err != nil {
						yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", fmt.Errorf("invalid search score: %w", err)))
						return
					}
				}
				seen++
				if threshold == nil || score == nil || !(*score < *threshold) {
					if !yield(vectordata.SearchResult[any]{Record: record, Score: score}, nil) {
						return
					}
				}
				if seen >= request.Top {
					return
				}
			}
			if page.NextLink == "" {
				return
			}
			if len(page.NextPageParameters) == 0 || string(page.NextPageParameters) == "null" {
				yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", fmt.Errorf("search continuation is missing its request parameters")))
				return
			}
			var err error
			endpoint, err = c.store.client.nextURL(endpoint, page.NextLink)
			if err != nil {
				yield(vectordata.SearchResult[any]{}, failure(c.name, "Search", err))
				return
			}
			body = page.NextPageParameters
		}
	}
}
