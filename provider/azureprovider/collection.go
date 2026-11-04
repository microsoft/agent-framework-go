// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

func (c *collection) Name() string { return c.name }

func (c *collection) Exists(ctx context.Context) (bool, error) {
	return c.store.CollectionExists(ctx, c.name)
}

func (c *collection) EnsureExists(ctx context.Context) error {
	exists, err := c.Exists(ctx)
	if err != nil || exists {
		return err
	}
	definition, err := c.indexDefinition()
	if err != nil {
		return failure(c.name, "CreateIndex", err)
	}
	err = c.store.client.request(ctx, http.MethodPost, c.store.client.url("/indexes", nil), definition, nil, http.StatusCreated)
	if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok && responseErr.ErrorCode == "ResourceNameAlreadyInUse" {
		return nil
	}
	if err != nil {
		return failure(c.name, "CreateIndex", err)
	}
	return nil
}

func (c *collection) EnsureDeleted(ctx context.Context) error {
	return c.store.EnsureCollectionDeleted(ctx, c.name)
}

func (c *collection) Get(ctx context.Context, key any, options *vectordata.RecordRetrievalOptions) (any, bool, error) {
	includeVectors := options != nil && options.IncludeVectors
	if err := c.validateRetrieval(includeVectors); err != nil {
		return nil, false, failure(c.name, "GetDocument", err)
	}
	stringKey, err := keyString(key)
	if err != nil {
		return nil, false, failure(c.name, "GetDocument", err)
	}
	query := make(url.Values)
	if !includeVectors {
		query.Set("$select", c.selectedFields())
	}
	path := indexPath(c.name) + "/docs('" + url.PathEscape(strings.ReplaceAll(stringKey, "'", "''")) + "')"
	var document map[string]json.RawMessage
	if err := c.store.client.request(ctx, http.MethodGet, c.store.client.url(path, query), nil, &document, http.StatusOK); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return nil, false, nil
		}
		return nil, false, failure(c.name, "GetDocument", err)
	}
	record, err := c.decodeRecord(document, includeVectors)
	if err != nil {
		return nil, false, failure(c.name, "GetDocument", err)
	}
	return record, true, nil
}

func (c *collection) Delete(ctx context.Context, key any) error {
	return c.DeleteBatch(ctx, []any{key})
}

func (c *collection) DeleteBatch(ctx context.Context, keys []any) error {
	if err := ctx.Err(); err != nil {
		return failure(c.name, "DeleteDocuments", err)
	}
	if len(keys) == 0 {
		return nil
	}
	actions := make([]map[string]any, len(keys))
	for i, key := range keys {
		value, err := keyString(key)
		if err != nil {
			return failure(c.name, "DeleteDocuments", err)
		}
		actions[i] = map[string]any{"@search.action": "delete", c.key.StorageName: value}
	}
	return c.indexDocuments(ctx, actions, "DeleteDocuments")
}

func (c *collection) Upsert(ctx context.Context, records ...any) error {
	if err := ctx.Err(); err != nil {
		return failure(c.name, "UploadDocuments", err)
	}
	if len(records) == 0 {
		return nil
	}
	owned := make([]any, len(records))
	for i, record := range records {
		value, err := c.prepareRecord(record)
		if err != nil {
			return failure(c.name, "UploadDocuments", err)
		}
		owned[i] = value
	}
	generated := make(map[string][][]float32)
	for _, property := range c.vectors {
		if property.Type == property.EmbeddingType {
			continue
		}
		result, err := providerservices.GenerateEmbeddings(ctx, property, owned)
		if err != nil {
			return failure(c.name, "UploadDocuments", err)
		}
		vectors := make([][]float32, len(result.Embeddings))
		for i, output := range result.Embeddings {
			vector, ok := providerservices.AsVector[float32](output)
			if !ok || vector == nil {
				return failure(c.name, "UploadDocuments", fmt.Errorf("property %q: generator returned an invalid float32 embedding at index %d", property.Name, i))
			}
			vectors[i] = vector
		}
		generated[property.Name] = vectors
	}
	actions := make([]map[string]any, len(records))
	for i, record := range owned {
		document, err := c.encodeRecord(record, i, generated)
		if err != nil {
			return failure(c.name, "UploadDocuments", err)
		}
		document["@search.action"] = "upload"
		actions[i] = document
	}
	return c.indexDocuments(ctx, actions, "UploadDocuments")
}

func (c *collection) indexDocuments(ctx context.Context, actions []map[string]any, operation string) error {
	body := struct {
		Value []map[string]any `json:"value"`
	}{Value: actions}
	var response struct {
		Value []struct {
			Key          string `json:"key"`
			Status       *bool  `json:"status"`
			StatusCode   int    `json:"statusCode"`
			ErrorMessage string `json:"errorMessage"`
		} `json:"value"`
	}
	if err := c.store.client.request(ctx, http.MethodPost, c.store.client.url(indexPath(c.name)+"/docs/index", nil),
		body, &response, http.StatusOK, http.StatusMultiStatus); err != nil {
		return failure(c.name, operation, err)
	}
	if len(response.Value) != len(actions) {
		return failure(c.name, operation, fmt.Errorf("indexing response contains %d results for %d documents", len(response.Value), len(actions)))
	}
	var failures []error
	for _, result := range response.Value {
		if result.Status == nil {
			failures = append(failures, fmt.Errorf("indexing result for document %q is missing its status", result.Key))
		} else if !*result.Status {
			failures = append(failures, fmt.Errorf("document %q failed with status %d: %s", result.Key, result.StatusCode, result.ErrorMessage))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return failure(c.name, operation, err)
	}
	return nil
}

func (c *collection) selectedFields() string {
	names := make([]string, 0, len(c.data)+1)
	names = append(names, c.key.StorageName)
	for _, property := range c.data {
		names = append(names, property.StorageName)
	}
	return strings.Join(names, ",")
}

func (c *collection) validateRetrieval(includeVectors bool) error {
	if includeVectors && c.model.EmbeddingGenerationRequired {
		return fmt.Errorf("IncludeVectors is unsupported for collections with generated vector fields")
	}
	return nil
}

var (
	_ vectordata.Collection[any, any]       = (*collection)(nil)
	_ vectordata.KeywordHybridSearcher[any] = (*collection)(nil)
)
