// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

type generatorFunc func(context.Context, []string, *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error)

func (f generatorFunc) Generate(ctx context.Context, values []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
	return f(ctx, values, options)
}

func vectors(values []string) *embedding.Generated[*embedding.Of[float32]] {
	result := &embedding.Generated[*embedding.Of[float32]]{}
	for range values {
		result.Embeddings = append(result.Embeddings, &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 2, 3}})
	}
	return result
}

type generatedDocument struct {
	ID      uuid.UUID `vectordata:"key,storage=id"`
	Text    string    `vectordata:"data,fulltext,storage=text"`
	Summary string    `vectordata:"vector,dimensions=3,storage=summary"`
	Details string    `vectordata:"vector,dimensions=3,storage=details"`
}

func TestCollection_BatchedEmbeddingGenerationAndQueries(t *testing.T) {
	var batches [][]string
	generator := embedding.Bind(generatorFunc(func(_ context.Context, values []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
		if options != nil {
			t.Errorf("generation options = %+v; expected provider defaults", options)
		}
		batches = append(batches, slices.Clone(values))
		return vectors(values), nil
	}))
	var uploaded []any
	var search map[string]any
	calls := 0
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method == http.MethodGet {
			return response(http.StatusOK, `{"id":"af1b47a5-b046-49b5-aacd-53f219f9958d","text":"stored"}`), nil
		}
		body := jsonBody(t, request)
		if strings.HasSuffix(request.URL.Path, "/docs/index") {
			uploaded = body["value"].([]any)
			return indexedResponse(t, uploaded, "id"), nil
		}
		search = body
		return response(http.StatusOK, `{"value":[]}`), nil
	}, azureprovider.AISearchConfig{EmbeddingGenerator: generator})
	collection := openCollection[uuid.UUID, *generatedDocument](t, store, nil)
	inputs := []*generatedDocument{
		{Text: "one", Summary: "summary one", Details: "details one"},
		{Text: "two", Summary: "summary two", Details: "details two"},
	}
	if err := collection.Upsert(t.Context(), inputs...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batches, [][]string{{"summary one", "summary two"}, {"details one", "details two"}}) ||
		inputs[0].ID == uuid.Nil || inputs[0].Summary != "summary one" {
		t.Fatalf("generation batches = %v; inputs = %v", batches, inputs)
	}
	first := uploaded[0].(map[string]any)
	if !reflect.DeepEqual(first["summary"], []any{float64(1), float64(2), float64(3)}) ||
		!reflect.DeepEqual(first["details"], first["summary"]) || first["text"] != "one" {
		t.Fatalf("generated wire record = %v", first)
	}
	sequence := collection.Search(t.Context(), "query", 3, &vectordata.SearchOptions{VectorProperty: "Summary"})
	if len(batches) != 2 {
		t.Fatal("query generation started eagerly")
	}
	collect(t, sequence)
	query := search["vectorQueries"].([]any)[0].(map[string]any)
	if query["kind"] != "vector" || query["fields"] != "summary" || len(batches) != 3 || !slices.Equal(batches[2], []string{"query"}) {
		t.Fatalf("query = %v; generation = %v", query, batches)
	}
	got, found, err := collection.Get(t.Context(), inputs[0].ID, nil)
	if err != nil || !found || got.Summary != "" || got.Text != "stored" {
		t.Fatalf("generated record retrieval = (%v, %v, %v)", got, found, err)
	}
	before := calls
	if _, _, err := collection.Get(t.Context(), inputs[0].ID, &vectordata.RecordRetrievalOptions{IncludeVectors: true}); err == nil {
		t.Fatal("generated-vector retrieval was accepted")
	}
	_ = sequenceError(t, collection.Search(t.Context(), "query", 1, &vectordata.SearchOptions{VectorProperty: "Summary", IncludeVectors: true}))
	_ = sequenceError(t, collection.GetFiltered(t.Context(), vectordata.Filter{Operator: vectordata.Equal, Property: "ID", Value: inputs[0].ID}, 1,
		&vectordata.FilteredRecordRetrievalOptions{IncludeVectors: true}))
	hybrid := collection.(vectordata.KeywordHybridSearcher[*generatedDocument])
	_ = sequenceError(t, hybrid.HybridSearch(t.Context(), "query", []string{"query"}, 1,
		&vectordata.HybridSearchOptions{VectorProperty: "Summary", IncludeVectors: true}))
	if calls != before || len(batches) != 3 {
		t.Fatal("rejected retrieval performed I/O or generated embeddings")
	}
}

