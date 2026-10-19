// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

type cosmosProtocolRecord struct {
	ID       uuid.UUID `vectordata:"key"`
	Category string    `vectordata:"data,indexed"`
	Text     string    `vectordata:"data,fulltext"`
	Tags     []string  `vectordata:"data"`
	Vector   []float32 `vectordata:"vector,dimensions=2,index=disk_ann,distance=cosine_similarity"`
	Bytes    []uint8   `vectordata:"vector,dimensions=2,index=flat"`
	Signed   []int8    `vectordata:"vector,dimensions=2,index=quantized_flat"`
}

type cosmosExampleShapedRecord struct {
	ID      uuid.UUID `vectordata:"key"`
	Topic   string    `vectordata:"data,indexed,storage=topic"`
	Text    string    `vectordata:"data,storage=text"`
	Content string    `vectordata:"vector,dimensions=3072,index=disk_ann,storage=vector"`
}

type cosmosTestEmbeddingGenerator struct {
	calls  int
	inputs [][]string
}

func (g *cosmosTestEmbeddingGenerator) Generate(_ context.Context, values []string, _ *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
	g.calls++
	g.inputs = append(g.inputs, append([]string(nil), values...))
	result := &embedding.Generated[*embedding.Of[float32]]{
		Embeddings: make([]*embedding.Of[float32], len(values)),
	}
	for i, value := range values {
		result.Embeddings[i] = &embedding.Of[float32]{Vector: []float32{float32(len(value)), float32(i)}}
	}
	return result, nil
}

