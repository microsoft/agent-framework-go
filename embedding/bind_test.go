// Copyright (c) Microsoft. All rights reserved.

package embedding_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

var _ embedding.Generator[any, any] = (*embedding.BoundGenerator)(nil)

func TestBind_TypesAndForwarding(t *testing.T) {
	ctx := t.Context()
	first := &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{1, 2}, ModelID: "first"}
	second := &embedding.Of[float32]{Vector: embedding.VectorOf[float32]{3, 4}, ModelID: "second"}
	want := &embedding.GeneratedOf[float32]{
		Embeddings:           []*embedding.Of[float32]{first, second},
		Usage:                &message.UsageDetails{InputTokenCount: 7, TotalTokenCount: 7},
		AdditionalProperties: map[string]any{"source": "original"},
	}
	values := []any{"one", "two"}
	options := &embedding.Options{Dimensions: new(2), ModelID: new("test"), AdditionalProperties: map[string]any{"option": true}}
	calls, factoryCalls := 0, 0
	inner := &testGenerator[string, *embedding.Of[float32]]{}
	options.RawRepresentationFactory = func(g any) any {
		factoryCalls++
		if g != inner {
			t.Fatal("factory received the binding rather than the inner generator")
		}
		return "native options"
	}
	inner.generate = func(gotCtx context.Context, inputs []string, opts *embedding.Options) (*embedding.GeneratedOf[float32], error) {
		calls++
		if gotCtx != ctx || opts != options || !slices.Equal(inputs, []string{"one", "two"}) {
			t.Fatal("generation arguments were changed")
		}
		if opts.RawRepresentationFactory(inner) != "native options" {
			t.Fatal("factory result changed")
		}
		return want, nil
	}
	binding := embedding.Bind(inner)
	if binding.InputType() != reflect.TypeFor[string]() || binding.EmbeddingType() != reflect.TypeFor[*embedding.Of[float32]]() {
		t.Fatalf("binding types = %v -> %v", binding.InputType(), binding.EmbeddingType())
	}
	result, err := binding.Generate(ctx, values, options)
	if err != nil || result == nil || calls != 1 || factoryCalls != 1 {
		t.Fatalf("Generate = %+v, %v; calls = %d, factory calls = %d", result, err, calls, factoryCalls)
	}
	if len(result.Embeddings) != 2 || result.Embeddings[0] != first || result.Embeddings[1] != second || result.Usage != want.Usage {
		t.Fatal("output values, order, or usage identity changed")
	}
	if !reflect.DeepEqual(values, []any{"one", "two"}) || *options.Dimensions != 2 ||
		*options.ModelID != "test" || options.AdditionalProperties["option"] != true {
		t.Fatal("generation changed caller inputs")
	}
	result.Embeddings[0] = nil
	result.AdditionalProperties["source"] = "shared"
	if want.Embeddings[0] != first || want.AdditionalProperties["source"] != "shared" {
		t.Fatal("output slice must be independent and metadata values shared")
	}
}

func TestBoundGenerator_ValidatesAllInputsBeforeCalling(t *testing.T) {
	type namedString string
	for _, value := range []any{42, nil, namedString("text"), []byte("text")} {
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			calls := 0
			binding := embedding.Bind(&testGenerator[string, *embedding.Of[float32]]{
				generate: func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
					calls++
					return nil, nil
				},
			})
			inputs := []any{"valid", value}
			result, err := binding.Generate(t.Context(), inputs, nil)
			if err == nil || !strings.Contains(err.Error(), "index 1") || result != nil || calls != 0 {
				t.Fatalf("Generate = %+v, %v; calls = %d", result, err, calls)
			}
			if inputs[0] != "valid" {
				t.Fatal("validation changed earlier inputs")
			}
		})
	}
}

type stringInput string

func (value stringInput) String() string { return string(value) }

