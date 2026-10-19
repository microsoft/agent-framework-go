// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"bytes"
	"cmp"
	"context"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

func newLiveSearch(t *testing.T, generateEmbeddings bool) *azureprovider.AISearch {
	t.Helper()
	if !strings.EqualFold(os.Getenv("RUN_AZURE_AI_SEARCH_INTEGRATION_TESTS"), "true") {
		t.Skip("RUN_AZURE_AI_SEARCH_INTEGRATION_TESTS is not true")
	}
	endpoint := strings.TrimSpace(os.Getenv("AZURE_AI_SEARCH_ENDPOINT"))
	if endpoint == "" {
		t.Fatal("AZURE_AI_SEARCH_ENDPOINT is required")
	}
	credential, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{
		Subscription: os.Getenv("AZURE_SUBSCRIPTION_ID"),
	})
	if err != nil {
		t.Fatal(err)
	}
	config := azureprovider.AISearchConfig{Credential: credential}
	if key := os.Getenv("AZURE_AI_SEARCH_API_KEY"); key != "" {
		config.Credential = nil
		config.APIKey = azcore.NewKeyCredential(key)
	}
	if generateEmbeddings {
		projectEndpoint := strings.TrimSpace(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"))
		if projectEndpoint == "" {
			t.Skip("FOUNDRY_PROJECT_ENDPOINT is required for generated-vector and memory tests")
		}
		config.EmbeddingGenerator = embedding.Bind(foundryprovider.NewEmbeddingGenerator(projectEndpoint, credential,
			foundryprovider.EmbeddingGeneratorConfig{
				Model: cmp.Or(os.Getenv("FOUNDRY_EMBEDDING_MODEL"), "text-embedding-3-large"),
			}))
	}
	search, err := azureprovider.NewAISearch(endpoint, config)
	if err != nil {
		t.Fatal(err)
	}
	return search
}

func liveIndex(t *testing.T, search *azureprovider.AISearch) string {
	t.Helper()
	name := "afgo-live-" + uuid.NewString()
	t.Logf("Temporary Search index: %s", name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := search.EnsureCollectionDeleted(ctx, name); err != nil {
			t.Errorf("delete temporary index %s: %v", name, err)
		}
	})
	return name
}