func TestCosmosDBNativeSDKLifecycleCRUDAndSearch(t *testing.T) {
	var mu sync.Mutex
	var stored map[string]any
	var createBody map[string]any
	var queries []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "mock", "enableMultipleWriteLocations": false,
				"readableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
				"writableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls" && strings.Contains(r.Header.Get("Content-Type"), "application/query+json"):
			_, _ = io.WriteString(w, `{"DocumentCollections":[{"id":"vectors"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls":
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"vectors","partitionKey":{"kind":"Hash","paths":["/id"],"version":2}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/dbs/sample/colls/vectors":
			_, _ = io.WriteString(w, `{"id":"vectors","partitionKey":{"kind":"Hash","paths":["/id"],"version":2}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls/vectors/docs" && strings.Contains(r.Header.Get("Content-Type"), "application/query+json"):
			var request struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			queries = append(queries, request.Query)
			document := map[string]any{
				"id": stored["id"], "Category": stored["Category"], "Text": stored["Text"],
				"Tags": stored["Tags"],
			}
			document["__cosmos_score"] = 0.75
			if strings.Contains(request.Query, `c["Vector"]`) {
				document["Vector"] = stored["Vector"]
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"Documents": []any{document}})
		case r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls/vectors/docs":
			mu.Lock()
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				mu.Unlock()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(stored)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/dbs/sample/colls/vectors/docs/"):
			_ = json.NewEncoder(w).Encode(stored)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/dbs/sample/colls/vectors/docs/"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/dbs/sample/colls/vectors":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("local-test-key")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport:                       server.Client(),
			InsecureAllowCredentialWithHTTP: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := azureprovider.NewCosmosDB(client, "sample", azureprovider.CosmosDBConfig{})
	if err != nil {
		t.Fatal(err)
	}
	collection, err := vectordata.GetCollection[uuid.UUID, *cosmosProtocolRecord](db.Store, "vectors", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	if createBody["id"] != "vectors" {
		t.Fatalf("container create payload = %#v", createBody)
	}
	embeddingPolicy, ok := createBody["vectorEmbeddingPolicy"].(map[string]any)
	if !ok {
		t.Fatalf("container missing vector embedding policy: %#v", createBody)
	}
	embeddings, ok := embeddingPolicy["vectorEmbeddings"].([]any)
	if !ok || len(embeddings) != 3 {
		t.Fatalf("vector embedding policy = %#v", embeddingPolicy)
	}
	if embeddings[0].(map[string]any)["dataType"] != "float32" ||
		embeddings[1].(map[string]any)["dataType"] != "uint8" ||
		embeddings[2].(map[string]any)["dataType"] != "int8" {
		t.Fatalf("vector embedding data types = %#v", embeddings)
	}
	fullTextPolicy, ok := createBody["fullTextPolicy"].(map[string]any)
	if !ok || fullTextPolicy["defaultLanguage"] != "en-US" {
		t.Fatalf("full-text policy = %#v", createBody["fullTextPolicy"])
	}

	record := &cosmosProtocolRecord{
		Category: "engineering", Text: "vector search", Tags: []string{"go", "cosmos"},
		Vector: []float32{0.1, 0.2}, Bytes: []uint8{1, 2}, Signed: []int8{-1, 2},
	}
	if err := collection.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if record.ID == uuid.Nil {
		t.Fatal("upsert did not generate a UUID key")
	}
	mu.Lock()
	gotStored := stored
	mu.Unlock()
	if gotStored["id"] != record.ID.String() || gotStored["Vector"] == nil {
		t.Fatalf("stored document = %#v", gotStored)
	}
	if vector, ok := gotStored["Bytes"].([]any); !ok || !reflect.DeepEqual(vector, []any{float64(1), float64(2)}) {
		t.Fatalf("byte vector must be a numeric JSON array: %#v", gotStored["Bytes"])
	}

	got, found, err := collection.Get(t.Context(), record.ID, nil)
	if err != nil || !found {
		t.Fatalf("Get() = (%v, %v, %v)", got, found, err)
	}
	if got.ID != record.ID || got.Vector != nil {
		t.Fatalf("Get() did not return key and omit vectors by default: %#v", got)
	}
	gotWithVectors, found, err := collection.Get(t.Context(), record.ID, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || gotWithVectors.Vector[0] != 0.1 ||
		gotWithVectors.Bytes[0] != 1 || gotWithVectors.Signed[0] != -1 {
		t.Fatalf("Get() with vectors = (%#v, %v, %v)", gotWithVectors, found, err)
	}

	idFilter := vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: record.ID}
	filtered := make([]*cosmosProtocolRecord, 0, 1)
	for item, err := range collection.GetFiltered(t.Context(), vectordata.Filter{
		Operator: vectordata.And, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Category", Value: "engineering"},
			idFilter,
		},
	}, 5, nil) {
		if err != nil {
			t.Fatal(err)
		}
		filtered = append(filtered, item)
	}
	if len(filtered) != 1 || filtered[0].Category != "engineering" {
		t.Fatalf("filtered records = %#v", filtered)
	}
	for item, err := range collection.GetFiltered(t.Context(), vectordata.Filter{
		Operator: vectordata.Contains, Property: "Tags", Value: "go",
	}, 5, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if item.Category != "engineering" {
			t.Fatalf("contains result = %#v", item)
		}
	}

	searchResults := make([]vectordata.SearchResult[*cosmosProtocolRecord], 0, 1)
	threshold := 0.5
	for result, err := range collection.Search(t.Context(), []float32{0.1, 0.2}, 3, &vectordata.SearchOptions{
		VectorProperty: "Vector", ScoreThreshold: &threshold, Filter: &idFilter,
	}) {
		if err != nil {
			t.Fatal(err)
		}
		searchResults = append(searchResults, result)
	}
	if len(searchResults) != 1 || searchResults[0].Score == nil || *searchResults[0].Score != 0.75 || searchResults[0].Record.Vector != nil {
		t.Fatalf("search results = %#v", searchResults)
	}

	hybridResults := make([]vectordata.SearchResult[*cosmosProtocolRecord], 0, 1)
	hybrid, ok := collection.(vectordata.KeywordHybridSearcher[*cosmosProtocolRecord])
	if !ok {
		t.Fatal("collection does not expose hybrid search")
	}
	for result, err := range hybrid.HybridSearch(t.Context(), []float32{0.1, 0.2}, []string{"cosmos", "vectors"}, 3,
		&vectordata.HybridSearchOptions{VectorProperty: "Vector", ScoreThreshold: &threshold, Filter: &idFilter}) {
		if err != nil {
			t.Fatal(err)
		}
		hybridResults = append(hybridResults, result)
	}
	if len(hybridResults) != 1 {
		t.Fatalf("hybrid results = %#v", hybridResults)
	}
	if hybridResults[0].Score == nil || *hybridResults[0].Score != 0.75 {
		t.Fatalf("hybrid results = %#v", hybridResults)
	}

	names := make([]string, 0, 1)
	for name, err := range db.ListCollectionNames(t.Context()) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if len(names) != 1 || names[0] != "vectors" {
		t.Fatalf("collection names = %#v", names)
	}
	mu.Lock()
	gotQueries := append([]string(nil), queries...)
	mu.Unlock()
	if len(gotQueries) != 4 ||
		!strings.Contains(gotQueries[0], "@p0") ||
		!strings.Contains(gotQueries[1], `ARRAY_CONTAINS(c["Tags"]`) ||
		!strings.Contains(gotQueries[2], "VectorDistance") ||
		!strings.Contains(gotQueries[2], ">= @scoreThreshold") ||
		!strings.Contains(gotQueries[3], "FullTextScore") ||
		!strings.Contains(gotQueries[3], "RANK RRF") ||
		!strings.Contains(gotQueries[3], ">= @scoreThreshold") {
		t.Fatalf("query SQL = %#v", gotQueries)
	}

	var invalidVectorErr error
	for _, err := range collection.Search(t.Context(), []float32{0.1}, 1, &vectordata.SearchOptions{
		VectorProperty: "Vector", Filter: &idFilter,
	}) {
		invalidVectorErr = err
	}
	if invalidVectorErr == nil || !strings.Contains(invalidVectorErr.Error(), "dimension 2") {
		t.Fatalf("invalid vector search error = %v", invalidVectorErr)
	}
	var invalidFilterErr error
	for _, err := range collection.GetFiltered(t.Context(), vectordata.Filter{
		Operator: vectordata.Contains, Property: "Category", Value: "engineering",
	}, 1, nil) {
		invalidFilterErr = err
	}
	if invalidFilterErr == nil || !strings.Contains(invalidFilterErr.Error(), "not a collection") {
		t.Fatalf("invalid filter error = %v", invalidFilterErr)
	}
	if err := collection.Delete(t.Context(), record.ID); err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureDeleted(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCosmosDBExampleShapedGeneratedVectorSchema(t *testing.T) {
	var createBody map[string]any
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "mock", "enableMultipleWriteLocations": false,
				"readableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
				"writableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls" {
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"example-shaped","partitionKey":{"kind":"Hash","paths":["/id"],"version":2}}`)
			return
		}
		http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
	}))
	defer server.Close()

	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("local-test-key")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport:                       server.Client(),
			InsecureAllowCredentialWithHTTP: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := azureprovider.NewCosmosDB(client, "sample", azureprovider.CosmosDBConfig{
		PartitionKeyProperties: []string{"Topic"},
		EmbeddingGenerator:     embedding.Bind[string, *embedding.Of[float32]](&cosmosTestEmbeddingGenerator{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	collection, err := vectordata.GetCollection[azureprovider.CosmosKey, *cosmosExampleShapedRecord](store.Store, "example-shaped", nil)
	if err != nil {
		t.Fatalf("open collection with index=diskann: %v", err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	partition, ok := createBody["partitionKey"].(map[string]any)
	paths, pathsOK := partition["paths"].([]any)
	if !ok || !pathsOK || len(paths) != 1 || paths[0] != "/topic" {
		t.Fatalf("UUID collection partition key = %#v", createBody["partitionKey"])
	}
	vectorPolicy, ok := createBody["vectorEmbeddingPolicy"].(map[string]any)
	if !ok {
		t.Fatalf("vector embedding policy = %#v", createBody["vectorEmbeddingPolicy"])
	}
	embeddings, ok := vectorPolicy["vectorEmbeddings"].([]any)
	if !ok || len(embeddings) != 1 {
		t.Fatalf("vector embeddings = %#v", vectorPolicy["vectorEmbeddings"])
	}
	vectorEmbedding := embeddings[0].(map[string]any)
	if vectorEmbedding["path"] != "/vector" || vectorEmbedding["dimensions"] != float64(3072) ||
		vectorEmbedding["dataType"] != "float32" || vectorEmbedding["distanceFunction"] != "cosine" {
		t.Fatalf("example-shaped vector embedding policy = %#v", vectorEmbedding)
	}
	indexingPolicy, ok := createBody["indexingPolicy"].(map[string]any)
	if !ok {
		t.Fatalf("indexing policy = %#v", createBody["indexingPolicy"])
	}
	indexes, ok := indexingPolicy["vectorIndexes"].([]any)
	if !ok || len(indexes) != 1 {
		t.Fatalf("vector indexes = %#v", indexingPolicy["vectorIndexes"])
	}
	if index := indexes[0].(map[string]any); index["path"] != "/vector" || index["type"] != "diskANN" {
		t.Fatalf("example-shaped vector index = %#v", index)
	}
}

func TestCosmosDBIteratorStopsBeforeFetchingContinuationPage(t *testing.T) {
	pageRequests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "mock", "enableMultipleWriteLocations": false,
				"readableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
				"writableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls" &&
			strings.Contains(r.Header.Get("Content-Type"), "application/query+json") {
			pageRequests++
			w.Header().Set("x-ms-continuation", "next-page")
			_, _ = io.WriteString(w, `{"DocumentCollections":[{"id":"first"}]}`)
			return
		}
		http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
	}))
	defer server.Close()
	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("local-test-key")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: server.Client(), InsecureAllowCredentialWithHTTP: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := azureprovider.NewCosmosDB(client, "sample", azureprovider.CosmosDBConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range store.ListCollectionNames(t.Context()) {
		if err != nil {
			t.Fatal(err)
		}
		if name != "first" {
			t.Fatalf("first collection name = %q", name)
		}
		break
	}
	if pageRequests != 1 {
		t.Fatalf("query pages requested after early stop = %d, want 1", pageRequests)
	}
}

func TestCosmosDBRejectsInvalidConfigAndPartitionModel(t *testing.T) {
	if _, err := azureprovider.NewCosmosDB(nil, "sample", azureprovider.CosmosDBConfig{}); err == nil {
		t.Fatal("expected nil client error")
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("test")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport:                       server.Client(),
			InsecureAllowCredentialWithHTTP: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := azureprovider.NewCosmosDB(client, "sample", azureprovider.CosmosDBConfig{
		PartitionKeyProperties: []string{"Category"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vectordata.GetCollection[uuid.UUID, *cosmosProtocolRecord](store.Store, "vectors", nil); err == nil || !strings.Contains(err.Error(), "CosmosKey") {
		t.Fatalf("expected custom partition key type error, got %v", err)
	}
}

func TestCosmosDBCompositePartitionKeyDefinitionAndOperations(t *testing.T) {
	var createBody map[string]any
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "mock", "enableMultipleWriteLocations": false,
				"readableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
				"writableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
			})
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls" {
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"vectors","partitionKey":{"kind":"MultiHash","paths":["/Category","/Tenant"],"version":2}}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls/vectors/docs" && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			var document map[string]any
			_ = json.NewDecoder(r.Body).Decode(&document)
			if got := r.Header.Get("x-ms-documentdb-partitionkey"); got != `["engineering","tenant-1"]` {
				http.Error(w, "partition key "+got, http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(document)
			return
		}
		http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
	}))
	defer server.Close()
	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("local-test-key")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: server.Client(), InsecureAllowCredentialWithHTTP: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := azureprovider.NewCosmosDB(client, "sample", azureprovider.CosmosDBConfig{
		PartitionKeyProperties: []string{"Category", "Tenant"},
	})
	if err != nil {
		t.Fatal(err)
	}
	type record struct {
		ID       string    `vectordata:"key"`
		Category string    `vectordata:"data"`
		Tenant   string    `vectordata:"data"`
		Vector   []float32 `vectordata:"vector,dimensions=2"`
	}
	collection, err := vectordata.GetCollection[azureprovider.CosmosKey, record](store.Store, "vectors", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	partitionDefinition, ok := createBody["partitionKey"].(map[string]any)
	if !ok || partitionDefinition["kind"] != "MultiHash" {
		t.Fatalf("partition key definition = %#v", createBody["partitionKey"])
	}
	if err := collection.Upsert(t.Context(), record{
		ID: "item-1", Category: "engineering", Tenant: "tenant-1", Vector: []float32{0.2, 0.3},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCosmosDBGeneratesEmbeddingsOncePerBatch(t *testing.T) {
	var stored []map[string]any
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "mock", "enableMultipleWriteLocations": false,
				"readableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
				"writableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls/vectors/docs" &&
			strings.Contains(r.Header.Get("Content-Type"), "application/query+json") {
			_, _ = io.WriteString(w, `{"Documents":[]}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls/vectors/docs" {
			var document map[string]any
			if err := json.NewDecoder(r.Body).Decode(&document); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			stored = append(stored, document)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
	}))
	defer server.Close()
	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("local-test-key")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: server.Client(), InsecureAllowCredentialWithHTTP: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	generator := &cosmosTestEmbeddingGenerator{}
	store, err := azureprovider.NewCosmosDB(client, "sample", azureprovider.CosmosDBConfig{
		EmbeddingGenerator: embedding.Bind(generator),
	})
	if err != nil {
		t.Fatal(err)
	}
	type generatedRecord struct {
		ID      uuid.UUID `vectordata:"key"`
		Content string    `vectordata:"vector,dimensions=2"`
	}
	collection, err := vectordata.GetCollection[uuid.UUID, *generatedRecord](store.Store, "vectors", nil)
	if err != nil {
		t.Fatal(err)
	}
	records := []*generatedRecord{{Content: "first"}, {Content: "second"}}
	if err := collection.Upsert(t.Context(), records...); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 || len(generator.inputs) != 1 ||
		len(generator.inputs[0]) != 2 || generator.inputs[0][0] != "first" || generator.inputs[0][1] != "second" {
		t.Fatalf("generator calls/inputs = %d, %#v", generator.calls, generator.inputs)
	}
	if len(stored) != 2 {
		t.Fatalf("stored items = %d, want 2", len(stored))
	}
	for i, document := range stored {
		vector, ok := document["Content"].([]any)
		if !ok || len(vector) != 2 || document["id"] != records[i].ID.String() {
			t.Fatalf("stored generated document = %#v", document)
		}
	}
	for _, err := range collection.Search(t.Context(), "query", 1, &vectordata.SearchOptions{
		Filter: &vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: records[0].ID},
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if generator.calls != 2 || len(generator.inputs) != 2 ||
		len(generator.inputs[1]) != 1 || generator.inputs[1][0] != "query" {
		t.Fatalf("search generator calls/inputs = %d, %#v", generator.calls, generator.inputs)
	}
}

func TestCosmosDBDynamicRecordMapping(t *testing.T) {
	var stored map[string]any
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "mock", "enableMultipleWriteLocations": false,
				"readableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
				"writableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/dbs/sample/colls/dynamic/docs" {
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(stored)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/dbs/sample/colls/dynamic/docs/") {
			_ = json.NewEncoder(w).Encode(stored)
			return
		}
		http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
	}))
	defer server.Close()
	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("local-test-key")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: server.Client(), InsecureAllowCredentialWithHTTP: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := azureprovider.NewCosmosDB(client, "sample", azureprovider.CosmosDBConfig{})
	if err != nil {
		t.Fatal(err)
	}
	definition := &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", StorageName: "id", Type: reflect.TypeFor[uuid.UUID]()},
		&vectordata.DataProperty{Name: "Text", StorageName: "text", Type: reflect.TypeFor[string]()},
	}}
	collection, err := store.DynamicCollection("dynamic", definition)
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"Text": "dynamic"}
	if err := collection.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	id, ok := record["ID"].(uuid.UUID)
	if !ok || id == uuid.Nil || stored["id"] != id.String() || stored["text"] != "dynamic" {
		t.Fatalf("dynamic record after upsert = %#v; stored document = %#v", record, stored)
	}
	result, found, err := collection.Get(t.Context(), id, nil)
	if err != nil || !found {
		t.Fatalf("dynamic Get() = (%#v, %v, %v)", result, found, err)
	}
	if result["ID"] != id || result["Text"] != "dynamic" {
		t.Fatalf("dynamic Get() record = %#v", result)
	}
}