func TestBoundGenerator_CustomAndInterfaceInputs(t *testing.T) {
	type document struct{ Text string }
	type customEmbedding struct{ Values []float64 }
	value := &document{Text: "document"}
	want := customEmbedding{Values: []float64{1, 2}}
	binding := embedding.Bind(&testGenerator[*document, customEmbedding]{
		generate: func(_ context.Context, inputs []*document, _ *embedding.Options) (*embedding.Generated[customEmbedding], error) {
			if len(inputs) != 1 || inputs[0] != value {
				t.Fatal("custom input identity changed")
			}
			return &embedding.Generated[customEmbedding]{Embeddings: []customEmbedding{want}}, nil
		},
	})
	if binding.InputType() != reflect.TypeFor[*document]() || binding.EmbeddingType() != reflect.TypeFor[customEmbedding]() {
		t.Fatal("custom type tags were lost")
	}
	result, err := binding.Generate(t.Context(), []any{value}, nil)
	if err != nil || result == nil || len(result.Embeddings) != 1 || !reflect.DeepEqual(result.Embeddings[0], want) {
		t.Fatalf("custom result = %+v, %v", result, err)
	}
	got := result.Embeddings[0].(customEmbedding)
	if &got.Values[0] != &want.Values[0] {
		t.Fatal("custom output vector was copied")
	}
	interfaceBinding := embedding.Bind(&testGenerator[fmt.Stringer, string]{
		generate: func(_ context.Context, inputs []fmt.Stringer, _ *embedding.Options) (*embedding.Generated[string], error) {
			return &embedding.Generated[string]{Embeddings: []string{inputs[0].String()}}, nil
		},
	})
	result, err = interfaceBinding.Generate(t.Context(), []any{stringInput("text")}, nil)
	if err != nil || result.Embeddings[0] != "text" || interfaceBinding.InputType() != reflect.TypeFor[fmt.Stringer]() {
		t.Fatalf("interface result = %+v, %v", result, err)
	}
}

func TestBoundGenerator_PreservesTypedNilValues(t *testing.T) {
	var pointer *string
	var values []byte
	binding := embedding.Bind(&testGenerator[any, any]{
		generate: func(_ context.Context, inputs []any, _ *embedding.Options) (*embedding.Generated[any], error) {
			if len(inputs) != 2 || inputs[0] != pointer || !reflect.DeepEqual(inputs[1], values) {
				t.Fatalf("typed nil inputs = %+v", inputs)
			}
			return &embedding.Generated[any]{Embeddings: []any{nil, (*embedding.Of[float32])(nil)}}, nil
		},
	})
	result, err := binding.Generate(t.Context(), []any{pointer, values}, nil)
	if err != nil || result.Embeddings[0] != nil || result.Embeddings[1] != (*embedding.Of[float32])(nil) {
		t.Fatalf("typed nil result = %+v, %v", result, err)
	}
}

func TestBoundGenerator_EmptyBatchAndResultShapes(t *testing.T) {
	for _, nilInput := range []bool{false, true} {
		for _, nilOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("inputNil=%t/outputNil=%t", nilInput, nilOutput), func(t *testing.T) {
				calls := 0
				binding := embedding.Bind(&testGenerator[string, string]{
					generate: func(_ context.Context, inputs []string, _ *embedding.Options) (*embedding.Generated[string], error) {
						calls++
						if len(inputs) != 0 || (inputs == nil) != nilInput {
							t.Fatal("empty input shape changed")
						}
						outputs := []string{}
						if nilOutput {
							outputs = nil
						}
						return &embedding.Generated[string]{Embeddings: outputs}, nil
					},
				})
				inputs := []any{}
				if nilInput {
					inputs = nil
				}
				result, err := binding.Generate(t.Context(), inputs, nil)
				if err != nil || result == nil || len(result.Embeddings) != 0 || (result.Embeddings == nil) != nilOutput || calls != 1 {
					t.Fatalf("empty result = %+v, %v; calls = %d", result, err, calls)
				}
			})
		}
	}
}

