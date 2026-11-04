// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

func indexedResponse(t *testing.T, actions []any, keyName string) *http.Response {
	t.Helper()
	results := make([]map[string]any, len(actions))
	for i, action := range actions {
		results[i] = map[string]any{"key": action.(map[string]any)[keyName], "status": true, "statusCode": 200}
	}
	body, err := json.Marshal(map[string]any{"value": results})
	if err != nil {
		t.Fatal(err)
	}
	return response(http.StatusOK, string(body))
}

func TestCollection_CRUDAndBatching(t *testing.T) {
	for _, includeVectors := range []bool{false, true} {
		t.Run(fmt.Sprintf("includeVectors=%t", includeVectors), func(t *testing.T) {
			calls := 0
			var writes [][]any
			store := newStore(t, func(request *http.Request) (*http.Response, error) {
				calls++
				switch request.Method {
				case http.MethodPost:
					if request.URL.Path != "/indexes('records')/docs/index" {
						t.Errorf("indexing path = %q", request.URL.Path)
					}
					actions := jsonBody(t, request)["value"].([]any)
					writes = append(writes, actions)
					return indexedResponse(t, actions, "id"), nil
				case http.MethodGet:
					selectFields := request.URL.Query().Get("$select")
					if includeVectors && selectFields != "" || !includeVectors && selectFields != "id,text,rank,tags" {
						t.Errorf("selected fields = %q", selectFields)
					}
					if strings.HasSuffix(request.URL.Path, "/docs('missing')") {
						return response(http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
					}
					return response(http.StatusOK, `{"id":"one","text":"saved","rank":42,"tags":["a","b"],"vector":[1,2,3],"unknown":"ignored"}`), nil
				default:
					t.Fatalf("unexpected method %s", request.Method)
					return nil, nil
				}
			}, azureprovider.AISearchConfig{})
			collection := openCollection[string, document](t, store, nil)
			input := []document{
				{ID: "one", Text: "first", Rank: 1, Tags: []string{"a"}, Vector: []float32{1, 2, 3}, Other: "not in schema"},
				{ID: "two", Text: "second", Vector: []float32{3, 2, 1}},
			}
			before := slices.Clone(input)
			if err := collection.Upsert(t.Context(), input...); err != nil {
				t.Fatal(err)
			}
			if len(writes) != 1 || len(writes[0]) != 2 || calls != 1 || !reflect.DeepEqual(input, before) {
				t.Fatalf("writes=%v calls=%d input=%v", writes, calls, input)
			}
			first := writes[0][0].(map[string]any)
			if first["@search.action"] != "upload" || first["id"] != "one" || first["text"] != "first" ||
				!reflect.DeepEqual(first["vector"], []any{float64(1), float64(2), float64(3)}) {
				t.Fatalf("upsert action = %v", first)
			}
			if _, found := first["Other"]; found {
				t.Fatal("uploaded an unconfigured field")
			}
			options := &vectordata.RecordRetrievalOptions{IncludeVectors: includeVectors}
			records := collect(t, vectordata.GetBatch(t.Context(), collection, []string{"one", "missing", "two"}, options))
			if len(records) != 2 || records[0].Text != "saved" || records[0].Rank != 42 ||
				!slices.Equal(records[0].Tags, []string{"a", "b"}) || records[0].Other != "" {
				t.Fatalf("GetBatch = %v", records)
			}
			if includeVectors && !slices.Equal(records[0].Vector, []float32{1, 2, 3}) ||
				!includeVectors && records[0].Vector != nil {
				t.Fatalf("retrieved vector = %v", records[0].Vector)
			}
			if err := vectordata.DeleteBatch(t.Context(), collection, []string{"one", "two"}); err != nil {
				t.Fatal(err)
			}
			if len(writes) != 2 || len(writes[1]) != 2 {
				t.Fatalf("deletion did not use a single batch: %v", writes)
			}
			for i, key := range []string{"one", "two"} {
				if action := writes[1][i]; !reflect.DeepEqual(action, map[string]any{"@search.action": "delete", "id": key}) {
					t.Fatalf("delete action = %v", action)
				}
			}
			if err := collection.Delete(t.Context(), "one"); err != nil {
				t.Fatal(err)
			}
			beforeCalls := calls
			for _, batch := range [][]document{nil, {}} {
				if err := collection.Upsert(t.Context(), batch...); err != nil {
					t.Fatal(err)
				}
			}
			for _, keys := range [][]string{nil, {}} {
				if err := vectordata.DeleteBatch(t.Context(), collection, keys); err != nil {
					t.Fatal(err)
				}
				collect(t, vectordata.GetBatch(t.Context(), collection, keys, options))
			}
			if calls != beforeCalls {
				t.Fatal("empty batches performed I/O")
			}
		})
	}
}

func TestCollection_UUIDKeysAndDynamicRecords(t *testing.T) {
	type entry struct {
		ID   uuid.UUID `vectordata:"key,storage=id"`
		Text string    `vectordata:"data,storage=text"`
	}
	var uploaded map[string]any
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost {
			actions := jsonBody(t, request)["value"].([]any)
			uploaded = actions[0].(map[string]any)
			return indexedResponse(t, actions, "id"), nil
		}
		body, err := json.Marshal(uploaded)
		if err != nil {
			t.Fatal(err)
		}
		return response(http.StatusOK, string(body)), nil
	}, azureprovider.AISearchConfig{})
	pointers := openCollection[uuid.UUID, *entry](t, store, nil)
	input := &entry{Text: "pointer"}
	if err := pointers.Upsert(t.Context(), input); err != nil || input.ID == uuid.Nil || uploaded["id"] != input.ID.String() {
		t.Fatalf("generated key = %v, document = %v, error = %v", input.ID, uploaded, err)
	}
	got, found, err := pointers.Get(t.Context(), input.ID, nil)
	if err != nil || !found || !reflect.DeepEqual(got, input) || got == input {
		t.Fatalf("pointer retrieval = (%v, %v, %v)", got, found, err)
	}
	values := openCollection[uuid.UUID, entry](t, store, nil)
	value := entry{Text: "value"}
	if err := values.Upsert(t.Context(), value); err != nil || value.ID != uuid.Nil {
		t.Fatalf("value record mutation = (%v, %v)", value, err)
	}
	if key, err := uuid.Parse(uploaded["id"].(string)); err != nil || key == uuid.Nil {
		t.Fatalf("stored struct key = (%v, %v)", key, err)
	}
	keyProperty := &vectordata.KeyProperty{Name: "ID", StorageName: "id", Type: reflect.TypeFor[uuid.UUID]()}
	definition := &vectordata.Definition{Properties: []vectordata.Property{
		keyProperty,
		&vectordata.DataProperty{Name: "Text", StorageName: "text", Type: reflect.TypeFor[string]()},
		&vectordata.DataProperty{Name: "When", StorageName: "when", Type: reflect.TypeFor[*time.Time]()},
	}}
	dynamic, err := store.DynamicCollection("records", definition)
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("offset", 2*60*60))
	record := map[string]any{"Text": "dynamic", "When": &instant, "Other": "ignored"}
	if err := dynamic.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	key, ok := record["ID"].(uuid.UUID)
	if !ok || key == uuid.Nil || uploaded["id"] != key.String() || uploaded["when"] != instant.Format(time.RFC3339) {
		t.Fatalf("dynamic record = %v; uploaded = %v", record, uploaded)
	}
	gotMap, found, err := dynamic.Get(t.Context(), key, nil)
	if err != nil || !found || gotMap["ID"] != key || gotMap["Text"] != "dynamic" ||
		!gotMap["When"].(*time.Time).Equal(instant) {
		t.Fatalf("dynamic retrieval = (%v, %v, %v)", gotMap, found, err)
	}
	gotMap["Text"] = "changed"
	if record["Text"] != "dynamic" || keyProperty.IsAutoGenerated != nil {
		t.Fatal("retrieval or model resolution modified caller state")
	}
}

