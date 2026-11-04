// Copyright (c) Microsoft. All rights reserved.

package otelprovider_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"

	otellib "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type embeddingGeneratorFunc[I, E any] func(context.Context, []I, *embedding.Options) (*embedding.Generated[E], error)

func (f embeddingGeneratorFunc[I, E]) Generate(ctx context.Context, values []I, options *embedding.Options) (*embedding.Generated[E], error) {
	return f(ctx, values, options)
}

func singleEmbeddingSpan(t *testing.T, exporter *tracetest.InMemoryExporter) (tracetest.SpanStub, map[string]attribute.Value) {
	t.Helper()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	attrs := make(map[string]attribute.Value)
	for _, attr := range spans[0].Attributes {
		attrs[string(attr.Key)] = attr.Value
	}
	return spans[0], attrs
}

func TestOpenTelemetryEmbeddingGenerator_ExpectedInformationLogged_Async(t *testing.T) {
	for _, test := range []struct {
		name      string
		modelID   *string
		sensitive bool
	}{
		{"default model", nil, false},
		{"request model", new("replacementmodel"), false},
		{"sensitive", new("replacementmodel"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				exporter := setupTracer(t)
				want := &embedding.GeneratedOf[float32]{
					Embeddings: []*embedding.Of[float32]{{Vector: embedding.VectorOf[float32]{1, 2, 3}}},
					Usage:      &message.UsageDetails{InputTokenCount: 10, TotalTokenCount: 10},
					AdditionalProperties: map[string]any{
						"system_fingerprint": "abcdefgh",
						"AndSomethingElse":   "value3",
					},
				}
				inner := embeddingGeneratorFunc[string, *embedding.Of[float32]](func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
					time.Sleep(time.Second)
					return want, nil
				})
				g := otelprovider.WrapEmbeddingGenerator(inner, otelprovider.EmbeddingGeneratorConfig{
					SourceName: "test-embedding-source", ProviderName: "testservice",
					ServerAddress: "localhost", ServerPort: 12345,
					DefaultModelID: "defaultmodel", DefaultModelDimensions: new(1234),
					EnableSensitiveData: new(test.sensitive),
				})
				options := &embedding.Options{
					ModelID: test.modelID,
					AdditionalProperties: map[string]any{
						"service_tier":  "value1",
						"SomethingElse": "value2",
					},
				}
				vector, err := embedding.GenerateVector(t.Context(), g, "hello", options)
				if err != nil || !slices.Equal(vector, []float32{1, 2, 3}) {
					t.Fatalf("GenerateVector = %v, %v", vector, err)
				}
				span, attrs := singleEmbeddingSpan(t, exporter)
				model := "defaultmodel"
				if test.modelID != nil {
					model = *test.modelID
				}
				if span.Name != "embeddings "+model || span.SpanKind != trace.SpanKindClient ||
					!span.SpanContext.IsValid() || !span.EndTime.After(span.StartTime) {
					t.Fatalf("span = %+v", span)
				}
				if span.InstrumentationScope.Name != "test-embedding-source" {
					t.Errorf("scope = %q", span.InstrumentationScope.Name)
				}
				for key, want := range map[string]string{
					"gen_ai.operation.name": "embeddings",
					"gen_ai.provider.name":  "testservice",
					"gen_ai.request.model":  model,
					"server.address":        "localhost",
				} {
					if attrs[key].AsString() != want {
						t.Errorf("%s = %v, want %q", key, attrs[key], want)
					}
				}
				for key, want := range map[string]int64{
					"server.port":                       12345,
					"gen_ai.embeddings.dimension.count": 1234,
					"gen_ai.usage.input_tokens":         10,
				} {
					if attrs[key].AsInt64() != want {
						t.Errorf("%s = %v, want %d", key, attrs[key], want)
					}
				}
				for key, value := range map[string]string{
					"service_tier": "value1", "SomethingElse": "value2",
					"system_fingerprint": "abcdefgh", "AndSomethingElse": "value3",
				} {
					attr, ok := attrs[key]
					if ok != test.sensitive || ok && attr.AsString() != value {
						t.Errorf("%s = %v, present = %v, sensitive = %v", key, attr, ok, test.sensitive)
					}
				}
				for _, key := range []string{"gen_ai.response.model", "gen_ai.usage.output_tokens", "gen_ai.input.messages", "gen_ai.output.messages"} {
					if _, ok := attrs[key]; ok {
						t.Errorf("unexpected attribute %s", key)
					}
				}
				if len(span.Events) != 0 {
					t.Errorf("raw input/output events recorded: %v", span.Events)
				}
			})
		})
	}
}

type embeddingLogHandler struct {
	slog.Handler
	records []slog.Record
}

func (h *embeddingLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record.Clone())
	return nil
}