type cosmosTestQuery struct {
	Query      string `json:"query"`
	Parameters []struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	} `json:"parameters"`
	partition string
}

func (q cosmosTestQuery) parameter(name string) string {
	for _, parameter := range q.Parameters {
		if parameter.Name == name {
			return string(parameter.Value)
		}
	}
	return "<missing>"
}

type cosmosTestProtocol struct {
	mu         sync.Mutex
	requests   int
	documents  map[string]map[string]json.RawMessage
	partitions []string
	queries    []cosmosTestQuery
}

func newCosmosTestStore(t *testing.T, config azureprovider.CosmosDBConfig, queryHandler http.HandlerFunc) (*azureprovider.CosmosDB, *cosmosTestProtocol) {
	t.Helper()
	protocol := &cosmosTestProtocol{documents: make(map[string]map[string]json.RawMessage)}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol.mu.Lock()
		protocol.requests++
		protocol.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		writeJSON := func(value any) {
			if err := json.NewEncoder(w).Encode(value); err != nil {
				t.Errorf("encode mock response: %v", err)
			}
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			writeJSON(map[string]any{
				"id": "mock", "enableMultipleWriteLocations": false,
				"readableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
				"writableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
			})
		case r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/query+json"):
			var query cosmosTestQuery
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			query.partition = r.Header.Get("x-ms-documentdb-partitionkey")
			protocol.mu.Lock()
			protocol.queries = append(protocol.queries, query)
			protocol.mu.Unlock()
			if queryHandler != nil {
				queryHandler(w, r)
			} else {
				writeJSON(map[string]any{"Documents": []any{}})
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/docs"):
			var document map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&document); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var id string
			if err := json.Unmarshal(document["id"], &id); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			protocol.mu.Lock()
			protocol.documents[id] = document
			protocol.partitions = append(protocol.partitions, r.Header.Get("x-ms-documentdb-partitionkey"))
			protocol.mu.Unlock()
			writeJSON(document)
		case strings.Contains(r.URL.Path, "/docs/") && (r.Method == http.MethodGet || r.Method == http.MethodDelete):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			protocol.mu.Lock()
			document, found := protocol.documents[id]
			protocol.partitions = append(protocol.partitions, r.Header.Get("x-ms-documentdb-partitionkey"))
			if r.Method == http.MethodDelete {
				delete(protocol.documents, id)
			}
			protocol.mu.Unlock()
			if !found {
				w.WriteHeader(http.StatusNotFound)
				writeJSON(map[string]string{"code": "NotFound", "message": "missing"})
			} else if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
			} else {
				writeJSON(document)
			}
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("local-test-key")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: server.Client(), InsecureAllowCredentialWithHTTP: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	store, err := azureprovider.NewCosmosDB(client, "sample", config)
	if err != nil {
		t.Fatal(err)
	}
	return store, protocol
}

