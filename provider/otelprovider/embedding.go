// Copyright (c) Microsoft. All rights reserved.

package otelprovider

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/internal/telemetry"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
)

// EmbeddingGeneratorConfig configures embedding generation telemetry.
type EmbeddingGeneratorConfig struct {
	// SourceName is the tracing and metrics instrumentation scope. Empty uses
	// "github.com/microsoft/agent-framework-go".
	SourceName string
	// ProviderName identifies the embedding service. Empty omits the attribute.
	ProviderName string
	// ServerAddress and ServerPort identify the service endpoint. When the
	// address is empty, both attributes are omitted. Neither affects routing.
	ServerAddress string
	ServerPort    int
	// DefaultModelID is used when a request does not specify a model.
	// Empty means unknown. It does not configure the underlying generator.
	DefaultModelID string
	// DefaultModelDimensions is used when a request does not specify dimensions.
	// Nil means unknown; explicit zero is preserved.
	DefaultModelDimensions *int
	// EnableSensitiveData permits additional request and response properties
	// on spans. Supported attribute types retain their values; other types are
	// formatted as strings, and nil entries are omitted. Raw inputs and vectors
	// are never captured. Nil uses
	// OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT, enabled only by
	// "true" (case-insensitive), read when the wrapper is constructed.
	EnableSensitiveData *bool
	// Logger receives operation failures as warning logs. Nil disables logs.
	Logger *slog.Logger
}