func TestOpenTelemetryEmbeddingGenerator_ExceptionLogged_Async(t *testing.T) {
	exporter := setupTracer(t)
	reader := setupMeter(t)
	handler := &embeddingLogHandler{Handler: slog.NewTextHandler(io.Discard, nil)}
	wantErr := errors.New("test exception message")
	inner := embeddingGeneratorFunc[string, *embedding.Of[float32]](func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
		return nil, wantErr
	})
	g := otelprovider.WrapEmbeddingGenerator(inner, otelprovider.EmbeddingGeneratorConfig{
		ProviderName: "testservice", ServerAddress: "localhost", ServerPort: 12345,
		DefaultModelID: "testmodel", Logger: slog.New(handler),
	})
	result, err := g.Generate(t.Context(), []string{"hello"}, nil)
	if err != wantErr || result != nil {
		t.Fatalf("Generate = %+v, %v; want original error", result, err)
	}
	span, attrs := singleEmbeddingSpan(t, exporter)
	if attrs["error.type"].AsString() != "errorString" ||
		span.Status.Code != codes.Error || span.Status.Description != wantErr.Error() {
		t.Fatalf("error span = %+v, attributes = %v", span, attrs)
	}
	if len(span.Events) != 0 {
		t.Error("errors should be logged, not recorded as exception span events")
	}
	if len(handler.records) != 1 {
		t.Fatalf("logs = %d, want 1", len(handler.records))
	}
	record := handler.records[0]
	if record.Level != slog.LevelWarn || record.Message != "gen_ai.client.operation.exception" {
		t.Errorf("log = %+v", record)
	}
	var loggedError any
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "error" {
			loggedError = attr.Value.Any()
		}
		return true
	})
	if loggedError != wantErr {
		t.Errorf("logged error = %v, want original error", loggedError)
	}
	duration := collectMetric(t, reader, "gen_ai.client.operation.duration").(metricdata.Histogram[float64])
	if len(duration.DataPoints) != 1 {
		t.Fatalf("duration points = %v", duration.DataPoints)
	}
	if errorType, _ := metricAttribute(duration.DataPoints[0].Attributes, "error.type"); errorType != "errorString" {
		t.Errorf("metric error.type = %q", errorType)
	}
	if hasMetric(collectMetrics(t, reader), "gen_ai.client.token.usage") {
		t.Error("failure recorded token usage")
	}
}

func TestEmbeddingTelemetry_Metrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exporter := setupTracer(t)
		reader := setupMeter(t)
		inner := embeddingGeneratorFunc[string, *embedding.Of[float32]](func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			time.Sleep(time.Second)
			return &embedding.GeneratedOf[float32]{
				Embeddings: []*embedding.Of[float32]{
					{ModelID: "response-model"},
					{ModelID: "ignored-model"},
				},
				Usage:                &message.UsageDetails{InputTokenCount: 1 << 40, OutputTokenCount: 5, TotalTokenCount: 1<<40 + 5},
				AdditionalProperties: map[string]any{"sensitive-response": "hidden"},
			}, nil
		})
		g := otelprovider.WrapEmbeddingGenerator(inner, otelprovider.EmbeddingGeneratorConfig{
			SourceName: "embedding-metrics", ProviderName: "test-provider",
			ServerAddress: "localhost", ServerPort: 443, DefaultModelID: "default",
			DefaultModelDimensions: new(10), EnableSensitiveData: new(true),
		})
		_, err := g.Generate(t.Context(), []string{"first", "second"}, &embedding.Options{
			ModelID: new("request-model"), Dimensions: new(0),
			AdditionalProperties: map[string]any{"sensitive-request": "hidden"},
		})
		if err != nil {
			t.Fatal(err)
		}
		span, attrs := singleEmbeddingSpan(t, exporter)
		if attrs["gen_ai.response.model"].AsString() != "response-model" ||
			attrs["gen_ai.usage.input_tokens"].AsInt64() != 1<<40 ||
			attrs["gen_ai.embeddings.dimension.count"].AsInt64() != 0 {
			t.Fatalf("response attributes = %v", attrs)
		}
		metrics := collectMetrics(t, reader)
		seen := map[string]bool{}
		for _, scope := range metrics.ScopeMetrics {
			if scope.Scope.Name != "embedding-metrics" {
				t.Errorf("metric scope = %q", scope.Scope.Name)
			}
			for _, m := range scope.Metrics {
				seen[m.Name] = true
				var points []attribute.Set
				switch m.Name {
				case "gen_ai.client.operation.duration":
					histogram := m.Data.(metricdata.Histogram[float64])
					if m.Unit != "s" || len(histogram.DataPoints) != 1 {
						t.Fatalf("duration metric = %+v", m)
					}
					point := histogram.DataPoints[0]
					if point.Count != 1 || point.Sum != 1 || point.Sum > span.EndTime.Sub(span.StartTime).Seconds() {
						t.Errorf("duration point = %+v", point)
					}
					if !slices.Equal(point.Bounds, []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}) {
						t.Errorf("duration bounds = %v", point.Bounds)
					}
					points = append(points, point.Attributes)
				case "gen_ai.client.token.usage":
					histogram := m.Data.(metricdata.Histogram[int64])
					if m.Unit != "{token}" || len(histogram.DataPoints) != 1 {
						t.Fatalf("usage metric = %+v", m)
					}
					point := histogram.DataPoints[0]
					if point.Count != 1 || point.Sum != 1<<40 {
						t.Errorf("usage point = %+v", point)
					}
					if !slices.Equal(point.Bounds, []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}) {
						t.Errorf("usage bounds = %v", point.Bounds)
					}
					if tokenType, _ := metricAttribute(point.Attributes, "gen_ai.token.type"); tokenType != "input" {
						t.Errorf("token type = %q", tokenType)
					}
					points = append(points, point.Attributes)
				default:
					t.Errorf("unexpected metric %q", m.Name)
				}
				for _, attrs := range points {
					for key, want := range map[string]string{
						"gen_ai.operation.name": "embeddings", "gen_ai.provider.name": "test-provider",
						"gen_ai.request.model": "request-model", "gen_ai.response.model": "response-model",
						"server.address": "localhost",
					} {
						if got, _ := metricAttribute(attrs, key); got != want {
							t.Errorf("%s = %q, want %q", key, got, want)
						}
					}
					if port, ok := attrs.Value("server.port"); !ok || port.AsInt64() != 443 {
						t.Errorf("server.port = %v, %v", port, ok)
					}
					for _, key := range []attribute.Key{"error.type", "gen_ai.embeddings.dimension.count", "sensitive-request", "sensitive-response", "gen_ai.agent.id"} {
						if _, ok := attrs.Value(key); ok {
							t.Errorf("metric unexpectedly contains %s", key)
						}
					}
				}
			}
		}
		if !seen["gen_ai.client.operation.duration"] || !seen["gen_ai.client.token.usage"] {
			t.Fatalf("recorded metrics = %v", seen)
		}
	})
}