func TestCosmosDBByteVectorsUseNumericJSONArrays(t *testing.T) {
	for _, value := range []any{
		[]uint8{0, 255},
		embedding.VectorOf[uint8]{0, 255},
		embedding.Of[uint8]{Vector: []uint8{0, 255}},
		&embedding.Of[uint8]{Vector: []uint8{0, 255}},
	} {
		t.Run(reflect.TypeOf(value).String(), func(t *testing.T) {
			store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{}, nil)
			definition := &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
				&vectordata.VectorProperty{Name: "Vector", Type: reflect.TypeOf(value), Dimensions: 2},
			}}
			collection, err := store.DynamicCollection("records", definition)
			if err != nil {
				t.Fatal(err)
			}
			record := map[string]any{"ID": "item-1", "Vector": value}
			if err := collection.Upsert(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			protocol.mu.Lock()
			stored := string(protocol.documents["item-1"]["Vector"])
			protocol.mu.Unlock()
			if stored != "[0,255]" {
				t.Fatalf("stored byte vector = %s, want [0,255]", stored)
			}
			got, found, err := collection.Get(t.Context(), "item-1", &vectordata.RecordRetrievalOptions{IncludeVectors: true})
			if err != nil || !found || !reflect.DeepEqual(got["Vector"], value) {
				t.Fatalf("byte-vector round trip = (%#v, %v, %v)", got, found, err)
			}
			for _, err := range collection.Search(t.Context(), value, 1, &vectordata.SearchOptions{
				Filter: &vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: "item-1"},
			}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			protocol.mu.Lock()
			query := protocol.queries[0]
			protocol.mu.Unlock()
			if query.parameter("@vector") != "[0,255]" || query.partition != `["item-1"]` {
				t.Fatalf("byte-vector query = %#v; @vector = %s", query, query.parameter("@vector"))
			}
			if !reflect.DeepEqual(record["Vector"], value) {
				t.Fatal("upsert changed the input vector")
			}
		})
	}
}

type cosmosTestByteEmbeddingGenerator struct {
	calls int
}

func (g *cosmosTestByteEmbeddingGenerator) Generate(_ context.Context, values []string, _ *embedding.Options) (*embedding.Generated[*embedding.Of[uint8]], error) {
	g.calls++
	result := &embedding.Generated[*embedding.Of[uint8]]{Embeddings: make([]*embedding.Of[uint8], len(values))}
	for i := range values {
		result.Embeddings[i] = &embedding.Of[uint8]{Vector: []uint8{7, 255}}
	}
	return result, nil
}