func TestCollection_NilCollectionFields(t *testing.T) {
	type record struct {
		ID      string    `vectordata:"key,storage=id"`
		Tags    []string  `vectordata:"data,storage=tags"`
		Numbers *[]int32  `vectordata:"data,storage=numbers"`
		Text    *string   `vectordata:"data,storage=text"`
		Vector  []float32 `vectordata:"vector,dimensions=3,storage=vector"`
	}
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		actions := jsonBody(t, request)["value"].([]any)
		document := actions[0].(map[string]any)
		for _, name := range []string{"tags", "numbers", "vector"} {
			if value, ok := document[name].([]any); !ok || len(value) != 0 {
				t.Errorf("field %s = %#v, want an empty array", name, document[name])
			}
		}
		if value, found := document["text"]; !found || value != nil {
			t.Errorf("nullable scalar = %#v, present=%v", value, found)
		}
		return indexedResponse(t, actions, "id"), nil
	}, azureprovider.AISearchConfig{})
	collection := openCollection[string, *record](t, store, nil)
	input := &record{ID: "one", Numbers: new([]int32(nil))}
	if err := collection.Upsert(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if input.Tags != nil || *input.Numbers != nil || input.Text != nil || input.Vector != nil {
		t.Fatalf("upsert changed the input: %+v", input)
	}
}