func TestEmbeddingTelemetry_TypedResponseModels(t *testing.T) {
	testEmbeddingResponseModels[embedding.Vector](t, embedding.VectorOf[float32]{1})
	testEmbeddingResponseModels(t, embedding.VectorOf[uint8]{1})
	testEmbeddingResponseModels(t, embedding.VectorOf[int8]{1})
	testEmbeddingResponseModels(t, embedding.VectorOf[float32]{1})
	testEmbeddingResponseModels(t, embedding.VectorOf[float64]{1})
	testEmbeddingResponseModels(t, embedding.Binary{true})
}

func testEmbeddingResponseModels[TVector embedding.Vector](t *testing.T, vector TVector) {
	t.Helper()
	for i, first := range []any{
		embedding.Embedding[TVector]{ModelID: "served-model", Vector: vector},
		&embedding.Embedding[TVector]{ModelID: "served-model", Vector: vector},
		(*embedding.Embedding[TVector])(nil),
	} {
		t.Run(fmt.Sprintf("%T/%d", first, i), func(t *testing.T) {
			exporter := setupTracer(t)
			want := &embedding.Generated[any]{Embeddings: []any{first}}
			g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, any](
				func(context.Context, []string, *embedding.Options) (*embedding.Generated[any], error) {
					return want, nil
				}), otelprovider.EmbeddingGeneratorConfig{})
			got, err := g.Generate(t.Context(), []string{"hello"}, nil)
			if err != nil || got != want {
				t.Fatalf("Generate = %+v, %v", got, err)
			}
			_, attrs := singleEmbeddingSpan(t, exporter)
			model, present := attrs["gen_ai.response.model"]
			if i == 2 {
				if present {
					t.Fatalf("nil embedding supplied response model %v", model)
				}
			} else if !present || model.AsString() != "served-model" {
				t.Fatalf("response model = %v, %v", model, present)
			}
		})
	}
}

