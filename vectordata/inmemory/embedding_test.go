// Copyright (c) Microsoft. All rights reserved.

package inmemory_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
)

type generatorFunc[I, E any] func(context.Context, []I, *embedding.Options) (*embedding.Generated[E], error)

func (f generatorFunc[I, E]) Generate(ctx context.Context, values []I, options *embedding.Options) (*embedding.Generated[E], error) {
	return f(ctx, values, options)
}

type embeddedDocument struct {
	ID   string `vectordata:"key"`
	Text string `vectordata:"vector,dimensions=2"`
}

func textGenerator() generatorFunc[string, *embedding.Of[float32]] {
	return func(_ context.Context, inputs []string, _ *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
		result := &embedding.Generated[*embedding.Of[float32]]{}
		for _, input := range inputs {
			vector := embedding.VectorOf[float32]{1, 0}
			if input == "north" {
				vector = embedding.VectorOf[float32]{0, 1}
			}
			result.Embeddings = append(result.Embeddings, &embedding.Of[float32]{Vector: vector})
		}
		return result, nil
	}
}

func TestEmbedding_BatchedPropertiesAndQueries(t *testing.T) {
	type record struct {
		ID      string    `vectordata:"key"`
		Summary string    `vectordata:"vector,dimensions=2"`
		Details string    `vectordata:"vector,dimensions=2"`
		Native  []float32 `vectordata:"vector,dimensions=2"`
	}
	ctx := t.Context()
	var calls [][]string
	var generated []*embedding.Of[float32]
	generator := generatorFunc[string, *embedding.Of[float32]](func(got context.Context, values []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
		if got != ctx || options != nil {
			t.Fatal("generator context or default options changed")
		}
		calls = append(calls, slices.Clone(values))
		result, err := textGenerator().Generate(got, values, options)
		generated = append(generated, result.Embeddings...)
		return result, err
	})
	c := createCollection[string, record](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
	input := []record{
		{ID: "one", Summary: "east", Details: "north", Native: []float32{1, 0}},
		{ID: "two", Summary: "north", Details: "east", Native: []float32{0, 1}},
	}
	if err := c.Upsert(ctx, input...); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"east", "north"}, {"north", "east"}}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("generator calls = %v, want %v", calls, want)
	}
	for _, result := range generated {
		result.Vector[0], result.Vector[1] = 100, 100
	}
	if err := c.Upsert(ctx); err != nil || len(calls) != 2 {
		t.Fatalf("empty upsert generated embeddings: %v, %v", calls, err)
	}
	options := &vectordata.SearchOptions{VectorProperty: "Summary"}
	sequence := c.Search(ctx, "east", 2, options)
	if len(calls) != 2 {
		t.Fatal("search generated embeddings eagerly")
	}
	results := collect(t, sequence)
	if len(results) != 2 || results[0].Record.ID != "one" || *results[0].Score != 1 ||
		results[0].Record.Summary != "east" || !slices.Equal(results[0].Record.Native, input[0].Native) {
		t.Fatalf("generated search results = %+v", results)
	}
	if len(calls) != 3 || !slices.Equal(calls[2], []string{"east"}) {
		t.Fatalf("query embedding calls = %v", calls)
	}
	collect(t, c.Search(ctx, []float32{1, 0}, 2, options))
	if len(calls) != 3 {
		t.Fatal("native query used the generator")
	}
	_ = sequenceError(t, c.Search(ctx, 42, 2, options))
	if len(calls) != 3 {
		t.Fatal("incompatible query reached the generator")
	}
	got, found, err := c.Get(ctx, "one", nil)
	if err != nil || !found || got.Summary != "east" || got.Details != "north" || !slices.Equal(got.Native, input[0].Native) {
		t.Fatalf("generated record Get = %+v, %v, %v", got, found, err)
	}
	filtered := collect(t, c.GetFiltered(ctx, vectordata.Filter{Operator: vectordata.Equal, Property: "Summary", Value: "east"}, 2, nil))
	if len(filtered) != 1 || filtered[0].ID != "one" {
		t.Fatalf("filter did not use original generation input: %v", filtered)
	}
	if _, _, err := c.Get(ctx, "one", &vectordata.RecordRetrievalOptions{IncludeVectors: true}); err == nil {
		t.Fatal("IncludeVectors accepted for generated records")
	}
	_ = sequenceError(t, c.Search(ctx, "east", 2, &vectordata.SearchOptions{VectorProperty: "Summary", IncludeVectors: true}))
	_ = sequenceError(t, c.GetFiltered(ctx, allRecords, 2, &vectordata.FilteredRecordRetrievalOptions{IncludeVectors: true}))
	if len(calls) != 3 || input[0].Summary != "east" || input[0].Native[0] != 1 || options.IncludeVectors {
		t.Fatal("rejected operations generated embeddings or mutated inputs")
	}
}