// WrapEmbeddingGenerator wraps inner with OpenTelemetry GenAI v1.41 client spans
// and duration and input-token histograms. The conventions are experimental.
// It panics if inner is nil. Requests, results, and errors are forwarded unchanged.
//
// Request options override the configured model and dimension defaults.
// Response-model telemetry uses only the first result, when it is an
// [embedding.Embedding] value or pointer using [embedding.Vector] or a built-in
// JSON vector type. Other output types still receive usage and duration telemetry
// but do not supply response-model tags.
// A non-nil result Usage reports InputTokenCount, including an explicit zero.
//
// The returned generator implements [io.Closer] if and only if inner does.
// Close calls are forwarded unchanged, including errors and repeated calls.
// Generation does not close inner, and the wrapper never shuts down the global
// telemetry providers. Callers must not close a generator while it is in use.
func WrapEmbeddingGenerator[TInput, TEmbedding any](inner embedding.Generator[TInput, TEmbedding], cfg EmbeddingGeneratorConfig) embedding.Generator[TInput, TEmbedding] {
	if inner == nil {
		panic("otelprovider: embedding generator is required")
	}
	if cfg.DefaultModelDimensions != nil {
		cfg.DefaultModelDimensions = new(*cfg.DefaultModelDimensions)
	}
	enableSensitiveData := strings.EqualFold(os.Getenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"), "true")
	if cfg.EnableSensitiveData != nil {
		enableSensitiveData = *cfg.EnableSensitiveData
	}
	name := cmp.Or(cfg.SourceName, "github.com/microsoft/agent-framework-go")
	operationDuration, tokenUsage := newClientMetrics(otel.Meter(name))
	g := &embeddingGenerator[TInput, TEmbedding]{
		inner:               inner,
		cfg:                 cfg,
		tracer:              otel.Tracer(name),
		operationDuration:   operationDuration,
		tokenUsage:          tokenUsage,
		enableSensitiveData: enableSensitiveData,
	}
	if closer, ok := inner.(io.Closer); ok {
		return &struct {
			*embeddingGenerator[TInput, TEmbedding]
			io.Closer
		}{g, closer}
	}
	return g
}

type embeddingGenerator[TInput, TEmbedding any] struct {
	inner               embedding.Generator[TInput, TEmbedding]
	cfg                 EmbeddingGeneratorConfig
	tracer              trace.Tracer
	operationDuration   genaiconv.ClientOperationDuration
	tokenUsage          genaiconv.ClientTokenUsage
	enableSensitiveData bool
}

func (g *embeddingGenerator[TInput, TEmbedding]) Generate(ctx context.Context, values []TInput, options *embedding.Options) (*embedding.Generated[TEmbedding], error) {
	modelID := g.cfg.DefaultModelID
	hasModelID := modelID != ""
	dimensions := g.cfg.DefaultModelDimensions
	if options != nil {
		if options.ModelID != nil {
			modelID, hasModelID = *options.ModelID, true
		}
		if options.Dimensions != nil {
			dimensions = options.Dimensions
		}
	}
	name := string(genaiconv.OperationNameEmbeddings)
	if strings.TrimSpace(modelID) != "" {
		name += " " + modelID
	}
	attrs := []attribute.KeyValue{semconv.GenAIOperationNameEmbeddings}
	if hasModelID {
		attrs = append(attrs, semconv.GenAIRequestModel(modelID))
	}
	if g.cfg.ProviderName != "" {
		attrs = append(attrs, semconv.GenAIProviderNameKey.String(g.cfg.ProviderName))
	}
	if g.cfg.ServerAddress != "" {
		attrs = append(attrs, semconv.ServerAddress(g.cfg.ServerAddress), semconv.ServerPort(g.cfg.ServerPort))
	}
	ctx, span := g.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	defer span.End()
	if dimensions != nil {
		span.SetAttributes(semconv.GenAIEmbeddingsDimensionCount(*dimensions))
	}
	if g.enableSensitiveData && options != nil {
		setEmbeddingProperties(span, options.AdditionalProperties)
	}
	var start time.Time
	if g.operationDuration.Enabled(ctx) {
		start = time.Now()
	}
	result, err := g.inner.Generate(ctx, values, options)

	var responseModelID string
	if err == nil && result != nil && len(result.Embeddings) > 0 {
		first := any(result.Embeddings[0])
		responseModelID = cmp.Or(
			embeddingResponseModel[embedding.Vector](first),
			embeddingResponseModel[embedding.VectorOf[uint8]](first),
			embeddingResponseModel[embedding.VectorOf[int8]](first),
			embeddingResponseModel[embedding.VectorOf[float32]](first),
			embeddingResponseModel[embedding.VectorOf[float64]](first),
			embeddingResponseModel[embedding.Binary](first),
		)
	}
	if responseModelID != "" {
		attrs = append(attrs, semconv.GenAIResponseModel(responseModelID))
		span.SetAttributes(semconv.GenAIResponseModel(responseModelID))
	}
	if !start.IsZero() {
		durationAttrs := attrs
		if err != nil {
			durationAttrs = append(durationAttrs, semconv.ErrorTypeKey.String(telemetry.ErrorTypeName(err)))
		}
		g.operationDuration.RecordSet(ctx, time.Since(start).Seconds(), attribute.NewSet(durationAttrs...))
	}
	if err != nil {
		span.SetAttributes(semconv.ErrorTypeKey.String(telemetry.ErrorTypeName(err)))
		span.SetStatus(codes.Error, err.Error())
		if g.cfg.Logger != nil {
			g.cfg.Logger.WarnContext(ctx, "gen_ai.client.operation.exception", slog.Any("error", err))
		}
	}
	if err == nil && result != nil {
		if result.Usage != nil {
			inputTokens := result.Usage.InputTokenCount
			span.SetAttributes(semconv.GenAIUsageInputTokensKey.Int64(inputTokens))
			attrs = append(attrs, semconv.GenAITokenTypeInput)
			g.tokenUsage.RecordSet(ctx, inputTokens, attribute.NewSet(attrs...))
		}
		if g.enableSensitiveData {
			setEmbeddingProperties(span, result.AdditionalProperties)
		}
	}
	return result, err
}

func embeddingResponseModel[TVector embedding.Vector](value any) string {
	switch e := value.(type) {
	case *embedding.Embedding[TVector]:
		if e != nil {
			return e.ModelID
		}
	case embedding.Embedding[TVector]:
		return e.ModelID
	}
	return ""
}

func setEmbeddingProperties(span trace.Span, properties map[string]any) {
	if !span.IsRecording() {
		return
	}
	for key, value := range properties {
		var attr attribute.KeyValue
		switch value := value.(type) {
		case nil:
			continue
		case string:
			attr = attribute.String(key, value)
		case bool:
			attr = attribute.Bool(key, value)
		case int:
			attr = attribute.Int(key, value)
		case int8:
			attr = attribute.Int64(key, int64(value))
		case int16:
			attr = attribute.Int64(key, int64(value))
		case int32:
			attr = attribute.Int64(key, int64(value))
		case int64:
			attr = attribute.Int64(key, value)
		case uint8:
			attr = attribute.Int64(key, int64(value))
		case uint16:
			attr = attribute.Int64(key, int64(value))
		case uint32:
			attr = attribute.Int64(key, int64(value))
		case float32:
			attr = attribute.Float64(key, float64(value))
		case float64:
			attr = attribute.Float64(key, value)
		case []string:
			attr = attribute.StringSlice(key, value)
		case []bool:
			attr = attribute.BoolSlice(key, value)
		case []int:
			attr = attribute.IntSlice(key, value)
		case []int64:
			attr = attribute.Int64Slice(key, value)
		case []float64:
			attr = attribute.Float64Slice(key, value)
		case attribute.Value:
			attr = attribute.KeyValue{Key: attribute.Key(key), Value: value}
		default:
			attr = attribute.String(key, fmt.Sprint(value))
		}
		span.SetAttributes(attr)
	}
}