func TestEmbeddingTelemetry_UsageAndFirstResponseModel(t *testing.T) {
	for _, test := range []struct {
		name       string
		result     *embedding.GeneratedOf[float32]
		wantUsage  bool
		wantTokens int64
		wantModel  string
	}{
		{"nil response", nil, false, 0, ""},
		{"no usage", &embedding.GeneratedOf[float32]{}, false, 0, ""},
		{"explicit zero", &embedding.GeneratedOf[float32]{Usage: &message.UsageDetails{}}, true, 0, ""},
		{"output only", &embedding.GeneratedOf[float32]{Usage: &message.UsageDetails{OutputTokenCount: 5}}, true, 0, ""},
		{"first nil", &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{nil, {ModelID: "later"}}}, false, 0, ""},
		{"first unnamed", &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{{}, {ModelID: "later"}}}, false, 0, ""},
		{"first named", &embedding.GeneratedOf[float32]{Embeddings: []*embedding.Of[float32]{{ModelID: "first"}, {ModelID: "later"}}}, false, 0, "first"},
	} {
		t.Run(test.name, func(t *testing.T) {
			exporter := setupTracer(t)
			reader := setupMeter(t)
			g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
				func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
					return test.result, nil
				}), otelprovider.EmbeddingGeneratorConfig{})
			got, err := g.Generate(t.Context(), []string{"hello"}, nil)
			if err != nil || got != test.result {
				t.Fatalf("Generate = %+v, %v", got, err)
			}
			span, attrs := singleEmbeddingSpan(t, exporter)
			if span.InstrumentationScope.Name != "github.com/microsoft/agent-framework-go" {
				t.Errorf("scope = %q", span.InstrumentationScope.Name)
			}
			model, hasModel := attrs["gen_ai.response.model"]
			if model.AsString() != test.wantModel || hasModel != (test.wantModel != "") {
				t.Errorf("response model = %v, %v", model, hasModel)
			}
			tokens, hasTokens := attrs["gen_ai.usage.input_tokens"]
			if hasTokens != test.wantUsage || hasTokens && tokens.AsInt64() != test.wantTokens {
				t.Errorf("input tokens = %v, %v", tokens, hasTokens)
			}
			if hasMetric(collectMetrics(t, reader), "gen_ai.client.token.usage") != test.wantUsage {
				t.Error("usage metric presence does not match reported usage")
			}
			if test.wantUsage {
				usage := collectMetric(t, reader, "gen_ai.client.token.usage").(metricdata.Histogram[int64])
				if len(usage.DataPoints) != 1 || usage.DataPoints[0].Count != 1 || usage.DataPoints[0].Sum != test.wantTokens {
					t.Errorf("usage points = %+v", usage.DataPoints)
				}
			}
			for _, key := range []string{"gen_ai.provider.name", "gen_ai.request.model", "server.address", "server.port", "gen_ai.embeddings.dimension.count"} {
				if _, ok := attrs[key]; ok {
					t.Errorf("unknown metadata should be omitted: %s", key)
				}
			}
		})
	}
}

func TestEmbeddingTelemetry_RequestModelAndDimensions(t *testing.T) {
	for _, test := range []struct {
		name       string
		options    *embedding.Options
		wantModel  string
		wantName   string
		dimensions int64
	}{
		{"defaults", nil, "default-model", "embeddings default-model", 1234},
		{"empty model", &embedding.Options{ModelID: new("")}, "", "embeddings", 1234},
		{"whitespace model", &embedding.Options{ModelID: new(" \t ")}, " \t ", "embeddings", 1234},
		{"overrides", &embedding.Options{ModelID: new("request-model"), Dimensions: new(0)}, "request-model", "embeddings request-model", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			exporter := setupTracer(t)
			inner := embeddingGeneratorFunc[string, *embedding.Of[float32]](func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
				return &embedding.GeneratedOf[float32]{}, nil
			})
			g := otelprovider.WrapEmbeddingGenerator(inner, otelprovider.EmbeddingGeneratorConfig{
				DefaultModelID: "default-model", DefaultModelDimensions: new(1234),
			})
			if _, err := g.Generate(t.Context(), nil, test.options); err != nil {
				t.Fatal(err)
			}
			span, attrs := singleEmbeddingSpan(t, exporter)
			model, hasModel := attrs["gen_ai.request.model"]
			dimensions, hasDimensions := attrs["gen_ai.embeddings.dimension.count"]
			if span.Name != test.wantName || !hasModel || model.AsString() != test.wantModel ||
				!hasDimensions || dimensions.AsInt64() != test.dimensions {
				t.Fatalf("span = %s, attributes = %v", span.Name, attrs)
			}
		})
	}
}

func TestEmbeddingTelemetry_SensitiveDataDefaults(t *testing.T) {
	for _, test := range []struct {
		env      string
		explicit *bool
		want     bool
	}{
		{"", nil, false},
		{"true", nil, true},
		{"TrUe", nil, true},
		{"false", nil, false},
		{"1", nil, false},
		{" true ", nil, false},
		{"true", new(false), false},
		{"false", new(true), true},
	} {
		t.Run(fmt.Sprintf("%q/%v", test.env, test.explicit), func(t *testing.T) {
			t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", test.env)
			exporter := setupTracer(t)
			g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
				func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
					return &embedding.GeneratedOf[float32]{AdditionalProperties: map[string]any{"response-secret": "value"}}, nil
				}), otelprovider.EmbeddingGeneratorConfig{EnableSensitiveData: test.explicit})
			_, err := g.Generate(t.Context(), []string{"raw input must not be recorded"}, &embedding.Options{
				AdditionalProperties: map[string]any{"request-secret": "value"},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, attrs := singleEmbeddingSpan(t, exporter)
			for key, value := range attrs {
				if strings.Contains(value.String(), "raw input must not be recorded") {
					t.Errorf("raw input captured in %s", key)
				}
			}
			for _, key := range []string{"request-secret", "response-secret"} {
				if _, present := attrs[key]; present != test.want {
					t.Errorf("%s present = %v, want %v", key, present, test.want)
				}
			}
		})
	}
}