func TestEmbedding_GeneratorPrecedence(t *testing.T) {
	for _, test := range []struct {
		name     string
		level    int
		explicit bool
	}{
		{"store inferred", 0, false},
		{"store explicit", 0, true},
		{"definition", 1, true},
		{"property", 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []int
			generators := make([]*embedding.BoundGenerator, 3)
			for i := range generators {
				generators[i] = embedding.Bind(generatorFunc[string, *embedding.Of[float32]](func(ctx context.Context, values []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
					calls = append(calls, i)
					return textGenerator().Generate(ctx, values, options)
				}))
			}
			store := inmemory.New(inmemory.Config{EmbeddingGenerator: generators[0]})
			var definition *vectordata.Definition
			if test.explicit {
				definition = &vectordata.Definition{}
			}
			var wantGenerator *embedding.BoundGenerator
			if test.level >= 1 {
				wantGenerator = generators[1]
				definition.EmbeddingGenerator = wantGenerator
			}
			if test.level >= 2 {
				definition.Properties = []vectordata.Property{
					&vectordata.VectorProperty{Name: "Text", Dimensions: 2, EmbeddingGenerator: generators[2]},
				}
			}
			c, err := vectordata.GetCollection[string, embeddedDocument](store.Store, "records", definition)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.EnsureExists(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := c.Upsert(t.Context(), embeddedDocument{ID: "id", Text: "east"}); err != nil {
				t.Fatal(err)
			}
			collect(t, c.Search(t.Context(), "east", 1, nil))
			if !slices.Equal(calls, []int{test.level, test.level}) {
				t.Fatalf("generator selection = %v", calls)
			}
			if definition != nil && definition.EmbeddingGenerator != wantGenerator {
				t.Fatal("collection changed the definition's generator")
			}
			if test.level >= 2 {
				property := definition.Properties[0].(*vectordata.VectorProperty)
				if property.Type != nil || property.EmbeddingType != nil || property.StorageName != "" ||
					property.EmbeddingGenerator != generators[2] {
					t.Fatal("collection changed the definition's vector property")
				}
			}
		})
	}
}

func TestEmbedding_SharedDefinitionDefaults(t *testing.T) {
	property := &vectordata.VectorProperty{Name: "Text", Type: reflect.TypeFor[string](), Dimensions: 2}
	definition := &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.KeyProperty{Name: "ID", Type: reflect.TypeFor[string]()},
		property,
	}}
	var calls []int
	for i := range 2 {
		generator := embedding.Bind(generatorFunc[string, *embedding.Of[float32]](func(ctx context.Context, values []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
			calls = append(calls, i)
			return textGenerator().Generate(ctx, values, options)
		}))
		store := inmemory.New(inmemory.Config{EmbeddingGenerator: generator})
		collection, err := store.DynamicCollection("records", definition)
		if err != nil {
			t.Fatal(err)
		}
		if err := collection.EnsureExists(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := collection.Upsert(t.Context(), map[string]any{"ID": "one", "Text": "east"}); err != nil {
			t.Fatal(err)
		}
		results := collect(t, collection.Search(t.Context(), "east", 1, nil))
		if len(results) != 1 || results[0].Record["ID"] != "one" {
			t.Fatalf("dynamic search results = %v", results)
		}
		if definition.EmbeddingGenerator != nil || property.EmbeddingGenerator != nil ||
			property.EmbeddingType != nil || property.StorageName != "" {
			t.Fatal("store defaults modified the shared definition")
		}
	}
	if !slices.Equal(calls, []int{0, 0, 1, 1}) {
		t.Fatalf("generator defaults leaked between stores: %v", calls)
	}
	if _, err := inmemory.NewCollection[string, map[string]any]("standalone", definition); err == nil {
		t.Fatal("standalone collection inherited a store's generator")
	}
}

