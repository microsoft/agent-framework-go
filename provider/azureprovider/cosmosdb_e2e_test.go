// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/vectordata"
)

// Live tests require RUN_AZURE_COSMOS_INTEGRATION_TESTS=true,
// AZURE_COSMOS_ENDPOINT, AZURE_COSMOS_DATABASE, and an Azure CLI login.
// AZURE_SUBSCRIPTION_ID selects the CLI subscription.
// The account must support vector and full-text search, and the database must
// already exist. The principal needs Cosmos DB data contributor access.
// Only UUID-named test containers are created and deleted.
//
// Generated-vector tests also require FOUNDRY_PROJECT_ENDPOINT and access to
// FOUNDRY_EMBEDDING_MODEL, which defaults to text-embedding-3-large (3072 dimensions).
func newLiveCosmosDB(t *testing.T, config azureprovider.CosmosDBConfig, generateEmbeddings bool) *azureprovider.CosmosDB {
	t.Helper()
	if !strings.EqualFold(os.Getenv("RUN_AZURE_COSMOS_INTEGRATION_TESTS"), "true") {
		t.Skip("RUN_AZURE_COSMOS_INTEGRATION_TESTS is not true")
	}
	endpoint := strings.TrimSpace(os.Getenv("AZURE_COSMOS_ENDPOINT"))
	database := strings.TrimSpace(os.Getenv("AZURE_COSMOS_DATABASE"))
	if endpoint == "" || database == "" {
		t.Fatal("AZURE_COSMOS_ENDPOINT and AZURE_COSMOS_DATABASE are required")
	}
	credential, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{
		Subscription: os.Getenv("AZURE_SUBSCRIPTION_ID"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if generateEmbeddings {
		projectEndpoint := strings.TrimSpace(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"))
		if projectEndpoint == "" {
			t.Skip("FOUNDRY_PROJECT_ENDPOINT is required for generated-vector tests")
		}
		config.EmbeddingGenerator = embedding.Bind(foundryprovider.NewEmbeddingGenerator(projectEndpoint, credential,
			foundryprovider.EmbeddingGeneratorConfig{
				Model: cmp.Or(os.Getenv("FOUNDRY_EMBEDDING_MODEL"), "text-embedding-3-large"),
			}))
	}
	client, err := azcosmos.NewClient(endpoint, credential, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	store, err := azureprovider.NewCosmosDB(client, database, config)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func liveCosmosContainer(t *testing.T, store *azureprovider.CosmosDB) string {
	t.Helper()
	name := "afgo-live-" + uuid.NewString()
	t.Logf("Temporary Cosmos DB container: %s", name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := store.EnsureCollectionDeleted(ctx, name); err != nil {
			t.Errorf("delete temporary container %s: %v", name, err)
		}
	})
	return name
}

type liveCosmosDocument struct {
	ID     uuid.UUID `vectordata:"key"`
	Text   string    `vectordata:"data,fulltext,storage=text"`
	Bucket string    `vectordata:"data,indexed,storage=bucket"`
	Rank   int32     `vectordata:"data,indexed,storage=rank"`
	When   time.Time `vectordata:"data,storage=when"`
	Vector []float32 `vectordata:"vector,dimensions=3,index=flat,storage=vector"`
	Bytes  []uint8   `vectordata:"vector,dimensions=3,index=quantized_flat,storage=bytes"`
	Signed []int8    `vectordata:"vector,dimensions=3,index=disk_ann,storage=signed"`
}

func TestE2E_CosmosDBNativeCRUDAndSearch(t *testing.T) {
	store := newLiveCosmosDB(t, azureprovider.CosmosDBConfig{PartitionKeyProperties: []string{"Bucket"}}, false)
	name := liveCosmosContainer(t, store)
	collection, err := vectordata.GetCollection[azureprovider.CosmosKey, *liveCosmosDocument](store.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := collection.Exists(t.Context()); err != nil || exists {
		t.Fatalf("absent container exists = (%v, %v)", exists, err)
	}
	for range 2 {
		if err := collection.EnsureExists(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if exists, err := collection.Exists(t.Context()); err != nil || !exists {
		t.Fatalf("created container exists = (%v, %v)", exists, err)
	}
	if !slices.Contains(collect(t, store.ListCollectionNames(t.Context())), name) {
		t.Fatalf("created container %s was not listed", name)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	records := []*liveCosmosDocument{
		{Text: "A pirate joke about a ship.", Bucket: "alpha", Rank: 1, When: now, Vector: []float32{1, 0, 0}, Bytes: []uint8{255, 0, 0}, Signed: []int8{127, 0, 0}},
		{Text: "A pirate joke about a parrot.", Bucket: "alpha", Rank: 2, When: now, Vector: []float32{0.8, 0.2, 0}, Bytes: []uint8{200, 50, 0}, Signed: []int8{100, 25, 0}},
		{Text: "A private joke from another bucket.", Bucket: "beta", Rank: 3, When: now, Vector: []float32{1, 0, 0}, Bytes: []uint8{255, 0, 0}, Signed: []int8{127, 0, 0}},
	}
	if err := collection.Upsert(t.Context(), records...); err != nil {
		t.Fatal(err)
	}
	key := func(record *liveCosmosDocument) azureprovider.CosmosKey {
		return azureprovider.CosmosKey{DocumentID: record.ID.String(), PartitionKey: []any{record.Bucket}}
	}
	if records[0].ID == uuid.Nil || records[0].ID == records[1].ID {
		t.Fatalf("generated record IDs = %s, %s", records[0].ID, records[1].ID)
	}
	got, found, err := collection.Get(t.Context(), key(records[0]), &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || !reflect.DeepEqual(got, records[0]) {
		t.Fatalf("native retrieval = (%+v, %v, %v)", got, found, err)
	}
	wrongPartition := azureprovider.CosmosKey{DocumentID: records[0].ID.String(), PartitionKey: []any{"beta"}}
	if _, found, err := collection.Get(t.Context(), wrongPartition, nil); err != nil || found {
		t.Fatalf("wrong-partition retrieval = (%v, %v)", found, err)
	}
	batch := collect(t, vectordata.GetBatch(t.Context(), collection, []azureprovider.CosmosKey{
		key(records[0]), wrongPartition, key(records[1]),
	}, nil))
	if len(batch) != 2 || batch[0].ID != records[0].ID || batch[1].ID != records[1].ID ||
		batch[0].Vector != nil || batch[0].Bytes != nil || batch[0].Signed != nil {
		t.Fatalf("batch retrieval = %+v", batch)
	}
	records[0].Text = "An updated pirate joke about a ship."
	if err := collection.Upsert(t.Context(), records[0]); err != nil {
		t.Fatal(err)
	}
	got, found, err = collection.Get(t.Context(), key(records[0]), nil)
	if err != nil || !found || got.Text != records[0].Text {
		t.Fatalf("updated retrieval = (%+v, %v, %v)", got, found, err)
	}
	filter := vectordata.Filter{Operator: vectordata.Equal, Property: "Bucket", Value: "alpha"}
	for _, vector := range []struct {
		property string
		value    any
	}{
		{"Vector", []float32{1, 0, 0}},
		{"Bytes", []uint8{255, 0, 0}},
		{"Signed", []int8{127, 0, 0}},
	} {
		t.Run(vector.property, func(t *testing.T) {
			results := collect(t, collection.Search(t.Context(), vector.value, 3,
				&vectordata.SearchOptions{Filter: &filter, VectorProperty: vector.property, IncludeVectors: true}))
			if len(results) != 2 || results[0].Record.ID != records[0].ID || results[0].Score == nil ||
				!reflect.DeepEqual(results[0].Record, records[0]) {
				t.Fatalf("scoped %s search = %+v", vector.property, results)
			}
			if score := *results[0].Score; math.IsNaN(score) || math.IsInf(score, 0) {
				t.Fatalf("invalid search score %v", score)
			}
			thresholded := collect(t, collection.Search(t.Context(), vector.value, 3,
				&vectordata.SearchOptions{Filter: &filter, VectorProperty: vector.property, ScoreThreshold: new(0.99)}))
			if len(thresholded) != 1 || thresholded[0].Record.ID != records[0].ID ||
				thresholded[0].Score == nil || *thresholded[0].Score < 0.99 {
				t.Fatalf("score-threshold search = %+v", thresholded)
			}
		})
	}
	results := collect(t, collection.Search(t.Context(), []float32{1, 0, 0}, 1,
		&vectordata.SearchOptions{Filter: &filter, VectorProperty: "Vector", Skip: 1}))
	if len(results) != 1 || results[0].Record.ID != records[1].ID || results[0].Record.Vector != nil {
		t.Fatalf("vector Skip/top = %+v", results)
	}
	hybrid := collection.(vectordata.KeywordHybridSearcher[*liveCosmosDocument])
	hybridResults := collect(t, hybrid.HybridSearch(t.Context(), []float32{1, 0, 0}, []string{"pirate", "ship"}, 2,
		&vectordata.HybridSearchOptions{Filter: &filter, VectorProperty: "Vector", IncludeVectors: true}))
	if len(hybridResults) != 2 || hybridResults[0].Record.ID != records[0].ID || hybridResults[0].Score == nil ||
		!reflect.DeepEqual(hybridResults[0].Record.Vector, records[0].Vector) {
		t.Fatalf("hybrid search = %+v", hybridResults)
	}
	ordered := collect(t, collection.GetFiltered(t.Context(), filter, 1,
		&vectordata.FilteredRecordRetrievalOptions{Skip: 1, OrderBy: []vectordata.Sort{{Property: "Rank", Descending: true}}}))
	if len(ordered) != 1 || ordered[0].ID != records[0].ID {
		t.Fatalf("ordered Skip/top = %+v", ordered)
	}
	membership := collect(t, collection.GetFiltered(t.Context(),
		vectordata.Filter{Operator: vectordata.In, Property: "ID", Value: []azureprovider.CosmosKey{key(records[0]), key(records[2])}}, 3, nil))
	if len(membership) != 2 {
		t.Fatalf("composite-key membership = %+v", membership)
	}
	if err := vectordata.DeleteBatch(t.Context(), collection, []azureprovider.CosmosKey{key(records[0]), key(records[1])}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := collection.Get(t.Context(), key(records[0]), nil); err != nil || found {
		t.Fatalf("deleted document retrieval = (%v, %v)", found, err)
	}
	if err := collection.Delete(t.Context(), key(records[0])); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
	for range 2 {
		if err := collection.EnsureDeleted(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if exists, err := collection.Exists(t.Context()); err != nil || exists {
		t.Fatalf("deleted container exists = (%v, %v)", exists, err)
	}
}

func TestE2E_CosmosDBHierarchicalNullablePartitions(t *testing.T) {
	type record struct {
		ID     string   `vectordata:"key"`
		Tenant *float32 `vectordata:"data,indexed,storage=tenant"`
		Region *string  `vectordata:"data,indexed,storage=region"`
		Active *bool    `vectordata:"data,indexed,storage=active"`
	}
	store := newLiveCosmosDB(t, azureprovider.CosmosDBConfig{PartitionKeyProperties: []string{"Tenant", "Region", "Active"}}, false)
	name := liveCosmosContainer(t, store)
	collection, err := vectordata.GetCollection[azureprovider.CosmosKey, *record](store.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	records := []*record{
		{ID: "numeric", Tenant: new(float32(0.1)), Region: new("north"), Active: new(true)},
		{ID: "null"},
	}
	if err := collection.Upsert(t.Context(), records...); err != nil {
		t.Fatal(err)
	}
	dynamic, err := store.DynamicCollection(name, &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
		&vectordata.DataProperty{Name: "Tenant", StorageName: "tenant", Type: reflect.TypeFor[*float32](), IsIndexed: true},
		&vectordata.DataProperty{Name: "Region", StorageName: "region", Type: reflect.TypeFor[*string](), IsIndexed: true},
		&vectordata.DataProperty{Name: "Active", StorageName: "active", Type: reflect.TypeFor[*bool](), IsIndexed: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range records {
		t.Run(want.ID, func(t *testing.T) {
			key := azureprovider.CosmosKey{DocumentID: want.ID, PartitionKey: []any{want.Tenant, want.Region, want.Active}}
			got, found, err := collection.Get(t.Context(), key, nil)
			if err != nil || !found || !reflect.DeepEqual(got, want) {
				t.Fatalf("hierarchical retrieval = (%+v, %v, %v), want %+v", got, found, err, want)
			}
			mapped, found, err := dynamic.Get(t.Context(), key, nil)
			var wantTenant any
			if want.Tenant != nil {
				wantTenant = want.Tenant
			}
			if err != nil || !found || mapped["ID"] != want.ID || !reflect.DeepEqual(mapped["Tenant"], wantTenant) {
				t.Fatalf("dynamic retrieval = (%#v, %v, %v)", mapped, found, err)
			}
			if err := dynamic.Upsert(t.Context(), mapped); err != nil {
				t.Fatal(err)
			}
			filter := vectordata.Filter{Operator: vectordata.And, Operands: []vectordata.Filter{
				{Operator: vectordata.Equal, Property: "Tenant", Value: want.Tenant},
				{Operator: vectordata.Equal, Property: "Region", Value: want.Region},
				{Operator: vectordata.Equal, Property: "Active", Value: want.Active},
			}}
			matches := collect(t, collection.GetFiltered(t.Context(), filter, 2,
				&vectordata.FilteredRecordRetrievalOptions{OrderBy: []vectordata.Sort{{Property: "ID"}}}))
			if len(matches) != 1 || matches[0].ID != want.ID {
				t.Fatalf("hierarchical filtered retrieval = %+v", matches)
			}
			if err := dynamic.Delete(t.Context(), key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestE2E_CosmosDBCrossPartitionPagingAndNamedKeys(t *testing.T) {
	type key string
	type record struct {
		ID     key       `vectordata:"key"`
		Bucket string    `vectordata:"data,indexed,storage=bucket"`
		Vector []float32 `vectordata:"vector,dimensions=3,index=flat,storage=vector"`
	}
	store := newLiveCosmosDB(t, azureprovider.CosmosDBConfig{}, false)
	name := liveCosmosContainer(t, store)
	collection, err := vectordata.GetCollection[key, *record](store.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	const count = 125
	records := make([]*record, count)
	for i := range records {
		records[i] = &record{ID: key(fmt.Sprintf("item-%03d", i)), Bucket: "alpha"}
	}
	if err := collection.Upsert(t.Context(), records...); err != nil {
		t.Fatal(err)
	}
	got, found, err := collection.Get(t.Context(), records[0].ID, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || got.ID != records[0].ID || got.Vector != nil {
		t.Fatalf("named key and nil vector retrieval = (%+v, %v, %v)", got, found, err)
	}
	filter := vectordata.Filter{Operator: vectordata.Equal, Property: "Bucket", Value: "alpha"}
	matches := collect(t, collection.GetFiltered(t.Context(), filter, count+1, nil))
	seen := make(map[key]bool, count)
	for _, match := range matches {
		if match.Bucket != "alpha" || seen[match.ID] {
			t.Fatalf("unexpected or duplicate result: %+v", match)
		}
		seen[match.ID] = true
	}
	if len(seen) != count {
		t.Fatalf("cross-partition retrieval returned %d distinct records, want %d", len(seen), count)
	}
	for _, record := range records {
		if !seen[record.ID] {
			t.Fatalf("cross-partition retrieval omitted %s", record.ID)
		}
	}
	page := collect(t, collection.GetFiltered(t.Context(), filter, 10, &vectordata.FilteredRecordRetrievalOptions{Skip: 110}))
	if len(page) != 10 {
		t.Fatalf("cross-partition Skip/top returned %d records, want 10", len(page))
	}
	exhausted := collect(t, collection.GetFiltered(t.Context(), filter, 1, &vectordata.FilteredRecordRetrievalOptions{Skip: count}))
	if len(exhausted) != 0 {
		t.Fatalf("Skip past final record = %+v", exhausted)
	}
	yielded := 0
	for _, err := range collection.GetFiltered(t.Context(), filter, count, nil) {
		if err != nil {
			t.Fatal(err)
		}
		yielded++
		break
	}
	if yielded != 1 {
		t.Fatalf("early-stopped retrieval yielded %d records", yielded)
	}
	ctx, cancel := context.WithCancel(t.Context())
	yielded = 0
	var canceled bool
	for _, err := range collection.GetFiltered(ctx, filter, count, nil) {
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			canceled = true
			break
		}
		yielded++
		cancel()
	}
	cancel()
	if !canceled || yielded != 1 {
		t.Fatalf("canceled retrieval yielded %d records, cancellation error=%v", yielded, canceled)
	}
	var rejected bool
	for _, err := range collection.Search(t.Context(), []float32{1, 0, 0}, 1, nil) {
		if err == nil || !strings.Contains(err.Error(), "partition") {
			t.Fatalf("unscoped search error = %v", err)
		}
		rejected = true
	}
	if !rejected {
		t.Fatal("unscoped ranking was not rejected")
	}
}

func TestE2E_CosmosDBGeneratedVectors(t *testing.T) {
	type record struct {
		ID      uuid.UUID `vectordata:"key"`
		Bucket  string    `vectordata:"data,indexed,storage=bucket"`
		Text    string    `vectordata:"data,fulltext,storage=text"`
		Content string    `vectordata:"vector,dimensions=3072,storage=vector"`
	}
	store := newLiveCosmosDB(t, azureprovider.CosmosDBConfig{PartitionKeyProperties: []string{"Bucket"}}, true)
	name := liveCosmosContainer(t, store)
	collection, err := vectordata.GetCollection[azureprovider.CosmosKey, *record](store.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	records := []*record{
		{Bucket: "alpha", Text: "I enjoy pirate jokes about parrots and ships.", Content: "I enjoy pirate jokes about parrots and ships."},
		{Bucket: "alpha", Text: "I like hiking in the mountains.", Content: "I like hiking in the mountains."},
		{Bucket: "beta", Text: "I enjoy pirate jokes.", Content: "I enjoy pirate jokes."},
	}
	if err := collection.Upsert(t.Context(), records...); err != nil {
		t.Fatal(err)
	}
	filter := vectordata.Filter{Operator: vectordata.Equal, Property: "Bucket", Value: "alpha"}
	results := collect(t, collection.Search(t.Context(), "Tell me a pirate joke.", 2, &vectordata.SearchOptions{Filter: &filter}))
	if len(results) != 2 || results[0].Record.ID != records[0].ID || results[0].Record.Content != "" || results[0].Score == nil {
		t.Fatalf("generated-vector search = %+v", results)
	}
	hybrid := collection.(vectordata.KeywordHybridSearcher[*record])
	hybridResults := collect(t, hybrid.HybridSearch(t.Context(), "pirate jokes", []string{"pirate"}, 2,
		&vectordata.HybridSearchOptions{Filter: &filter}))
	if len(hybridResults) != 2 || hybridResults[0].Record.ID != records[0].ID {
		t.Fatalf("generated hybrid search = %+v", hybridResults)
	}
	type nativeView struct {
		ID      uuid.UUID `vectordata:"key"`
		Bucket  string    `vectordata:"data,indexed,storage=bucket"`
		Content []float32 `vectordata:"vector,dimensions=3072,storage=vector"`
	}
	native, err := vectordata.GetCollection[azureprovider.CosmosKey, nativeView](store.Store, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := azureprovider.CosmosKey{DocumentID: records[0].ID.String(), PartitionKey: []any{"alpha"}}
	value, found, err := native.Get(t.Context(), key, &vectordata.RecordRetrievalOptions{IncludeVectors: true})
	if err != nil || !found || len(value.Content) != 3072 {
		t.Fatalf("stored embedding dimensions = %d, found=%v, error=%v", len(value.Content), found, err)
	}
	if _, _, err := collection.Get(t.Context(), key, &vectordata.RecordRetrievalOptions{IncludeVectors: true}); err == nil {
		t.Fatal("generated-vector IncludeVectors unexpectedly succeeded")
	}
	if records[0].Content != records[0].Text {
		t.Fatal("embedding generation changed the source record")
	}
}