func TestEmbeddingTelemetry_SensitivePropertyTypes(t *testing.T) {
	exporter := setupTracer(t)
	properties := map[string]any{
		"string": "value", "bool": true, "int": int(42),
		"int8": int8(42), "int16": int16(42), "int32": int32(42), "int64": int64(42),
		"uint8": uint8(42), "uint16": uint16(42), "uint32": uint32(42),
		"float32": float32(1.5), "float64": float64(1.5),
		"strings": []string{"a", "b"}, "bools": []bool{true, false},
		"ints": []int{1, 2}, "int64s": []int64{1, 2}, "floats": []float64{1.5, 2.5},
		"attribute": attribute.StringValue("raw"), "other": struct{ Field int }{42}, "nil": nil,
	}
	g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
		func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			return &embedding.GeneratedOf[float32]{}, nil
		}), otelprovider.EmbeddingGeneratorConfig{EnableSensitiveData: new(true)})
	_, err := g.Generate(t.Context(), []string{"hello"}, &embedding.Options{AdditionalProperties: properties})
	if err != nil {
		t.Fatal(err)
	}
	_, attrs := singleEmbeddingSpan(t, exporter)
	for _, key := range []string{"int", "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32"} {
		if attrs[key].Type() != attribute.INT64 || attrs[key].AsInt64() != 42 {
			t.Errorf("%s = %v", key, attrs[key])
		}
	}
	for _, key := range []string{"float32", "float64"} {
		if attrs[key].Type() != attribute.FLOAT64 || attrs[key].AsFloat64() != 1.5 {
			t.Errorf("%s = %v", key, attrs[key])
		}
	}
	for key, want := range map[string]any{
		"string": "value", "bool": true, "strings": []string{"a", "b"},
		"bools": []bool{true, false}, "ints": []int64{1, 2}, "int64s": []int64{1, 2},
		"floats": []float64{1.5, 2.5}, "attribute": "raw", "other": "{42}",
	} {
		if !reflect.DeepEqual(attrs[key].AsInterface(), want) {
			t.Errorf("%s = %v, want %v", key, attrs[key].AsInterface(), want)
		}
	}
	if _, ok := attrs["nil"]; ok {
		t.Error("nil properties should be omitted")
	}
}

func TestEmbeddingTelemetry_ForwardsRequestsAndGenericOutputs(t *testing.T) {
	exporter := setupTracer(t)
	type input struct{ Data string }
	type output struct{ Vector []float32 }
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "context-value")
	values := []input{{"raw-input"}}
	want := &embedding.Generated[output]{
		Embeddings: []output{{Vector: []float32{1, 2, 3}}},
		Usage:      &message.UsageDetails{InputTokenCount: 3},
	}
	options := &embedding.Options{
		Dimensions: new(0), ModelID: new("test-model"),
		AdditionalProperties: map[string]any{"request": "unchanged"},
		RawRepresentationFactory: func(any) any {
			t.Error("telemetry must not invoke the raw options factory")
			return nil
		},
	}
	g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[input, output](
		func(gotCtx context.Context, got []input, gotOptions *embedding.Options) (*embedding.Generated[output], error) {
			if gotCtx.Value(contextKey{}) != "context-value" || !trace.SpanFromContext(gotCtx).SpanContext().IsValid() ||
				&got[0] != &values[0] || gotOptions != options {
				t.Fatal("request was not forwarded unchanged with a trace context")
			}
			return want, nil
		}), otelprovider.EmbeddingGeneratorConfig{EnableSensitiveData: new(false)})
	got, err := g.Generate(ctx, values, options)
	if err != nil || got != want || values[0].Data != "raw-input" || *options.Dimensions != 0 ||
		options.AdditionalProperties["request"] != "unchanged" {
		t.Fatalf("Generate = %+v, %v", got, err)
	}
	_, attrs := singleEmbeddingSpan(t, exporter)
	if attrs["gen_ai.usage.input_tokens"].AsInt64() != 3 {
		t.Errorf("generic result usage = %v", attrs)
	}
	if _, ok := attrs["gen_ai.response.model"]; ok {
		t.Error("custom output should not acquire a fabricated response model")
	}
}