func TestEmbedding_DefinitionOverridesInvalidStoreGenerator(t *testing.T) {
	store := inmemory.New(inmemory.Config{EmbeddingGenerator: &embedding.BoundGenerator{}})
	if _, err := vectordata.GetCollection[string, embeddedDocument](store.Store, "invalid", nil); err == nil {
		t.Fatal("invalid store generator was accepted")
	}
	definition := &vectordata.Definition{EmbeddingGenerator: embedding.Bind(textGenerator())}
	collection, err := vectordata.GetCollection[string, embeddedDocument](store.Store, "records", definition)
	if err != nil {
		t.Fatalf("overridden store generator was selected: %v", err)
	}
	if err := collection.EnsureExists(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := collection.Upsert(t.Context(), embeddedDocument{ID: "one", Text: "east"}); err != nil {
		t.Fatal(err)
	}
	results := collect(t, collection.Search(t.Context(), "east", 1, nil))
	if len(results) != 1 || results[0].Record.ID != "one" {
		t.Fatalf("definition generator search results = %v", results)
	}
}

func TestEmbedding_InvalidOutputsAreAtomic(t *testing.T) {
	want := errors.New("generator failed")
	type output = embedding.Generated[*embedding.Of[float32]]
	valid := &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 0}}
	for name, response := range map[string]struct {
		result *output
		err    error
	}{
		"nil-batch": {},
		"empty":     {result: &output{}},
		"too-few":   {result: &output{Embeddings: []*embedding.Of[float32]{valid}}},
		"too-many":  {result: &output{Embeddings: []*embedding.Of[float32]{valid, valid, valid}}},
		"nil-item":  {result: &output{Embeddings: []*embedding.Of[float32]{valid, nil}}},
		"nil-vector": {result: &output{Embeddings: []*embedding.Of[float32]{
			valid, {},
		}}},
		"dimensions": {result: &output{Embeddings: []*embedding.Of[float32]{
			valid, {Vector: embedding.VectorOf[float32]{1}},
		}}},
		"provider-error": {result: &output{Embeddings: []*embedding.Of[float32]{valid, valid}}, err: want},
	} {
		t.Run(name, func(t *testing.T) {
			fail := false
			generator := generatorFunc[string, *embedding.Of[float32]](func(ctx context.Context, values []string, options *embedding.Options) (*output, error) {
				if fail {
					return response.result, response.err
				}
				return textGenerator().Generate(ctx, values, options)
			})
			c := createCollection[string, embeddedDocument](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
			if err := c.Upsert(t.Context(), embeddedDocument{ID: "existing", Text: "east"}); err != nil {
				t.Fatal(err)
			}
			fail = true
			err := c.Upsert(t.Context(), embeddedDocument{ID: "existing", Text: "north"}, embeddedDocument{ID: "new", Text: "north"})
			if err == nil || response.err != nil && !errors.Is(err, want) {
				t.Fatalf("malformed generation error = %v", err)
			}
			got := collect(t, c.GetFiltered(t.Context(), allRecords, 10, nil))
			if len(got) != 1 || got[0].Text != "east" {
				t.Fatalf("failed generation partially committed: %v", got)
			}
		})
	}
}

func TestEmbedding_SearchErrors(t *testing.T) {
	for name, response := range map[string]*embedding.Generated[*embedding.Of[float32]]{
		"nil": nil, "empty": {}, "nil-item": {Embeddings: []*embedding.Of[float32]{nil}},
		"dimensions": {Embeddings: []*embedding.Of[float32]{{Vector: embedding.VectorOf[float32]{1}}}},
	} {
		t.Run(name, func(t *testing.T) {
			generator := generatorFunc[string, *embedding.Of[float32]](func(context.Context, []string, *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
				return response, nil
			})
			c := createCollection[string, embeddedDocument](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
			_ = sequenceError(t, c.Search(t.Context(), "query", 1, nil))
		})
	}
	type record struct {
		ID    string  `vectordata:"key"`
		Input *string `vectordata:"vector,dimensions=2"`
	}
	called := false
	generator := generatorFunc[*string, []float32](func(context.Context, []*string, *embedding.Options) (*embedding.Generated[[]float32], error) {
		called = true
		return &embedding.Generated[[]float32]{Embeddings: [][]float32{{1, 0}}}, nil
	})
	c := createCollection[string, record](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
	_ = sequenceError(t, c.Search(t.Context(), (*string)(nil), 1, nil))
	if called {
		t.Fatal("nil query reached the generator")
	}
}

func TestEmbedding_CancellationAndOriginalErrors(t *testing.T) {
	t.Run("during-generation", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		generator := generatorFunc[string, *embedding.Of[float32]](func(ctx context.Context, inputs []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
			close(entered)
			<-release
			return textGenerator().Generate(ctx, inputs, options)
		})
		c := createCollection[string, embeddedDocument](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- c.Upsert(ctx, embeddedDocument{ID: "id", Text: "east"}) }()
		<-entered
		exists, err := c.Exists(t.Context())
		if err != nil || !exists {
			t.Errorf("store unavailable during generation: %v, %v", exists, err)
		}
		cancel()
		close(release)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Upsert error = %v", err)
		}
		if got := collect(t, c.GetFiltered(t.Context(), allRecords, 1, nil)); len(got) != 0 {
			t.Fatalf("canceled generation committed records: %v", got)
		}
	})
	for _, search := range []bool{false, true} {
		t.Run(map[bool]string{false: "upsert-error", true: "search-error"}[search], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := errors.New("original provider failure")
			generator := generatorFunc[string, *embedding.Of[float32]](func(context.Context, []string, *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
				cancel()
				return nil, want
			})
			c := createCollection[string, embeddedDocument](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
			var err error
			if search {
				err = sequenceError(t, c.Search(ctx, "query", 1, nil))
			} else {
				err = c.Upsert(ctx, embeddedDocument{ID: "id", Text: "east"})
			}
			if !errors.Is(err, want) || errors.Is(err, context.Canceled) {
				t.Fatalf("provider error was replaced: %v", err)
			}
		})
	}
}