func TestCosmosDBGeneratedByteVectorsUseNumericJSONArrays(t *testing.T) {
	generator := &cosmosTestByteEmbeddingGenerator{}
	store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{EmbeddingGenerator: embedding.Bind(generator)}, nil)
	type record struct {
		ID      string `vectordata:"key"`
		Content string `vectordata:"vector,dimensions=2"`
	}
	collection, err := vectordata.GetCollection[string, record](store.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.Upsert(t.Context(), record{ID: "item-1", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	protocol.mu.Lock()
	vector := string(protocol.documents["item-1"]["Content"])
	protocol.mu.Unlock()
	if vector != "[7,255]" {
		t.Fatalf("generated byte-vector JSON = %s", vector)
	}
	for _, err := range collection.Search(t.Context(), "query", 1, &vectordata.SearchOptions{
		Filter: &vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: "item-1"},
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	protocol.mu.Lock()
	query := protocol.queries[0]
	protocol.mu.Unlock()
	if query.parameter("@vector") != "[7,255]" || generator.calls != 2 {
		t.Fatalf("generated search: vector=%s, generator calls=%d", query.parameter("@vector"), generator.calls)
	}
}

func TestCosmosDBCompositeLookupKeysPreserveScalarRecordIDs(t *testing.T) {
	for _, keyType := range []reflect.Type{reflect.TypeFor[string](), reflect.TypeFor[uuid.UUID]()} {
		t.Run(keyType.String(), func(t *testing.T) {
			store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{PartitionKeyProperties: []string{"Tenant"}}, nil)
			definition := &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: keyType},
				&vectordata.DataProperty{Name: "Tenant", Type: reflect.TypeFor[string]()},
			}}
			collection, err := vectordata.GetCollection[azureprovider.CosmosKey, map[string]any](store.Store, "records", definition)
			if err != nil {
				t.Fatal(err)
			}
			record := map[string]any{"Tenant": "tenant-1"}
			if keyType == reflect.TypeFor[string]() {
				record["ID"] = "item-1"
			}
			if err := collection.Upsert(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			id := "item-1"
			if keyType == reflect.TypeFor[uuid.UUID]() {
				generated, ok := record["ID"].(uuid.UUID)
				if !ok || generated == uuid.Nil {
					t.Fatalf("generated record ID = %#v", record["ID"])
				}
				id = generated.String()
			}
			key := azureprovider.CosmosKey{DocumentID: id, PartitionKey: []any{"tenant-1"}}
			got, found, err := collection.Get(t.Context(), key, nil)
			if err != nil || !found || got["ID"] != record["ID"] || got["Tenant"] != "tenant-1" {
				t.Fatalf("composite-key Get = (%#v, %v, %v)", got, found, err)
			}
			if err := collection.Delete(t.Context(), key); err != nil {
				t.Fatal(err)
			}
			_, found, err = collection.Get(t.Context(), key, nil)
			if err != nil || found {
				t.Fatalf("Get after Delete = (found=%v, err=%v)", found, err)
			}
			protocol.mu.Lock()
			partitions := append([]string(nil), protocol.partitions...)
			protocol.mu.Unlock()
			if len(partitions) != 4 {
				t.Fatalf("point-operation partition headers = %#v", partitions)
			}
			for _, partition := range partitions {
				if partition != `["tenant-1"]` {
					t.Fatalf("point operation used wrong partition: %s", partition)
				}
			}
		})
	}
}

func TestCosmosDBPartitionValuesMatchDocumentJSONAndRoundTrip(t *testing.T) {
	text, number, boolean := "tenant-1", float32(0.1), true
	tests := []struct {
		name      string
		typ       reflect.Type
		value     any
		partition string
	}{
		{"float32", reflect.TypeFor[float32](), float32(0.1), "[0.1]"},
		{"float64", reflect.TypeFor[float64](), 0.1, "[0.1]"},
		{"integer", reflect.TypeFor[int64](), int64(42), "[42]"},
		{"string-pointer", reflect.TypeFor[*string](), &text, `["tenant-1"]`},
		{"float32-pointer", reflect.TypeFor[*float32](), &number, "[0.1]"},
		{"bool-pointer", reflect.TypeFor[*bool](), &boolean, "[true]"},
		{"nil-pointer", reflect.TypeFor[*string](), (*string)(nil), "[null]"},
		{"dynamic-null", reflect.TypeFor[string](), nil, "[null]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{PartitionKeyProperties: []string{"Tenant"}}, nil)
			definition := &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
				&vectordata.DataProperty{Name: "Tenant", StorageName: "tenant", Type: tt.typ},
			}}
			collection, err := store.DynamicCollection("records", definition)
			if err != nil {
				t.Fatal(err)
			}
			if err := collection.Upsert(t.Context(), map[string]any{"ID": "item-1", "Tenant": tt.value}); err != nil {
				t.Fatal(err)
			}
			protocol.mu.Lock()
			partition := protocol.partitions[0]
			stored := append(json.RawMessage(nil), protocol.documents["item-1"]["tenant"]...)
			protocol.mu.Unlock()
			if partition != tt.partition || "["+string(stored)+"]" != tt.partition {
				t.Fatalf("partition header=%s, document tenant=%s, want %s", partition, stored, tt.partition)
			}
			key := azureprovider.CosmosKey{DocumentID: "item-1", PartitionKey: []any{tt.value}}
			got, found, err := collection.Get(t.Context(), key, nil)
			if err != nil || !found || got["ID"] != "item-1" {
				t.Fatalf("partitioned Get = (%#v, %v, %v)", got, found, err)
			}
			tenantJSON, err := json.Marshal(got["Tenant"])
			if err != nil || "["+string(tenantJSON)+"]" != tt.partition {
				t.Fatalf("retrieved tenant JSON=%s, err=%v", tenantJSON, err)
			}
			for _, err := range collection.GetFiltered(t.Context(), vectordata.Filter{
				Operator: vectordata.Equal, Property: "Tenant", Value: tt.value,
			}, 1, &vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "ID"}}}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			protocol.mu.Lock()
			query := protocol.queries[0]
			protocol.mu.Unlock()
			if query.partition != tt.partition {
				t.Fatalf("query partition=%s, want %s", query.partition, tt.partition)
			}
		})
	}
}