func TestCollection_CollectionFieldSerialization(t *testing.T) {
	for _, test := range []struct {
		name   string
		typ    reflect.Type
		value  any
		vector bool
		want   any
	}{
		{"nil data", reflect.TypeFor[[]string](), nil, false, []any{}},
		{"nil slice", reflect.TypeFor[[]string](), []string(nil), false, []any{}},
		{"empty slice", reflect.TypeFor[[]string](), []string{}, false, []any{}},
		{"nonempty slice", reflect.TypeFor[[]string](), []string{"one"}, false, []any{"one"}},
		{"nil slice pointer", reflect.TypeFor[*[]string](), (*[]string)(nil), false, []any{}},
		{"pointer to nil slice", reflect.TypeFor[*[]string](), new([]string(nil)), false, []any{}},
		{"pointer to nonempty slice", reflect.TypeFor[*[]string](), new([]string{"one"}), false, []any{"one"}},
		{"nil array pointer", reflect.TypeFor[*[2]string](), (*[2]string)(nil), false, []any{}},
		{"nonempty array", reflect.TypeFor[[2]string](), [2]string{"one", "two"}, false, []any{"one", "two"}},
		{"untyped nil vector", reflect.TypeFor[[]float32](), nil, true, []any{}},
		{"nil vector", reflect.TypeFor[[]float32](), []float32(nil), true, []any{}},
		{"nil embedding vector", reflect.TypeFor[embedding.VectorOf[float32]](), embedding.VectorOf[float32](nil), true, []any{}},
		{"empty embedding", reflect.TypeFor[embedding.Of[float32]](), embedding.Of[float32]{}, true, []any{}},
		{"nil embedding pointer", reflect.TypeFor[*embedding.Of[float32]](), (*embedding.Of[float32])(nil), true, []any{}},
		{"pointer to empty embedding", reflect.TypeFor[*embedding.Of[float32]](), &embedding.Of[float32]{}, true, []any{}},
		{"nil scalar pointer", reflect.TypeFor[*string](), (*string)(nil), false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newStore(t, func(request *http.Request) (*http.Response, error) {
				actions := jsonBody(t, request)["value"].([]any)
				document := actions[0].(map[string]any)
				if value, found := document["value"]; !found || !reflect.DeepEqual(value, test.want) {
					t.Errorf("serialized value = %#v, present=%v; want %#v", value, found, test.want)
				}
				return indexedResponse(t, actions, "id"), nil
			}, azureprovider.AISearchConfig{})
			var property vectordata.Property = &vectordata.DataProperty{Name: "Value", StorageName: "value", Type: test.typ}
			if test.vector {
				property = &vectordata.VectorProperty{Name: "Value", StorageName: "value", Type: test.typ, Dimensions: 3}
			}
			collection, err := store.DynamicCollection("records", &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", StorageName: "id", Type: reflect.TypeFor[string]()},
				property,
			}})
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"ID": "one", "Value": test.value}
			before, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := collection.Upsert(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(input)
			if err != nil || string(after) != string(before) {
				t.Fatalf("upsert changed the input: before=%s after=%s err=%v", before, after, err)
			}
		})
	}
}

func TestCollection_NativeEmbeddingRepresentations(t *testing.T) {
	for _, test := range []struct {
		name  string
		typ   reflect.Type
		value any
	}{
		{"slice", reflect.TypeFor[[]float32](), []float32{1, 2, 3}},
		{"vector", reflect.TypeFor[embedding.VectorOf[float32]](), embedding.VectorOf[float32]{1, 2, 3}},
		{"embedding", reflect.TypeFor[embedding.Of[float32]](), embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 2, 3}}},
		{"pointer", reflect.TypeFor[*embedding.Of[float32]](), &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 2, 3}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newStore(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodPost {
					actions := jsonBody(t, request)["value"].([]any)
					vector := actions[0].(map[string]any)["Vector"]
					if !reflect.DeepEqual(vector, []any{float64(1), float64(2), float64(3)}) {
						t.Fatalf("wire vector = %v", vector)
					}
					return indexedResponse(t, actions, "ID"), nil
				}
				return response(http.StatusOK, `{"ID":"one","Vector":[1,2,3]}`), nil
			}, azureprovider.AISearchConfig{})
			collection, err := store.DynamicCollection("records", &vectordata.Definition{Properties: []vectordata.Property{
				&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
				&vectordata.VectorProperty{Name: "Vector", Type: test.typ, Dimensions: 3},
			}})
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"ID": "one", "Vector": test.value}
			if err := collection.Upsert(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			output, found, err := collection.Get(t.Context(), "one", &vectordata.RecordRetrievalOptions{IncludeVectors: true})
			if err != nil || !found || !reflect.DeepEqual(output["Vector"], test.value) {
				t.Fatalf("vector roundtrip = (%v, %v, %v)", output, found, err)
			}
		})
	}
}