func TestEmbeddingTelemetry_ConfigIsSnapshot(t *testing.T) {
	exporter := setupTracer(t)
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "false")
	dimensions, sensitive := 1234, false
	g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, embedding.Of[float32]](
		func(context.Context, []string, *embedding.Options) (*embedding.Generated[embedding.Of[float32]], error) {
			return &embedding.Generated[embedding.Of[float32]]{Embeddings: []embedding.Of[float32]{{ModelID: "served-model"}}}, nil
		}), otelprovider.EmbeddingGeneratorConfig{
		DefaultModelDimensions: &dimensions, EnableSensitiveData: &sensitive,
	})
	dimensions, sensitive = 99, true
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "true")
	if _, err := g.Generate(t.Context(), nil, &embedding.Options{AdditionalProperties: map[string]any{"secret": "value"}}); err != nil {
		t.Fatal(err)
	}
	_, attrs := singleEmbeddingSpan(t, exporter)
	if attrs["gen_ai.embeddings.dimension.count"].AsInt64() != 1234 || attrs["gen_ai.response.model"].AsString() != "served-model" {
		t.Errorf("attributes = %v", attrs)
	}
	if _, ok := attrs["secret"]; ok {
		t.Error("changing constructor inputs changed sensitive-data policy")
	}
}

func TestEmbeddingTelemetry_NilAndEmptyBatches(t *testing.T) {
	for _, values := range [][]string{nil, {}} {
		t.Run(fmt.Sprintf("nil=%v", values == nil), func(t *testing.T) {
			calls := 0
			g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
				func(_ context.Context, got []string, options *embedding.Options) (*embedding.GeneratedOf[float32], error) {
					calls++
					if (got == nil) != (values == nil) || len(got) != 0 || options != nil {
						t.Error("empty request was not preserved")
					}
					return nil, nil
				}), otelprovider.EmbeddingGeneratorConfig{})
			if result, err := g.Generate(t.Context(), values, nil); result != nil || err != nil || calls != 1 {
				t.Fatalf("Generate = %+v, %v; calls = %d", result, err, calls)
			}
		})
	}
}

func TestEmbeddingTelemetry_RepeatedRequestsDoNotRetainMetadata(t *testing.T) {
	exporter := setupTracer(t)
	calls := 0
	g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
		func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			calls++
			if calls == 1 {
				return &embedding.GeneratedOf[float32]{
					Embeddings:           []*embedding.Of[float32]{{ModelID: "response-model"}},
					Usage:                &message.UsageDetails{InputTokenCount: 5},
					AdditionalProperties: map[string]any{"first-response": "secret"},
				}, nil
			}
			return &embedding.GeneratedOf[float32]{}, nil
		}), otelprovider.EmbeddingGeneratorConfig{DefaultModelID: "default-model", EnableSensitiveData: new(true)})
	_, err := g.Generate(t.Context(), []string{"first"}, &embedding.Options{
		ModelID: new("first-model"), Dimensions: new(3),
		AdditionalProperties: map[string]any{"first-request": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Generate(t.Context(), []string{"second"}, nil); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 || spans[0].Name != "embeddings first-model" || spans[1].Name != "embeddings default-model" {
		t.Fatalf("spans = %+v", spans)
	}
	for _, attr := range spans[1].Attributes {
		switch string(attr.Key) {
		case "first-response", "first-request", "gen_ai.response.model", "gen_ai.usage.input_tokens", "gen_ai.embeddings.dimension.count":
			t.Errorf("second request retained %s", attr.Key)
		}
	}
}

func TestEmbeddingTelemetry_PropagatesParentAndChildSpans(t *testing.T) {
	exporter := setupTracer(t)
	parentCtx, parent := otellib.Tracer("parent").Start(t.Context(), "parent")
	g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
		func(ctx context.Context, _ []string, _ *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			_, child := otellib.Tracer("nested").Start(ctx, "child")
			child.End()
			return &embedding.GeneratedOf[float32]{}, nil
		}), otelprovider.EmbeddingGeneratorConfig{})
	_, err := g.Generate(parentCtx, []string{"hello"}, nil)
	parent.End()
	if err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3", len(spans))
	}
	byName := map[string]tracetest.SpanStub{}
	for _, span := range spans {
		byName[span.Name] = span
	}
	generated := byName["embeddings"]
	if generated.Parent.SpanID() != parent.SpanContext().SpanID() ||
		generated.SpanContext.TraceID() != parent.SpanContext().TraceID() ||
		byName["child"].Parent.SpanID() != generated.SpanContext.SpanID() {
		t.Fatalf("incorrect span hierarchy: %+v", byName)
	}
}