func TestCollection_EmbeddingPrecedenceAndOwnership(t *testing.T) {
	type entry struct {
		ID   string `vectordata:"key,storage=id"`
		Text string `vectordata:"vector,dimensions=3"`
	}
	for level := range 3 {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			var calls []int
			generators := make([]*embedding.BoundGenerator, 3)
			for i := range generators {
				generators[i] = embedding.Bind(generatorFunc(func(_ context.Context, values []string, _ *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
					calls = append(calls, i)
					return vectors(values), nil
				}))
			}
			property := &vectordata.VectorProperty{Name: "Text", Dimensions: 3}
			definition := &vectordata.Definition{Properties: []vectordata.Property{property}}
			if level >= 1 {
				definition.EmbeddingGenerator = generators[1]
			}
			if level >= 2 {
				property.EmbeddingGenerator = generators[2]
			}
			store := newStore(t, func(request *http.Request) (*http.Response, error) {
				body := jsonBody(t, request)
				if actions, found := body["value"]; found {
					return indexedResponse(t, actions.([]any), "id"), nil
				}
				return response(http.StatusOK, `{"value":[]}`), nil
			}, azureprovider.AISearchConfig{EmbeddingGenerator: generators[0]})
			collection := openCollection[string, entry](t, store, definition)
			if err := collection.Upsert(t.Context(), entry{ID: "one", Text: "text"}); err != nil {
				t.Fatal(err)
			}
			collect(t, collection.Search(t.Context(), "query", 1, nil))
			if !slices.Equal(calls, []int{level, level}) || property.Type != nil || property.EmbeddingType != nil || property.StorageName != "" {
				t.Fatalf("generation=%v property=%+v", calls, property)
			}
			if level == 0 && definition.EmbeddingGenerator != nil || level >= 1 && definition.EmbeddingGenerator != generators[1] {
				t.Fatal("store defaults changed the input definition")
			}
		})
	}
}

func TestCollection_EmbeddingErrorsDoNotUploadOrGenerateKeys(t *testing.T) {
	wantErr := errors.New("embedding failure")
	for _, test := range []struct {
		name   string
		result *embedding.Generated[*embedding.Of[float32]]
		err    error
	}{
		{"provider error", nil, wantErr},
		{"nil batch", nil, nil},
		{"wrong count", vectors([]string{"one"}), nil},
		{"nil embedding", &embedding.Generated[*embedding.Of[float32]]{Embeddings: []*embedding.Of[float32]{nil, nil}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			generator := embedding.Bind(generatorFunc(func(context.Context, []string, *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
				return test.result, test.err
			}))
			store := newStore(t, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected request")
			}, azureprovider.AISearchConfig{EmbeddingGenerator: generator})
			collection := openCollection[uuid.UUID, *generatedDocument](t, store, nil)
			input := &generatedDocument{}
			err := collection.Upsert(t.Context(), input, &generatedDocument{})
			if err == nil || test.err != nil && !errors.Is(err, wantErr) || calls != 0 || input.ID != uuid.Nil {
				t.Fatalf("failure=%v calls=%d key=%v", err, calls, input.ID)
			}
		})
	}
}