func TestCosmosDBNilNativeVectorsAreNull(t *testing.T) {
	values := []any{
		[]float32(nil), []uint8(nil), []int8(nil),
		embedding.VectorOf[float32](nil), embedding.VectorOf[uint8](nil), embedding.VectorOf[int8](nil),
		embedding.Of[float32]{},
		embedding.Of[uint8]{},
		embedding.Of[int8]{},
		(*embedding.Of[float32])(nil), (*embedding.Of[uint8])(nil), (*embedding.Of[int8])(nil),
	}
	for _, value := range values {
		t.Run(reflect.TypeOf(value).String(), func(t *testing.T) {
			store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{}, nil)
			definition := &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
				&vectordata.VectorProperty{Name: "Vector", Type: reflect.TypeOf(value), Dimensions: 2},
			}}
			collection, err := store.DynamicCollection("records", definition)
			if err != nil {
				t.Fatal(err)
			}
			if err := collection.Upsert(t.Context(), map[string]any{"ID": "item-1", "Vector": value}); err != nil {
				t.Fatal(err)
			}
			protocol.mu.Lock()
			vector := string(protocol.documents["item-1"]["Vector"])
			protocol.mu.Unlock()
			if vector != "null" {
				t.Fatalf("nil native vector JSON = %s", vector)
			}
			if _, found, err := collection.Get(t.Context(), "item-1", &vectordata.RecordRetrievalOptions{IncludeVectors: true}); err != nil || !found {
				t.Fatalf("null-vector retrieval = (found=%v, err=%v)", found, err)
			}
		})
	}
}

func TestCosmosDBEmptyNativeVectorsRemainInvalid(t *testing.T) {
	for _, value := range []any{[]float32{}, []uint8{}, []int8{}} {
		t.Run(reflect.TypeOf(value).String(), func(t *testing.T) {
			store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{}, nil)
			definition := &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
				&vectordata.VectorProperty{Name: "Vector", Type: reflect.TypeOf(value), Dimensions: 2},
			}}
			collection, err := store.DynamicCollection("records", definition)
			if err != nil {
				t.Fatal(err)
			}
			err = collection.Upsert(t.Context(), map[string]any{"ID": "item-1", "Vector": value})
			if err == nil || !strings.Contains(err.Error(), "requires 2 dimensions") {
				t.Fatalf("empty vector upsert error = %v", err)
			}
			var searchErr error
			for _, err := range collection.Search(t.Context(), value, 1, &vectordata.SearchOptions{
				Filter: &vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: "item-1"},
			}) {
				searchErr = err
			}
			if searchErr == nil || !strings.Contains(searchErr.Error(), "dimension 2") {
				t.Fatalf("empty search vector error = %v", searchErr)
			}
			protocol.mu.Lock()
			requests := protocol.requests
			protocol.mu.Unlock()
			if requests != 0 {
				t.Fatalf("invalid vectors performed %d HTTP requests", requests)
			}
		})
	}
}

func TestCosmosDBNamedStringKeys(t *testing.T) {
	type recordID string
	type record struct {
		ID recordID `vectordata:"key"`
	}
	for _, partitionProperties := range [][]string{nil, {"ID"}} {
		t.Run(strings.Join(partitionProperties, ","), func(t *testing.T) {
			store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{PartitionKeyProperties: partitionProperties}, nil)
			collection, err := vectordata.GetCollection[recordID, record](store.Store, "records", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := collection.Upsert(t.Context(), record{ID: "item-1"}); err != nil {
				t.Fatal(err)
			}
			got, found, err := collection.Get(t.Context(), recordID("item-1"), nil)
			if err != nil || !found || got.ID != recordID("item-1") {
				t.Fatalf("named-key Get = (%#v, %v, %v)", got, found, err)
			}
			if err := collection.Delete(t.Context(), recordID("item-1")); err != nil {
				t.Fatal(err)
			}
			protocol.mu.Lock()
			partitions := append([]string(nil), protocol.partitions...)
			protocol.mu.Unlock()
			for _, partition := range partitions {
				if partition != `["item-1"]` {
					t.Fatalf("named-key partition = %s", partition)
				}
			}
		})
	}
}