func waitForLiveIndex(t *testing.T, ready func(context.Context) (bool, error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := ready(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for Search indexing: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

type liveDocument struct {
	ID     uuid.UUID `vectordata:"key,storage=id"`
	Text   string    `vectordata:"data,indexed,fulltext,storage=text"`
	Bucket string    `vectordata:"data,indexed,storage=bucket"`
	Rank   int32     `vectordata:"data,indexed,storage=rank"`
	When   time.Time `vectordata:"data,storage=when"`
	Vector []float32 `vectordata:"vector,dimensions=3,storage=vector"`
}

func TestE2E_AISearchNativeCRUD(t *testing.T) {
	search := newLiveSearch(t, false)
	name := liveIndex(t, search)
	collection, err := vectordata.GetCollection[uuid.UUID, *liveDocument](search.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := collection.EnsureExists(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	names := collect(t, search.ListCollectionNames(t.Context()))
	if !slices.Contains(names, name) {
		t.Fatalf("created index %s was not listed", name)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	records := []*liveDocument{
		{Text: "A pirate joke about a ship.", Bucket: "alpha", Rank: 1, When: now, Vector: []float32{1, 0, 0}},
		{Text: "A pirate joke about a parrot.", Bucket: "alpha", Rank: 2, When: now, Vector: []float32{0.8, 0.2, 0}},
		{Text: "A private joke from another bucket.", Bucket: "beta", Rank: 3, When: now, Vector: []float32{1, 0, 0}},
	}
	if err := collection.Upsert(t.Context(), records...); err != nil {
		t.Fatal(err)
	}
	filter := vectordata.Filter{Operator: vectordata.Equal, Property: "Bucket", Value: "alpha"}
	waitForLiveIndex(t, func(ctx context.Context) (bool, error) {
		count := 0
		for _, err := range collection.GetFiltered(ctx, filter, 3, nil) {
			if err != nil {
				return false, err
			}
			count++
		}
		return count == 2, nil
	})
	got, found, err := collection.Get(t.Context(), records[0].ID, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || !reflect.DeepEqual(got.Vector, records[0].Vector) || !got.When.Equal(now) {
		t.Fatalf("native retrieval = (%+v, %v, %v)", got, found, err)
	}
	batch := collect(t, vectordata.GetBatch(t.Context(), collection, []uuid.UUID{records[0].ID, uuid.New(), records[1].ID}, nil))
	if len(batch) != 2 || batch[0].Vector != nil || batch[0].ID != records[0].ID || batch[1].ID != records[1].ID {
		t.Fatalf("batch retrieval = %v", batch)
	}
	results := collect(t, collection.Search(t.Context(), []float32{1, 0, 0}, 3, &vectordata.SearchOptions{Filter: &filter}))
	if len(results) != 2 || results[0].Record.ID != records[0].ID || results[0].Score == nil {
		t.Fatalf("scoped vector results = %v", results)
	}
	ordered := collect(t, collection.GetFiltered(t.Context(), filter, 2,
		&vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "Rank", Descending: true}}}))
	if len(ordered) != 2 || ordered[0].Rank != 2 {
		t.Fatalf("filtered ordering = %v", ordered)
	}
	wantIDs := []string{records[0].ID.String(), records[1].ID.String()}
	slices.Sort(wantIDs)
	for _, descending := range []bool{false, true} {
		if descending {
			slices.Reverse(wantIDs)
		}
		orderedByKey := collect(t, collection.GetFiltered(t.Context(), filter, 2,
			&vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "ID", Descending: descending}}}))
		if len(orderedByKey) != len(wantIDs) {
			t.Fatalf("key ordering returned %d records, want %d", len(orderedByKey), len(wantIDs))
		}
		for i, record := range orderedByKey {
			if record.ID.String() != wantIDs[i] {
				t.Fatalf("key ordering descending=%v: record %d = %s, want %s", descending, i, record.ID, wantIDs[i])
			}
		}
	}
	hybrid := collection.(vectordata.KeywordHybridSearcher[*liveDocument])
	hybridResults := collect(t, hybrid.HybridSearch(t.Context(), []float32{1, 0, 0}, []string{"pirate", "ship"}, 2,
		&vectordata.HybridSearchOptions{Filter: &filter, IncludeVectors: true}))
	if len(hybridResults) != 2 || len(hybridResults[0].Record.Vector) != 3 {
		t.Fatalf("hybrid results = %v", hybridResults)
	}
	dynamic, err := search.DynamicCollection(name, &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", StorageName: "id", Type: reflect.TypeFor[uuid.UUID]()},
		&vectordata.DataProperty{Name: "Text", StorageName: "text", Type: reflect.TypeFor[string]()},
		&vectordata.VectorProperty{Name: "Vector", StorageName: "vector", Type: reflect.TypeFor[[]float32](), Dimensions: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mapped, found, err := dynamic.Get(t.Context(), records[0].ID, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || mapped["ID"] != records[0].ID || !reflect.DeepEqual(mapped["Vector"], records[0].Vector) {
		t.Fatalf("dynamic retrieval = (%v, %v, %v)", mapped, found, err)
	}
	if err := vectordata.DeleteBatch(t.Context(), collection, []uuid.UUID{records[0].ID, records[1].ID}); err != nil {
		t.Fatal(err)
	}
	waitForLiveIndex(t, func(ctx context.Context) (bool, error) {
		for _, err := range collection.GetFiltered(ctx, filter, 1, nil) {
			return false, err
		}
		return true, nil
	})
	if err := collection.EnsureDeleted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if exists, err := collection.Exists(t.Context()); err != nil || exists {
		t.Fatalf("deleted index exists = (%v, %v)", exists, err)
	}
}

func TestE2E_AISearchEmptyCollectionsAndMembership(t *testing.T) {
	type record struct {
		ID     string    `vectordata:"key,storage=id"`
		Text   *string   `vectordata:"data,indexed,storage=text"`
		Tags   []string  `vectordata:"data,storage=tags"`
		Vector []float32 `vectordata:"vector,dimensions=3,storage=vector"`
	}
	search := newLiveSearch(t, false)
	collection, err := vectordata.GetCollection[string, *record](search.Store, liveIndex(t, search), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	records := []*record{
		{ID: "empty", Text: new("")},
		{ID: "word", Text: new("word"), Tags: []string{}, Vector: []float32{}},
		{ID: "null", Tags: []string{"tag"}, Vector: []float32{1, 0, 0}},
	}
	if err := collection.Upsert(t.Context(), records...); err != nil {
		t.Fatal(err)
	}
	waitForLiveIndex(t, func(ctx context.Context) (bool, error) {
		count := 0
		for _, err := range collection.GetFiltered(ctx,
			vectordata.Filter{Operator: vectordata.In, Property: "ID", Value: []string{"empty", "word", "null"}}, 3, nil) {
			if err != nil {
				return false, err
			}
			count++
		}
		return count == len(records), nil
	})
	got, found, err := collection.Get(t.Context(), "empty", &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || got.Tags == nil || len(got.Tags) != 0 || got.Vector == nil || len(got.Vector) != 0 {
		t.Fatalf("empty collections = (%+v, %v, %v)", got, found, err)
	}
	if records[0].Tags != nil || records[0].Vector != nil {
		t.Fatalf("upsert changed nil input fields: %+v", records[0])
	}
	for _, test := range []struct {
		name   string
		filter vectordata.Filter
		want   []string
	}{
		{"empty set", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{}}, nil},
		{"empty string", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{""}}, []string{"empty"}},
		{"empty and nonempty strings", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{"", "word"}}, []string{"empty", "word"}},
		{"nonempty string", vectordata.Filter{Operator: vectordata.In, Property: "Text", Value: []string{"word"}}, []string{"word"}},
		{"negated membership", vectordata.Filter{Operator: vectordata.Not, Operands: []vectordata.Filter{
			{Operator: vectordata.In, Property: "Text", Value: []string{"", "word"}},
		}}, []string{"null"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ids []string
			for record, err := range collection.GetFiltered(t.Context(), test.filter, 3, nil) {
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, record.ID)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, test.want) {
				t.Fatalf("matched IDs = %v, want %v", ids, test.want)
			}
		})
	}
}

type liveGeneratedDocument struct {
	ID      uuid.UUID `vectordata:"key"`
	Text    string    `vectordata:"data,fulltext"`
	User    string    `vectordata:"data,indexed"`
	Content string    `vectordata:"vector,dimensions=3072"`
}

func TestE2E_AISearchGeneratedVectors(t *testing.T) {
	search := newLiveSearch(t, true)
	name := liveIndex(t, search)
	collection, err := vectordata.GetCollection[uuid.UUID, *liveGeneratedDocument](search.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := &liveGeneratedDocument{Text: "I enjoy pirate jokes.", User: "alpha", Content: "I enjoy pirate jokes."}
	second := &liveGeneratedDocument{Text: "I enjoy hiking in the mountains.", User: "beta", Content: "I enjoy hiking in the mountains."}
	if err := collection.Upsert(t.Context(), first, second); err != nil {
		t.Fatal(err)
	}
	filter := vectordata.Filter{Operator: vectordata.Equal, Property: "User", Value: "alpha"}
	waitForLiveIndex(t, func(ctx context.Context) (bool, error) {
		for _, err := range collection.GetFiltered(ctx, filter, 1, nil) {
			return err == nil, err
		}
		return false, nil
	})
	results := collect(t, collection.Search(t.Context(), "Tell me a pirate joke.", 2, &vectordata.SearchOptions{Filter: &filter}))
	if len(results) != 1 || results[0].Record.ID != first.ID || results[0].Record.Content != "" {
		t.Fatalf("generated-vector search = %v", results)
	}
	hybrid := collection.(vectordata.KeywordHybridSearcher[*liveGeneratedDocument])
	hybridResults := collect(t, hybrid.HybridSearch(t.Context(), "pirate jokes", []string{"pirate"}, 2,
		&vectordata.HybridSearchOptions{Filter: &filter}))
	if len(hybridResults) != 1 || hybridResults[0].Record.ID != first.ID {
		t.Fatalf("generated hybrid search = %v", hybridResults)
	}
	type nativeView struct {
		ID      uuid.UUID `vectordata:"key"`
		Content []float32 `vectordata:"vector,dimensions=3072"`
	}
	native, err := vectordata.GetCollection[uuid.UUID, nativeView](search.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, found, err := native.Get(t.Context(), first.ID, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || len(value.Content) != 3072 {
		t.Fatalf("stored embedding dimensions = %d, found=%v, error=%v", len(value.Content), found, err)
	}
}

func TestE2E_AISearchChatHistoryMemory(t *testing.T) {
	search := newLiveSearch(t, true)
	name := liveIndex(t, search)
	var diagnostics bytes.Buffer
	provider, err := memory.NewHistoryProvider(search.Store, name, 3072, func(*agent.Session) *memory.State {
		return &memory.State{
			StorageScope: memory.Scope{UserID: new("alpha"), SessionID: new(uuid.NewString())},
			SearchScope:  &memory.Scope{UserID: new("alpha")},
		}
	}, memory.Config{Logger: slog.New(slog.NewJSONHandler(&diagnostics, nil))})
	if err != nil {
		t.Fatal(err)
	}
	first := agenttest.CreateSession()
	if err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages:  []*message.Message{message.NewText("I like pirate jokes.")},
		ResponseMessages: []*message.Message{message.NewText("Here is a pirate joke.")},
		Options:          []agent.Option{agent.WithSession(first)},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diagnostics.String(), `"level":"ERROR"`) {
		t.Fatalf("memory storage diagnostics: %s", diagnostics.String())
	}
	type storedMemory struct {
		Key     uuid.UUID `vectordata:"key"`
		UserID  string    `vectordata:"data,indexed,storage=UserId"`
		Content string    `vectordata:"data,fulltext"`
	}
	view, err := vectordata.GetCollection[uuid.UUID, storedMemory](search.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForLiveIndex(t, func(ctx context.Context) (bool, error) {
		count := 0
		for _, err := range view.GetFiltered(ctx, vectordata.Filter{Operator: vectordata.Equal, Property: "UserID", Value: "alpha"}, 3, nil) {
			if err != nil {
				return false, err
			}
			count++
		}
		return count == 2, nil
	})
	reader := agenttest.CreateSession()
	messages, _, err := provider.Invoking(t.Context(), agent.InvokingContext{
		Messages: []*message.Message{message.NewText("What kind of jokes do I like?")},
		Options:  []agent.Option{agent.WithSession(reader)},
	})
	if err != nil || len(messages) != 2 || !strings.Contains(messages[1].String(), "I like pirate jokes.") ||
		strings.Contains(diagnostics.String(), `"level":"ERROR"`) {
		t.Fatalf("cross-session memory = (%v, %v); diagnostics: %s", messages, err, diagnostics.String())
	}
}