func TestEmbeddingTelemetry_ErrorsAndCancellation(t *testing.T) {
	for _, test := range []struct {
		err       error
		errorType string
	}{
		{errors.New("provider failed"), "errorString"},
		{context.Canceled, "errorString"},
		{context.DeadlineExceeded, "deadlineExceededError"},
	} {
		t.Run(test.err.Error(), func(t *testing.T) {
			exporter := setupTracer(t)
			reader := setupMeter(t)
			globalLogs := &embeddingLogHandler{Handler: slog.NewTextHandler(io.Discard, nil)}
			originalLogger := slog.Default()
			slog.SetDefault(slog.New(globalLogs))
			t.Cleanup(func() { slog.SetDefault(originalLogger) })
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			partial := &embedding.GeneratedOf[float32]{
				Embeddings: []*embedding.Of[float32]{{ModelID: "unsuccessful-response"}},
				Usage:      &message.UsageDetails{InputTokenCount: 100},
			}
			g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
				func(gotCtx context.Context, _ []string, _ *embedding.Options) (*embedding.GeneratedOf[float32], error) {
					if gotCtx.Err() != context.Canceled {
						t.Error("canceled context was not forwarded")
					}
					return partial, test.err
				}), otelprovider.EmbeddingGeneratorConfig{})
			got, err := g.Generate(ctx, []string{"hello"}, nil)
			if err != test.err || got != partial {
				t.Fatalf("Generate = %+v, %v", got, err)
			}
			span, attrs := singleEmbeddingSpan(t, exporter)
			if span.Status.Code != codes.Error || attrs["error.type"].AsString() != test.errorType {
				t.Errorf("failure span = %+v, attributes = %v", span, attrs)
			}
			for _, key := range []string{"gen_ai.response.model", "gen_ai.usage.input_tokens"} {
				if _, ok := attrs[key]; ok {
					t.Errorf("unsuccessful result recorded %s", key)
				}
			}
			if len(globalLogs.records) != 0 {
				t.Error("nil logger must not use the global logger")
			}
			if hasMetric(collectMetrics(t, reader), "gen_ai.client.token.usage") {
				t.Error("failure should not record success token usage")
			}
			duration := collectMetric(t, reader, "gen_ai.client.operation.duration").(metricdata.Histogram[float64])
			if len(duration.DataPoints) != 1 {
				t.Fatalf("duration points = %v", duration.DataPoints)
			}
			if errorType, _ := metricAttribute(duration.DataPoints[0].Attributes, "error.type"); errorType != test.errorType {
				t.Errorf("metric error.type = %q", errorType)
			}
		})
	}
}

func TestEmbeddingTelemetry_MetricsWithoutSampledSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()), sdktrace.WithSyncer(exporter))
	originalProvider := otellib.GetTracerProvider()
	otellib.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(t.Context())
		otellib.SetTracerProvider(originalProvider)
	})
	reader := setupMeter(t)
	g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
		func(ctx context.Context, _ []string, _ *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			if trace.SpanFromContext(ctx).IsRecording() {
				t.Error("expected a non-recording span")
			}
			return &embedding.GeneratedOf[float32]{Usage: &message.UsageDetails{InputTokenCount: 10}}, nil
		}), otelprovider.EmbeddingGeneratorConfig{EnableSensitiveData: new(true)})
	_, err := g.Generate(t.Context(), nil, &embedding.Options{AdditionalProperties: map[string]any{"secret": "value"}})
	if err != nil || len(exporter.GetSpans()) != 0 {
		t.Fatalf("Generate error = %v, spans = %v", err, exporter.GetSpans())
	}
	for _, name := range []string{"gen_ai.client.operation.duration", "gen_ai.client.token.usage"} {
		if !hasMetric(collectMetrics(t, reader), name) {
			t.Errorf("missing metric %q without sampled spans", name)
		}
	}
}

func TestEmbeddingTelemetry_TracesWithoutMetrics(t *testing.T) {
	exporter := setupTracer(t)
	originalProvider := otellib.GetMeterProvider()
	otellib.SetMeterProvider(metricnoop.NewMeterProvider())
	t.Cleanup(func() { otellib.SetMeterProvider(originalProvider) })
	g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
		func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			return &embedding.GeneratedOf[float32]{Usage: &message.UsageDetails{InputTokenCount: 10}}, nil
		}), otelprovider.EmbeddingGeneratorConfig{})
	if _, err := g.Generate(t.Context(), []string{"hello"}, nil); err != nil {
		t.Fatal(err)
	}
	_, attrs := singleEmbeddingSpan(t, exporter)
	if attrs["gen_ai.usage.input_tokens"].AsInt64() != 10 {
		t.Errorf("usage span attribute = %v", attrs)
	}
}