func TestEmbedding_CollectionDeletionDuringGeneration(t *testing.T) {
	type record struct {
		ID   uuid.UUID `vectordata:"key"`
		Text string    `vectordata:"vector,dimensions=2"`
	}
	for _, recreate := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleted", true: "recreated"}[recreate], func(t *testing.T) {
			var collection *inmemory.Collection[uuid.UUID, *record]
			generator := generatorFunc[string, *embedding.Of[float32]](func(ctx context.Context, values []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
				if err := collection.EnsureDeleted(ctx); err != nil {
					return nil, err
				}
				if recreate {
					if err := collection.EnsureExists(ctx); err != nil {
						return nil, err
					}
				}
				return textGenerator().Generate(ctx, values, options)
			})
			collection = createCollection[uuid.UUID, *record](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
			input := &record{Text: "east"}
			err := collection.Upsert(t.Context(), input)
			if recreate {
				if err != nil {
					t.Fatal(err)
				}
				if got, found, err := collection.Get(t.Context(), input.ID, nil); err != nil || !found || got != input || input.ID == uuid.Nil {
					t.Fatalf("upsert did not use the recreated collection: %p, %v, %v", got, found, err)
				}
			} else {
				if err == nil {
					t.Fatal("upsert succeeded after deletion")
				}
				if input.ID != uuid.Nil {
					t.Fatal("upsert assigned a key after the collection was deleted")
				}
				if exists, err := collection.Exists(t.Context()); err != nil || exists {
					t.Fatalf("upsert recreated the collection: %v, %v", exists, err)
				}
			}
		})
	}
}

func TestEmbedding_UUIDAssignmentFollowsGeneration(t *testing.T) {
	type record struct {
		ID   uuid.UUID `vectordata:"key"`
		Text string    `vectordata:"vector,dimensions=2"`
	}
	input := &record{Text: "east"}
	want := errors.New("generation failed")
	fail := true
	generator := generatorFunc[string, *embedding.Of[float32]](func(ctx context.Context, values []string, options *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
		if input.ID != uuid.Nil {
			t.Fatal("key was assigned before embedding generation")
		}
		if fail {
			return nil, want
		}
		return textGenerator().Generate(ctx, values, options)
	})
	c := createCollection[uuid.UUID, *record](t, &vectordata.Definition{EmbeddingGenerator: embedding.Bind(generator)})
	if err := c.Upsert(t.Context(), input); !errors.Is(err, want) || input.ID != uuid.Nil {
		t.Fatalf("failed generation changed the key: %v, %v", input.ID, err)
	}
	fail = false
	if err := c.Upsert(t.Context(), input); err != nil || input.ID == uuid.Nil {
		t.Fatalf("successful generation did not assign the input key: %v, %v", input.ID, err)
	}
	if got, found, err := c.Get(t.Context(), input.ID, nil); err != nil || !found || got != input {
		t.Fatalf("stored record = %p, want %p; found = %v, err = %v", got, input, found, err)
	}
}

func TestEmbedding_ProviderManagedGenerationUnsupported(t *testing.T) {
	options := &vectordata.Definition{Properties: []vectordata.Property{
		&vectordata.VectorProperty{Name: "Text", Dimensions: 2, EmbeddingType: reflect.TypeFor[*embedding.Of[float32]]()},
	}}
	if _, err := inmemory.NewCollection[string, embeddedDocument]("records", options); err == nil {
		t.Fatal("accepted generated field without a generator")
	}
}