func TestCollection_PartialWriteFailures(t *testing.T) {
	for _, operation := range []string{"upload", "delete"} {
		for _, payload := range []string{
			`{"value":[{"key":"one","status":true,"statusCode":200},{"key":"two","status":false,"statusCode":400,"errorMessage":"bad document"}]}`,
			`{"value":[{"key":"one","status":true},{"key":"two"}]}`,
			`{"value":[]}`,
		} {
			t.Run(operation+"/"+payload, func(t *testing.T) {
				calls := 0
				store := newStore(t, func(request *http.Request) (*http.Response, error) {
					calls++
					return response(http.StatusMultiStatus, payload), nil
				}, azureprovider.AISearchConfig{})
				collection := openCollection[string, document](t, store, nil)
				var err error
				if operation == "upload" {
					err = collection.Upsert(t.Context(), document{ID: "one"}, document{ID: "two"})
				} else {
					err = vectordata.DeleteBatch(t.Context(), collection, []string{"one", "two"})
				}
				var vectorErr *vectordata.Error
				if !errors.As(err, &vectorErr) || calls != 1 {
					t.Fatalf("partial batch did not fail: %v; calls=%d", err, calls)
				}
				if strings.Contains(payload, "bad document") && !strings.Contains(err.Error(), "bad document") {
					t.Fatalf("batch failure details were lost: %v", err)
				}
			})
		}
	}
}

func TestCollection_InvalidRecordsKeysAndResponses(t *testing.T) {
	calls := 0
	var payload string
	store := newStore(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusOK, payload), nil
	}, azureprovider.AISearchConfig{})
	collection := openCollection[string, *document](t, store, nil)
	if err := collection.Upsert(t.Context(), nil); err == nil {
		t.Fatal("accepted nil record")
	}
	for _, key := range []string{"", " \t"} {
		if _, _, err := collection.Get(t.Context(), key, nil); err == nil {
			t.Fatal("accepted blank retrieval key")
		}
		if err := collection.Delete(t.Context(), key); err == nil {
			t.Fatal("accepted blank deletion key")
		}
	}
	if calls != 0 {
		t.Fatal("invalid input performed I/O")
	}
	for _, invalid := range []string{
		`not json`, `null`, `{}`, `{"id":null}`, `{"id":7}`, `{"id":""}`, `{"id":"one","rank":"bad"}`, `{"id":"one","vector":"bad"}`,
	} {
		payload = invalid
		if _, _, err := collection.Get(t.Context(), "one", &vectordata.RecordRetrievalOptions{IncludeVectors: true}); err == nil {
			t.Fatalf("accepted malformed document %s", invalid)
		}
	}
}

func TestCollection_CancellationAndProviderErrors(t *testing.T) {
	wantErr := errors.New("transport failed")
	calls := 0
	store := newStore(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, wantErr
	}, azureprovider.AISearchConfig{})
	collection := openCollection[string, document](t, store, nil)
	if _, _, err := collection.Get(t.Context(), "one", nil); !errors.Is(err, wantErr) {
		t.Fatalf("transport error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := calls
	for _, err := range []error{
		collection.Upsert(ctx, document{ID: "one"}),
		collection.Delete(ctx, "one"),
		collection.EnsureExists(ctx),
		collection.EnsureDeleted(ctx),
		sequenceError(t, collection.Search(ctx, []float32{1, 2, 3}, 1, nil)),
	} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	}
	if calls != before {
		t.Fatal("canceled operations reached the transport")
	}
	store = newStore(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusForbidden, `{"error":{"code":"Forbidden","message":"denied"}}`), nil
	}, azureprovider.AISearchConfig{})
	_, _, err := openCollection[string, document](t, store, nil).Get(t.Context(), "one", nil)
	var serviceErr *azcore.ResponseError
	if !errors.As(err, &serviceErr) || serviceErr.ErrorCode != "Forbidden" {
		t.Fatalf("service failure was not preserved: %v", err)
	}
}

func TestCollection_BatchGetStopsWithConsumer(t *testing.T) {
	calls := 0
	store := newStore(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusOK, `{"id":"one"}`), nil
	}, azureprovider.AISearchConfig{})
	collection := openCollection[string, document](t, store, nil)
	sequence := vectordata.GetBatch(t.Context(), collection, []string{"one", "two"}, nil)
	if calls != 0 {
		t.Fatal("batch retrieval started eagerly")
	}
	for _, err := range sequence {
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if calls != 1 {
		t.Fatalf("consumer break retrieved %d documents", calls)
	}
}
