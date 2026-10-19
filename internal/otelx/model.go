// Copyright (c) Microsoft. All rights reserved.

package otelx

import (
	"context"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
)

type modelTracingKey struct{}

type modelTracing struct {
	duration      genaiconv.ClientOperationDuration
	tokenUsage    genaiconv.ClientTokenUsage
	responseModel string
	responseID    string
}

// WithModelTracing enables adapter-level model spans using the tracer already
// in ctx and metrics from the same instrumentation scope.
func WithModelTracing(ctx context.Context, duration genaiconv.ClientOperationDuration, tokens genaiconv.ClientTokenUsage) context.Context {
	return context.WithValue(ctx, modelTracingKey{}, &modelTracing{duration: duration, tokenUsage: tokens})
}

// WrapModelRun instruments each adapter invocation when model tracing is enabled.
// requestModel resolves the model from the adapter's configuration and options.
// Input messages, options, updates, and errors are forwarded unchanged.
func WrapModelRun(run agent.RunFunc, requestModel func([]agent.Option) string) agent.RunFunc {
	return func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		settings, enabled := ctx.Value(modelTracingKey{}).(*modelTracing)
		tracer, hasTracer := TracerFromContext(ctx)
		if !enabled || !hasTracer {
			return run(ctx, messages, options...)
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			observation := *settings
			observation.responseModel, observation.responseID = "", ""
			model := requestModel(options)
			name := string(genaiconv.OperationNameChat)
			attrs := []attribute.KeyValue{semconv.GenAIOperationNameChat}
			if model != "" {
				attrs = append(attrs, semconv.GenAIRequestModel(model))
				if strings.TrimSpace(model) != "" {
					name += " " + model
				}
			}
			if a, ok := agent.AgentFromContext(ctx); ok && a.ProviderName() != "" {
				attrs = append(attrs, semconv.GenAIProviderNameKey.String(a.ProviderName()))
			}
			start := time.Now()
			callCtx, span := tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithTimestamp(start), trace.WithAttributes(attrs...))
			callCtx = context.WithValue(callCtx, modelTracingKey{}, &observation)
			var usage message.UsageDetails
			var hasUsage bool
			var finishReason string
			var runErr error
			var stopped bool
			defer func() {
				// Late caller cancellation does not invalidate a completed response.
				if runErr == nil && stopped {
					runErr = callCtx.Err()
				}
				if runErr != nil {
					span.SetAttributes(semconv.ErrorTypeKey.String(ErrorTypeName(runErr)))
					span.RecordError(runErr)
					span.SetStatus(codes.Error, runErr.Error())
					attrs = append(attrs, semconv.ErrorTypeKey.String(ErrorTypeName(runErr)))
				}
				if observation.responseModel != "" {
					span.SetAttributes(semconv.GenAIResponseModel(observation.responseModel))
					attrs = append(attrs, semconv.GenAIResponseModel(observation.responseModel))
				}
				if observation.responseID != "" {
					span.SetAttributes(semconv.GenAIResponseID(observation.responseID))
				}
				if finishReason != "" {
					span.SetAttributes(semconv.GenAIResponseFinishReasons(finishReason))
				}
				end := time.Now()
				observation.duration.RecordSet(callCtx, end.Sub(start).Seconds(), attribute.NewSet(attrs...))
				if hasUsage {
					span.SetAttributes(UsageAttributes(usage)...)
					observation.tokenUsage.RecordSet(callCtx, usage.InputTokenCount, attribute.NewSet(append(slices.Clone(attrs), semconv.GenAITokenTypeInput)...))
					observation.tokenUsage.RecordSet(callCtx, usage.OutputTokenCount, attribute.NewSet(append(slices.Clone(attrs), semconv.GenAITokenTypeOutput)...))
				}
				span.End(trace.WithTimestamp(end))
			}()
			for update, err := range run(callCtx, messages, options...) {
				if update != nil {
					if update.ResponseID != "" {
						observation.responseID = update.ResponseID
					}
					if update.FinishReason != "" {
						finishReason = update.FinishReason
					}
					for _, content := range update.Contents {
						if value, ok := content.(*message.UsageContent); ok {
							hasUsage = true
							usage.Add(value.Details)
						}
					}
				}
				runErr = err
				stopped = !yield(update, err)
				if stopped || err != nil {
					return
				}
			}
		}
	}
}

// RecordModelResponse adds service-reported model and response identifiers to
// the current model call. Empty values do not clear earlier stream metadata.
func RecordModelResponse(ctx context.Context, model, responseID string) {
	if observation, ok := ctx.Value(modelTracingKey{}).(*modelTracing); ok {
		if model != "" {
			observation.responseModel = model
		}
		if responseID != "" {
			observation.responseID = responseID
		}
	}
}

// UsageAttributes converts reported usage to GenAI token attributes.
func UsageAttributes(usage message.UsageDetails) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.GenAIUsageInputTokensKey.Int64(usage.InputTokenCount),
		semconv.GenAIUsageOutputTokensKey.Int64(usage.OutputTokenCount),
	}
	if usage.CachedInputTokenCount > 0 {
		attrs = append(attrs, semconv.GenAIUsageCacheReadInputTokensKey.Int64(usage.CachedInputTokenCount))
	}
	if usage.ReasoningTokenCount > 0 {
		attrs = append(attrs, semconv.GenAIUsageReasoningOutputTokensKey.Int64(usage.ReasoningTokenCount))
	}
	return attrs
}