func TestAISearch_ChatHistoryMemory(t *testing.T) {
	var stored []map[string]any
	exists := false
	var logs bytes.Buffer
	store := newStore(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet:
			if !exists {
				return response(http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
			}
			return response(http.StatusOK, `{"name":"records"}`), nil
		case request.URL.Path == "/indexes":
			definition := jsonBody(t, request)
			fields := definition["fields"].([]any)
			if key := fields[0].(map[string]any); key["name"] != "Key" || key["type"] != "Edm.String" {
				t.Errorf("memory key schema = %v", key)
			}
			exists = true
			return response(http.StatusCreated, `{"name":"records"}`), nil
		case strings.HasSuffix(request.URL.Path, "/docs/index"):
			actions := jsonBody(t, request)["value"].([]any)
			for _, action := range actions {
				value := action.(map[string]any)
				if _, err := uuid.Parse(value["Key"].(string)); err != nil {
					t.Errorf("memory key was not encoded as a UUID string: %v", value["Key"])
				}
				stored = append(stored, value)
			}
			return indexedResponse(t, actions, "Key"), nil
		default:
			body := jsonBody(t, request)
			if body["filter"] != "(UserId eq 'u''1')" {
				t.Errorf("memory search scope = %v", body["filter"])
			}
			results := make([]map[string]any, len(stored))
			copy(results, stored)
			payload, err := json.Marshal(map[string]any{"value": results})
			if err != nil {
				t.Fatal(err)
			}
			return response(http.StatusOK, string(payload)), nil
		}
	}, azureprovider.AISearchConfig{EmbeddingGenerator: embedding.Bind(generatorFunc(func(_ context.Context, values []string, _ *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
		return vectors(values), nil
	}))})
	memories, err := memory.NewHistoryProvider(store.Store, "records", 3, func(*agent.Session) *memory.State {
		return &memory.State{
			StorageScope: memory.Scope{UserID: new("u'1"), SessionID: new(uuid.NewString())},
			SearchScope:  &memory.Scope{UserID: new("u'1")},
		}
	}, memory.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(nil).AddText("A pirate joke.").
		NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
			if len(messages) != 2 || !strings.Contains(messages[1].String(), "I like pirate jokes.") {
				t.Fatalf("new session did not receive prior memories: %v", messages)
			}
		}).AddText("Another pirate joke.").Build()}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ContextProviders: []agent.ContextProvider{memories}})
	for _, query := range []string{"I like pirate jokes.", "Tell me a joke I might like."} {
		session, err := a.CreateSession(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.RunText(t.Context(), query, agent.WithSession(session)).Collect(); err != nil {
			t.Fatal(err)
		}
	}
	if len(stored) != 4 || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("stored=%d diagnostics=%s", len(stored), logs.String())
	}
}

func TestCollection_SearchEmbeddingFailures(t *testing.T) {
	wantErr := errors.New("query embedding failed")
	for _, test := range []struct {
		name   string
		result *embedding.Generated[*embedding.Of[float32]]
		err    error
	}{
		{"provider error", nil, wantErr},
		{"nil batch", nil, nil},
		{"nil embedding", &embedding.Generated[*embedding.Of[float32]]{Embeddings: []*embedding.Of[float32]{nil}}, nil},
		{"nil vector", &embedding.Generated[*embedding.Of[float32]]{Embeddings: []*embedding.Of[float32]{{}}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			generator := embedding.Bind(generatorFunc(func(context.Context, []string, *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
				if test.err != nil {
					cancel()
				}
				return test.result, test.err
			}))
			calls := 0
			store := newStore(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(http.StatusOK, `{"value":[]}`), nil
			}, azureprovider.AISearchConfig{EmbeddingGenerator: generator})
			collection := openCollection[string, document](t, store, nil)
			err := sequenceError(t, collection.Search(ctx, "query", 1, nil))
			if calls != 0 || test.err != nil && (!errors.Is(err, wantErr) || errors.Is(err, context.Canceled)) {
				t.Fatalf("query embedding error = %v, HTTP calls = %d", err, calls)
			}
		})
	}
}
