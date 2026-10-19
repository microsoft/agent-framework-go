// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

func TestAISearch_IndexFieldTypes(t *testing.T) {
	for _, test := range []struct {
		typ  reflect.Type
		want string
	}{
		{reflect.TypeFor[string](), "Edm.String"},
		{reflect.TypeFor[*string](), "Edm.String"},
		{reflect.TypeFor[bool](), "Edm.Boolean"},
		{reflect.TypeFor[int32](), "Edm.Int32"},
		{reflect.TypeFor[int](), "Edm.Int64"},
		{reflect.TypeFor[int64](), "Edm.Int64"},
		{reflect.TypeFor[float32](), "Edm.Double"},
		{reflect.TypeFor[float64](), "Edm.Double"},
		{reflect.TypeFor[time.Time](), "Edm.DateTimeOffset"},
		{reflect.TypeFor[*time.Time](), "Edm.DateTimeOffset"},
		{reflect.TypeFor[[]string](), "Collection(Edm.String)"},
		{reflect.TypeFor[[2]int32](), "Collection(Edm.Int32)"},
		{reflect.TypeFor[[]bool](), "Collection(Edm.Boolean)"},
		{reflect.TypeFor[[]float32](), "Collection(Edm.Double)"},
		{reflect.TypeFor[[]time.Time](), "Collection(Edm.DateTimeOffset)"},
	} {
		t.Run(test.typ.String(), func(t *testing.T) {
			var field map[string]any
			store := newStore(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return response(http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
				}
				fields := jsonBody(t, request)["fields"].([]any)
				field = fields[1].(map[string]any)
				return response(http.StatusCreated, `{}`), nil
			}, azureprovider.AISearchConfig{})
			definition := &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
				&vectordata.DataProperty{Name: "Value", Type: test.typ, IsIndexed: true},
			}}
			collection := openCollection[string, map[string]any](t, store, definition)
			if err := collection.EnsureExists(t.Context()); err != nil {
				t.Fatal(err)
			}
			collectionType := test.typ.Kind() == reflect.Array || test.typ.Kind() == reflect.Slice
			if field["type"] != test.want || field["filterable"] != true || field["sortable"] != !collectionType {
				t.Fatalf("mapped field = %v", field)
			}
		})
	}
}

func TestAISearch_VectorIndexConfiguration(t *testing.T) {
	for _, test := range []struct {
		index      vectordata.IndexKind
		distance   vectordata.DistanceFunction
		kind       string
		parameters string
		metric     string
	}{
		{vectordata.HNSW, vectordata.CosineSimilarity, "hnsw", "hnswParameters", "cosine"},
		{vectordata.Flat, vectordata.DotProductSimilarity, "exhaustiveKnn", "exhaustiveKnnParameters", "dotProduct"},
		{vectordata.Flat, vectordata.EuclideanDistance, "exhaustiveKnn", "exhaustiveKnnParameters", "euclidean"},
	} {
		t.Run(string(test.index)+"/"+string(test.distance), func(t *testing.T) {
			var schema map[string]any
			store := newStore(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return response(http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
				}
				schema = jsonBody(t, request)
				return response(http.StatusCreated, `{}`), nil
			}, azureprovider.AISearchConfig{})
			definition := &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.VectorProperty{Name: "Vector", StorageName: "embedding", Dimensions: 3, IndexKind: test.index, DistanceFunction: test.distance},
			}}
			if err := openCollection[string, document](t, store, definition).EnsureExists(t.Context()); err != nil {
				t.Fatal(err)
			}
			vectorSearch := schema["vectorSearch"].(map[string]any)
			algorithm := vectorSearch["algorithms"].([]any)[0].(map[string]any)
			profile := vectorSearch["profiles"].([]any)[0].(map[string]any)
			if algorithm["kind"] != test.kind || algorithm[test.parameters].(map[string]any)["metric"] != test.metric ||
				profile["name"] != "embeddingProfile" || profile["algorithm"] != "embeddingAlgoConfig" {
				t.Fatalf("vector index = %v", vectorSearch)
			}
		})
	}
}

func TestAISearch_RejectsUnsupportedSchemas(t *testing.T) {
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Error("invalid index configuration was submitted")
		}
		return response(http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
	}, azureprovider.AISearchConfig{})
	for _, typ := range []reflect.Type{
		reflect.TypeFor[map[string]any](), reflect.TypeFor[uint64](), reflect.TypeFor[[][]string](), reflect.TypeFor[complex64](),
	} {
		definition := &vectordata.Definition{Properties: []vectordata.Property{
			&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
			&vectordata.DataProperty{Name: "Data", Type: typ},
		}}
		if _, err := store.DynamicCollection("records", definition); err == nil {
			t.Fatalf("accepted unsupported data type %v", typ)
		}
	}
	for _, storageName := range []string{"invalid-name", "value/child", "value eq 'x'", "1st", "@search.action"} {
		if _, err := vectordata.GetCollection[string, document](store.Store, "records", &vectordata.Definition{
			Properties: []vectordata.Property{&vectordata.DataProperty{Name: "Text", StorageName: storageName}},
		}); err == nil {
			t.Fatalf("accepted unsafe field name %q", storageName)
		}
	}
	for _, property := range []vectordata.Property{
		&vectordata.VectorProperty{Name: "Vector", Dimensions: 3, IndexKind: vectordata.IVFFlat},
		&vectordata.VectorProperty{Name: "Vector", Dimensions: 3, DistanceFunction: vectordata.CosineDistance},
		&vectordata.DataProperty{Name: "Rank", IsFullTextIndexed: true},
	} {
		collection := openCollection[string, document](t, store, &vectordata.Definition{Properties: []vectordata.Property{property}})
		if err := collection.EnsureExists(t.Context()); err == nil {
			t.Fatalf("accepted unsupported index configuration %+v", property)
		}
	}
}

func TestAISearch_NullableAndNamedDynamicRecords(t *testing.T) {
	type named map[string]any
	definition := &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
		&vectordata.DataProperty{Name: "Text", Type: reflect.TypeFor[*string](), IsFullTextIndexed: true},
		&vectordata.DataProperty{Name: "Number", Type: reflect.TypeFor[*int32]()},
	}}
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodGet:
			if request.URL.Path == "/indexes('records')" {
				return response(http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
			}
			return response(http.StatusOK, `{"ID":"one","Text":null,"Number":null}`), nil
		default:
			body := jsonBody(t, request)
			if request.URL.Path == "/indexes" {
				if field := body["fields"].([]any)[1].(map[string]any); field["searchable"] != true || field["type"] != "Edm.String" {
					t.Errorf("nullable full-text field = %v", field)
				}
				return response(http.StatusCreated, `{}`), nil
			}
			return indexedResponse(t, body["value"].([]any), "ID"), nil
		}
	}, azureprovider.AISearchConfig{})
	collection := openCollection[string, *named](t, store, definition)
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	input := named{"ID": "one", "Text": (*string)(nil), "Number": (*int32)(nil)}
	if err := collection.Upsert(t.Context(), &input); err != nil {
		t.Fatal(err)
	}
	output, found, err := collection.Get(t.Context(), "one", nil)
	if err != nil || !found || (*output)["Text"] != nil || (*output)["Number"] != nil {
		data, _ := json.Marshal(output)
		t.Fatalf("nullable dynamic record = (%s, %v, %v)", data, found, err)
	}
}