func TestBoundGenerator_PreservesErrorsAndPartialResults(t *testing.T) {
	wantErr := errors.New("provider failed")
	for _, returnedErr := range []error{nil, wantErr, context.Canceled, context.DeadlineExceeded} {
		for _, hasResult := range []bool{false, true} {
			t.Run(fmt.Sprintf("error=%v/result=%t", returnedErr, hasResult), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var want *embedding.Generated[string]
				if hasResult {
					want = &embedding.Generated[string]{
						Embeddings: []string{"partial"}, Usage: &message.UsageDetails{InputTokenCount: 3},
						AdditionalProperties: map[string]any{"partial": true},
					}
				}
				binding := embedding.Bind(&testGenerator[string, string]{
					generate: func(gotCtx context.Context, _ []string, _ *embedding.Options) (*embedding.Generated[string], error) {
						if gotCtx != ctx {
							t.Fatal("context was replaced")
						}
						cancel()
						return want, returnedErr
					},
				})
				got, err := binding.Generate(ctx, []any{"input"}, nil)
				if err != returnedErr || (got != nil) != hasResult {
					t.Fatalf("Generate = %+v, %v; expected result=%t and error=%v", got, err, hasResult, returnedErr)
				}
				if got != nil && (got.Usage != want.Usage || got.Embeddings[0] != "partial" ||
					!reflect.DeepEqual(got.AdditionalProperties, want.AdditionalProperties)) {
					t.Fatalf("partial metadata changed: %+v", got)
				}
			})
		}
	}
}

func TestBoundGenerator_CanceledContextAndInvalidBinding(t *testing.T) {
	calls := 0
	binding := embedding.Bind(&testGenerator[string, string]{
		generate: func(context.Context, []string, *embedding.Options) (*embedding.Generated[string], error) {
			calls++
			return nil, nil
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, inputs := range [][]any{nil, {"text"}} {
		if result, err := binding.Generate(ctx, inputs, nil); result != nil || err != context.Canceled || calls != 0 {
			t.Fatalf("canceled Generate = %+v, %v; calls = %d", result, err, calls)
		}
	}
	for _, invalid := range []*embedding.BoundGenerator{nil, {}} {
		if result, err := invalid.Generate(t.Context(), []any{"text"}, nil); result != nil || err == nil {
			t.Fatalf("invalid binding = %+v, %v", result, err)
		}
	}
}

func TestBind_RejectsNilGenerator(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected nil generator panic")
		}
	}()
	embedding.Bind[string, string](nil)
}

func TestBoundGenerator_ZeroTypeMetadata(t *testing.T) {
	var binding embedding.BoundGenerator
	if binding.InputType() != nil || binding.EmbeddingType() != nil {
		t.Fatal("uninitialized binding must not claim input or output types")
	}
}

type nilBindingGenerator struct{}

func (*nilBindingGenerator) Generate(context.Context, []string, *embedding.Options) (*embedding.Generated[string], error) {
	return &embedding.Generated[string]{Embeddings: []string{"nil receiver supported"}}, nil
}

func TestBind_TypedNilGenerator(t *testing.T) {
	binding := embedding.Bind((*nilBindingGenerator)(nil))
	result, err := binding.Generate(t.Context(), []any{"text"}, nil)
	if err != nil || result.Embeddings[0] != "nil receiver supported" {
		t.Fatalf("typed nil generator = %+v, %v", result, err)
	}
}

func TestBoundGenerator_ConcurrentCalls(t *testing.T) {
	binding := embedding.Bind(&testGenerator[string, *embedding.Of[float64]]{
		generate: func(_ context.Context, inputs []string, _ *embedding.Options) (*embedding.GeneratedOf[float64], error) {
			return &embedding.GeneratedOf[float64]{Embeddings: []*embedding.Of[float64]{{ModelID: inputs[0]}}}, nil
		},
	})
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			input := fmt.Sprint(i)
			got, err := binding.Generate(t.Context(), []any{input}, nil)
			if err != nil || got.Embeddings[0].(*embedding.Of[float64]).ModelID != input {
				t.Errorf("concurrent result = %+v, %v; want %q", got, err, input)
			}
		})
	}
	wg.Wait()
}