func TestEmbeddingTelemetry_ConcurrentRequestsDoNotLeakState(t *testing.T) {
	exporter := setupTracer(t)
	reader := setupMeter(t)
	entered := make(chan struct{}, 10)
	release := make(chan struct{})
	g := otelprovider.WrapEmbeddingGenerator(embeddingGeneratorFunc[string, *embedding.Of[float32]](
		func(_ context.Context, values []string, _ *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			entered <- struct{}{}
			<-release
			return &embedding.GeneratedOf[float32]{
				Embeddings: []*embedding.Of[float32]{{ModelID: "served-" + values[0]}},
				Usage:      &message.UsageDetails{InputTokenCount: 1},
			}, nil
		}), otelprovider.EmbeddingGeneratorConfig{DefaultModelID: "default"})
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			model := fmt.Sprint(i)
			_, err := g.Generate(t.Context(), []string{model}, &embedding.Options{ModelID: &model})
			if err != nil {
				t.Error(err)
			}
		})
	}
	for range 10 {
		<-entered
	}
	close(release)
	wg.Wait()
	spans := exporter.GetSpans()
	if len(spans) != 10 {
		t.Fatalf("spans = %d, want 10", len(spans))
	}
	seen := map[string]bool{}
	for _, span := range spans {
		attrs := map[string]attribute.Value{}
		for _, attr := range span.Attributes {
			attrs[string(attr.Key)] = attr.Value
		}
		requestModel := attrs["gen_ai.request.model"].AsString()
		seen[requestModel] = true
		if span.Name != "embeddings "+requestModel || attrs["gen_ai.response.model"].AsString() != "served-"+requestModel {
			t.Errorf("request metadata leaked: %+v", attrs)
		}
	}
	if len(seen) != 10 {
		t.Errorf("unique request models = %d", len(seen))
	}
	usage := collectMetric(t, reader, "gen_ai.client.token.usage").(metricdata.Histogram[int64])
	var total int64
	for _, point := range usage.DataPoints {
		total += point.Sum
	}
	if total != 10 {
		t.Errorf("input token total = %d, want 10", total)
	}
}

func TestEmbeddingTelemetry_RequiresGenerator(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected nil generator panic")
		}
	}()
	otelprovider.WrapEmbeddingGenerator[string, *embedding.Of[float32]](nil, otelprovider.EmbeddingGeneratorConfig{})
}

type closableEmbeddingGenerator[I, E any] struct {
	embedding.Generator[I, E]
	close func() error
}

func (g *closableEmbeddingGenerator[I, E]) Close() error { return g.close() }

func TestEmbeddingTelemetry_DoesNotAddCloser(t *testing.T) {
	inner := embeddingGeneratorFunc[string, *embedding.Of[float32]](
		func(context.Context, []string, *embedding.Options) (*embedding.GeneratedOf[float32], error) {
			return &embedding.GeneratedOf[float32]{}, nil
		})
	g := otelprovider.WrapEmbeddingGenerator(inner, otelprovider.EmbeddingGeneratorConfig{})
	if _, ok := g.(io.Closer); ok {
		t.Fatal("wrapper must not implement io.Closer when the inner generator does not")
	}
}

func TestEmbeddingTelemetry_ForwardsOptionalClose(t *testing.T) {
	generateErr := errors.New("generation failed")
	closeErr := errors.New("close failed")
	for _, test := range []struct {
		name        string
		generateErr error
		closeErr    error
	}{
		{"success", nil, nil},
		{"close error", nil, closeErr},
		{"generation error", generateErr, closeErr},
	} {
		for _, layers := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/layers=%d", test.name, layers), func(t *testing.T) {
				exporter := setupTracer(t)
				type output struct{ Value int }
				want := &embedding.Generated[output]{Embeddings: []output{{Value: 42}}}
				calls := 0
				inner := &closableEmbeddingGenerator[string, output]{
					Generator: embeddingGeneratorFunc[string, output](
						func(context.Context, []string, *embedding.Options) (*embedding.Generated[output], error) {
							return want, test.generateErr
						}),
					close: func() error {
						calls++
						return test.closeErr
					},
				}
				var g embedding.Generator[string, output] = inner
				for range layers {
					g = otelprovider.WrapEmbeddingGenerator(g, otelprovider.EmbeddingGeneratorConfig{})
				}
				closer, ok := g.(io.Closer)
				if !ok {
					t.Fatal("wrapper must preserve the inner generator's io.Closer capability")
				}
				result, err := g.Generate(t.Context(), []string{"hello"}, nil)
				if result != want || err != test.generateErr {
					t.Fatalf("Generate = %+v, %v; want original result and error", result, err)
				}
				if calls != 0 {
					t.Fatal("generating must not close the inner generator")
				}
				if len(exporter.GetSpans()) != layers {
					t.Fatalf("spans = %d, want %d", len(exporter.GetSpans()), layers)
				}
				for i := 1; i <= 2; i++ {
					if err := closer.Close(); err != test.closeErr || calls != i {
						t.Fatalf("Close = %v, calls = %d; want %v, %d", err, calls, test.closeErr, i)
					}
				}
				other := otelprovider.WrapEmbeddingGenerator(
					embeddingGeneratorFunc[string, output](
						func(context.Context, []string, *embedding.Options) (*embedding.Generated[output], error) {
							return want, nil
						}),
					otelprovider.EmbeddingGeneratorConfig{})
				if _, err := other.Generate(t.Context(), []string{"hello"}, nil); err != nil {
					t.Fatal(err)
				}
				if len(exporter.GetSpans()) != layers+1 {
					t.Fatal("closing a generator must not shut down the shared telemetry provider")
				}
			})
		}
	}
}