func TestCosmosDBKeyMembershipFiltersNormalizeElements(t *testing.T) {
	id := uuid.MustParse("aa11aa11-aa11-4a11-8a11-aa11aa11aa11")
	tests := []struct {
		name string
		keys any
		want string
	}{
		{"composite-slice", []azureprovider.CosmosKey{{DocumentID: "item-1", PartitionKey: []any{"tenant-1"}}}, `["item-1"]`},
		{"composite-array", [1]azureprovider.CosmosKey{{DocumentID: "item-1", PartitionKey: []any{"tenant-1"}}}, `["item-1"]`},
		{"uuid", []uuid.UUID{id}, `["aa11aa11-aa11-4a11-8a11-aa11aa11aa11"]`},
		{"empty", []string{}, `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{}, nil)
			type record struct {
				ID string `vectordata:"key"`
			}
			collection, err := vectordata.GetCollection[string, record](store.Store, "records", nil)
			if err != nil {
				t.Fatal(err)
			}
			filter := vectordata.Filter{Operator: vectordata.In, Property: "ID", Value: tt.keys}
			before, err := json.Marshal(filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, err := range collection.GetFiltered(t.Context(), filter, 1, nil) {
				if err != nil {
					t.Fatal(err)
				}
			}

			protocol.mu.Lock()
			query := protocol.queries[0]
			protocol.mu.Unlock()
			if query.parameter("@p0") != tt.want || !strings.Contains(query.Query, `ARRAY_CONTAINS(@p0, c["id"])`) {
				t.Fatalf("membership query = %#v, parameter = %s", query, query.parameter("@p0"))
			}
			after, err := json.Marshal(filter)
			if err != nil || string(after) != string(before) {
				t.Fatalf("filter was changed: before=%s, after=%s, err=%v", before, after, err)
			}
		})
	}
}

func TestCosmosDBGeneratedVectorsRejectRetrievalBeforeIO(t *testing.T) {
	generator := &cosmosTestEmbeddingGenerator{}
	store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{EmbeddingGenerator: embedding.Bind(generator)}, nil)
	type record struct {
		ID      string `vectordata:"key"`
		Text    string `vectordata:"data,fulltext"`
		Content string `vectordata:"vector,dimensions=2"`
	}
	collection, err := vectordata.GetCollection[string, record](store.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	hybrid, ok := collection.(vectordata.KeywordHybridSearcher[record])
	if !ok {
		t.Fatal("missing hybrid-search capability")
	}
	tests := []struct {
		name string
		run  func() error
	}{
		{"get-missing", func() error {
			_, _, err := collection.Get(t.Context(), "missing", &vectordata.RecordRetrievalOptions{IncludeVectors: true})
			return err
		}},
		{"get-batch-missing", func() error {
			for _, err := range vectordata.GetBatch(t.Context(), collection, []string{"missing"}, &vectordata.RecordRetrievalOptions{IncludeVectors: true}) {
				return err
			}
			return nil
		}},
		{"filtered", func() error {
			for _, err := range collection.GetFiltered(t.Context(), vectordata.Filter{
				Operator: vectordata.Equal, Property: "ID", Value: "missing",
			}, 1, &vectordata.FilteredRecordRetrievalOptions{IncludeVectors: true}) {
				return err
			}
			return nil
		}},
		{"filtered-zero-top", func() error {
			for _, err := range collection.GetFiltered(t.Context(), vectordata.Filter{
				Operator: vectordata.Equal, Property: "ID", Value: "missing",
			}, 0, &vectordata.FilteredRecordRetrievalOptions{IncludeVectors: true}) {
				return err
			}
			return nil
		}},
		{"search", func() error {
			for _, err := range collection.Search(t.Context(), "query", 1, &vectordata.SearchOptions{IncludeVectors: true}) {
				return err
			}
			return nil
		}},
		{"hybrid-search", func() error {
			for _, err := range hybrid.HybridSearch(t.Context(), "query", []string{"keyword"}, 1, &vectordata.HybridSearchOptions{IncludeVectors: true}) {
				return err
			}
			return nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "IncludeVectors") {
				t.Fatalf("error = %v, want unsupported IncludeVectors", err)
			}
		})
	}
	protocol.mu.Lock()
	requests := protocol.requests
	protocol.mu.Unlock()
	if requests != 0 || generator.calls != 0 {
		t.Fatalf("unsupported retrieval performed I/O: HTTP requests=%d, embedding calls=%d", requests, generator.calls)
	}
}

func TestCosmosDBRejectsCrossPartitionRankingBeforeIO(t *testing.T) {
	generator := &cosmosTestEmbeddingGenerator{}
	store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{
		PartitionKeyProperties: []string{"Tenant", "Region"},
		EmbeddingGenerator:     embedding.Bind(generator),
	}, nil)
	type record struct {
		ID      string `vectordata:"key"`
		Tenant  string `vectordata:"data"`
		Region  string `vectordata:"data"`
		Text    string `vectordata:"data,fulltext"`
		Content string `vectordata:"vector,dimensions=2"`
	}
	collection, err := vectordata.GetCollection[azureprovider.CosmosKey, record](store.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	hybrid, ok := collection.(vectordata.KeywordHybridSearcher[record])
	if !ok {
		t.Fatal("missing hybrid-search capability")
	}
	filters := []struct {
		name   string
		filter *vectordata.Filter
	}{
		{"none", nil},
		{"partial-hierarchy", &vectordata.Filter{Operator: vectordata.Equal, Property: "Tenant", Value: "tenant-1"}},
		{"disjunction", &vectordata.Filter{Operator: vectordata.Or, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Tenant", Value: "tenant-1"},
			{Operator: vectordata.Equal, Property: "Region", Value: "region-1"},
		}}},
		{"negation", &vectordata.Filter{Operator: vectordata.Not, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Tenant", Value: "tenant-1"},
		}}},
	}
	for _, tt := range filters {
		t.Run(tt.name, func(t *testing.T) {
			var searchErr error
			for _, err := range collection.Search(t.Context(), "query", 1, &vectordata.SearchOptions{Filter: tt.filter}) {
				searchErr = err
			}
			var hybridErr error
			for _, err := range hybrid.HybridSearch(t.Context(), "query", []string{"keyword"}, 1, &vectordata.HybridSearchOptions{Filter: tt.filter}) {
				hybridErr = err
			}
			for _, err := range []error{searchErr, hybridErr} {
				if err == nil || !strings.Contains(err.Error(), "equality filters for every partition key") {
					t.Errorf("cross-partition ranking error = %v", err)
				}
			}
			if tt.filter != nil {
				var retrievalErr error
				for _, err := range collection.GetFiltered(t.Context(), *tt.filter, 1, &vectordata.FilteredRecordRetrievalOptions{
					OrderBy: []vectordata.Sort{{Property: "ID"}},
				}) {
					retrievalErr = err
				}
				if retrievalErr == nil || !strings.Contains(retrievalErr.Error(), "equality filters for every partition key") {
					t.Errorf("cross-partition ordering error = %v", retrievalErr)
				}
			}
		})
	}
	protocol.mu.Lock()
	requests := protocol.requests
	protocol.mu.Unlock()
	if requests != 0 || generator.calls != 0 {
		t.Fatalf("unsupported ranking performed I/O: HTTP requests=%d, embedding calls=%d", requests, generator.calls)
	}
}

func TestCosmosDBDefaultPartitionRejectsUnscopedSearch(t *testing.T) {
	type record struct {
		ID     string    `vectordata:"key"`
		Vector []float32 `vectordata:"vector,dimensions=2"`
	}
	store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{}, nil)
	collection, err := vectordata.GetCollection[string, record](store.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	var searchErr error
	for _, err := range collection.Search(t.Context(), []float32{1, 2}, 5, nil) {
		searchErr = err
	}
	if searchErr == nil || !strings.Contains(searchErr.Error(), `missing property "ID"`) {
		t.Fatalf("unscoped default-partition search error = %v", searchErr)
	}
	protocol.mu.Lock()
	requests := protocol.requests
	protocol.mu.Unlock()
	if requests != 0 {
		t.Fatalf("unsupported search performed %d HTTP requests", requests)
	}
}

func TestCosmosDBHierarchicalQueriesUseCompletePartition(t *testing.T) {
	store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{PartitionKeyProperties: []string{"Tenant", "Region"}}, nil)
	type record struct {
		ID     string    `vectordata:"key"`
		Tenant string    `vectordata:"data,storage=tenant"`
		Region int32     `vectordata:"data,storage=region"`
		Text   string    `vectordata:"data,fulltext"`
		Vector []float32 `vectordata:"vector,dimensions=2"`
	}
	collection, err := vectordata.GetCollection[azureprovider.CosmosKey, record](store.Store, "records", nil)
	if err != nil {
		t.Fatal(err)
	}
	filter := vectordata.Filter{Operator: vectordata.And, Operands: []vectordata.Filter{
		{Operator: vectordata.Equal, Property: "Tenant", Value: "tenant-1"},
		{Operator: vectordata.And, Operands: []vectordata.Filter{
			{Operator: vectordata.Equal, Property: "Region", Value: int32(3)},
			{Operator: vectordata.Or, Operands: []vectordata.Filter{
				{Operator: vectordata.Equal, Property: "ID", Value: "item-1"},
				{Operator: vectordata.Equal, Property: "ID", Value: "item-2"},
			}},
		}},
	}}
	for _, err := range collection.Search(t.Context(), []float32{1, 2}, 3, &vectordata.SearchOptions{Filter: &filter, Skip: 1}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	hybrid, ok := collection.(vectordata.KeywordHybridSearcher[record])
	if !ok {
		t.Fatal("missing hybrid-search capability")
	}
	for _, err := range hybrid.HybridSearch(t.Context(), []float32{1, 2}, []string{"keyword"}, 3, &vectordata.HybridSearchOptions{Filter: &filter}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range collection.GetFiltered(t.Context(), filter, 2, &vectordata.FilteredRecordRetrievalOptions{
		OrderBy: []vectordata.Sort{{Property: "ID", Descending: true}}, Skip: 1,
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	protocol.mu.Lock()
	queries := append([]cosmosTestQuery(nil), protocol.queries...)
	protocol.mu.Unlock()
	if len(queries) != 3 {
		t.Fatalf("queries = %#v", queries)
	}
	for _, query := range queries {
		if query.partition != `["tenant-1",3]` || !strings.Contains(query.Query, `c["tenant"]`) || !strings.Contains(query.Query, `c["region"]`) {
			t.Fatalf("query lost partition routing or filter: %#v", query)
		}
	}
	if !strings.Contains(queries[0].Query, "OFFSET 1 LIMIT 3") ||
		!strings.Contains(queries[1].Query, "RANK RRF") ||
		!strings.Contains(queries[2].Query, `ORDER BY c["id"] DESC`) {
		t.Fatalf("partition-scoped query shapes = %#v", queries)
	}
}

func TestCosmosDBCrossPartitionFilteredPaging(t *testing.T) {
	for _, stopEarly := range []bool{false, true} {
		name := "skip-and-top"
		if stopEarly {
			name = "consumer-stop"
		}
		t.Run(name, func(t *testing.T) {
			pages := 0
			store, protocol := newCosmosTestStore(t, azureprovider.CosmosDBConfig{}, func(w http.ResponseWriter, r *http.Request) {
				pages++
				var body string
				switch pages {
				case 1:
					if continuation := r.Header.Get("x-ms-continuation"); continuation != "" {
						t.Errorf("first page continuation = %q", continuation)
					}
					w.Header().Set("x-ms-continuation", "one")
					body = `{"Documents":[]}`
				case 2:
					if continuation := r.Header.Get("x-ms-continuation"); continuation != "one" {
						t.Errorf("second page continuation = %q", continuation)
					}
					w.Header().Set("x-ms-continuation", "two")
					body = `{"Documents":[{"id":"a","Category":"match"},{"id":"b","Category":"match"}]}`
				case 3:
					if continuation := r.Header.Get("x-ms-continuation"); continuation != "two" {
						t.Errorf("third page continuation = %q", continuation)
					}
					w.Header().Set("x-ms-continuation", "three")
					body = `{"Documents":[{"id":"c","Category":"match"},{"id":"d","Category":"match"}]}`
				default:
					t.Error("fetched an unnecessary continuation page")
					body = `{"Documents":[]}`
				}
				if _, err := io.WriteString(w, body); err != nil {
					t.Errorf("write query response: %v", err)
				}
			})
			type record struct {
				ID       string `vectordata:"key"`
				Category string `vectordata:"data"`
			}
			collection, err := vectordata.GetCollection[string, record](store.Store, "records", nil)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for item, err := range collection.GetFiltered(t.Context(), vectordata.Filter{
				Operator: vectordata.Equal, Property: "Category", Value: "match",
			}, 2, &vectordata.FilteredRecordRetrievalOptions{Skip: 1}) {
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, item.ID)
				if stopEarly {
					break
				}
			}
			wantIDs, wantPages := []string{"b", "c"}, 3
			if stopEarly {
				wantIDs, wantPages = []string{"b"}, 2
			}
			if !reflect.DeepEqual(ids, wantIDs) || pages != wantPages {
				t.Fatalf("filtered paging: IDs=%v, pages=%d, want %v/%d", ids, pages, wantIDs, wantPages)
			}
			protocol.mu.Lock()
			queries := append([]cosmosTestQuery(nil), protocol.queries...)
			protocol.mu.Unlock()
			for _, query := range queries {
				if query.partition != "" || strings.Contains(query.Query, "TOP") || strings.Contains(query.Query, "OFFSET") ||
					strings.Contains(query.Query, "LIMIT") || strings.Contains(query.Query, "ORDER BY") ||
					!strings.Contains(query.Query, `WHERE c["Category"] = @p0`) || query.parameter("@p0") != `"match"` {
					t.Fatalf("unsupported or incomplete cross-partition query: %#v", query)
				}
			}
		})
	}
}

func TestCosmosDBFilteredPagingPropagatesFailuresAndCancellation(t *testing.T) {
	for _, cancelAfterFirst := range []bool{false, true} {
		name := "service-error"
		if cancelAfterFirst {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			pages := 0
			store, _ := newCosmosTestStore(t, azureprovider.CosmosDBConfig{}, func(w http.ResponseWriter, _ *http.Request) {
				pages++
				body := `{"Documents":[{"id":"item-1"}]}`
				if pages == 1 {
					w.Header().Set("x-ms-continuation", "next")
				} else {
					w.WriteHeader(http.StatusBadRequest)
					body = `{"code":"BadRequest","message":"query failed"}`
				}
				if _, err := io.WriteString(w, body); err != nil {
					t.Errorf("write response: %v", err)
				}
			})
			type record struct {
				ID string `vectordata:"key"`
			}
			collection, err := vectordata.GetCollection[string, record](store.Store, "records", nil)
			if err != nil {
				t.Fatal(err)
			}
			var terminalErr error
			results := 0
			for _, err := range collection.GetFiltered(ctx, vectordata.Filter{
				Operator: vectordata.NotEqual, Property: "ID", Value: "other",
			}, 3, nil) {
				if err != nil {
					terminalErr = err
					continue
				}
				results++
				if cancelAfterFirst {
					cancel()
				}
			}
			if results != 1 || terminalErr == nil {
				t.Fatalf("query yielded %d results with error %v", results, terminalErr)
			}
			if cancelAfterFirst {
				if !errors.Is(terminalErr, context.Canceled) || pages != 1 {
					t.Fatalf("canceled query: pages=%d, err=%v", pages, terminalErr)
				}
			} else {
				responseErr, ok := errors.AsType[*azcore.ResponseError](terminalErr)
				if !ok || responseErr.StatusCode != http.StatusBadRequest || pages != 2 {
					t.Fatalf("failed query: pages=%d, err=%v", pages, terminalErr)
				}
			}
		})
	}
}