type closableBindingGenerator struct {
	*testGenerator[string, string]
	closed bool
}

func (g *closableBindingGenerator) Close() error {
	g.closed = true
	return nil
}

func TestBoundGenerator_DoesNotOwnGenerator(t *testing.T) {
	inner := &closableBindingGenerator{testGenerator: &testGenerator[string, string]{
		generate: func(context.Context, []string, *embedding.Options) (*embedding.Generated[string], error) {
			return &embedding.Generated[string]{}, nil
		},
	}}
	binding := embedding.Bind(inner)
	if _, ok := any(binding).(io.Closer); ok {
		t.Fatal("binding must not expose ownership of the generator")
	}
	if _, err := binding.Generate(t.Context(), []any{"text"}, nil); err != nil || inner.closed {
		t.Fatalf("Generate = %v; closed = %t", err, inner.closed)
	}
}

type bindingTransport func(*http.Request) (*http.Response, error)

func (f bindingTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestBoundGenerator_OpenAIAndTelemetry(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tracerProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	requests, factoryCalls := 0, 0
	native := openaiprovider.NewEmbeddingGenerator(openai.NewClient(
		option.WithAPIKey("test-key"), option.WithBaseURL("https://example.test/"), option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{Transport: bindingTransport(func(req *http.Request) (*http.Response, error) {
			requests++
			var body struct {
				Input []string `json:"input"`
				User  string   `json:"user"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(body.Input, []string{"hello"}) || body.User != "native-user" {
				t.Fatalf("request = %+v", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Request: req,
				Body: io.NopCloser(strings.NewReader(`{"data":[{"index":0,"embedding":[1,2]}],"model":"served","usage":{"prompt_tokens":4,"total_tokens":4}}`)),
			}, nil
		})}),
	), openaiprovider.EmbeddingGeneratorConfig{Model: "requested"})
	wrapped := otelprovider.WrapEmbeddingGenerator(native, otelprovider.EmbeddingGeneratorConfig{DefaultModelID: "requested"})
	binding := embedding.Bind(wrapped)
	got, err := binding.Generate(t.Context(), []any{"hello"}, &embedding.Options{
		RawRepresentationFactory: func(generator any) any {
			factoryCalls++
			if generator != native {
				t.Fatal("raw factory did not receive the native generator")
			}
			return openai.EmbeddingNewParams{User: openai.String("native-user")}
		},
	})
	if err != nil || got == nil || len(got.Embeddings) != 1 || got.Usage == nil || got.Usage.InputTokenCount != 4 ||
		requests != 1 || factoryCalls != 1 {
		t.Fatalf("Generate = %+v, %v; requests = %d, factories = %d", got, err, requests, factoryCalls)
	}
	value, ok := got.Embeddings[0].(*embedding.Of[float32])
	if !ok || value.ModelID != "served" || !slices.Equal(value.Vector, []float32{1, 2}) {
		t.Fatalf("embedding = %+v", got.Embeddings[0])
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "embeddings requested" {
		t.Fatalf("spans = %+v", spans)
	}
}

func ExampleBind() {
	generator := &testGenerator[string, *embedding.Of[float32]]{
		generate: func(_ context.Context, values []string, _ *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			return &embedding.GeneratedOf[float32]{
				Embeddings: []*embedding.Of[float32]{{Vector: embedding.VectorOf[float32]{float32(len(values[0]))}}},
			}, nil
		},
	}
	binding := embedding.Bind(generator)
	result, err := binding.Generate(context.Background(), []any{"hello"}, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(binding.InputType())
	fmt.Println(result.Embeddings[0].(*embedding.Of[float32]).Vector)
	// Output:
	// string
	// [5]
}
