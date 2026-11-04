// Copyright (c) Microsoft. All rights reserved.

package otelprovider

import (
	"cmp"
	"context"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/modelcall"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
)

func (m *mw) modelRun(run agent.RunFunc, metadata *modelcall.Metadata) agent.RunFunc {
	return func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			model := metadata.RequestModelID
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
			callCtx, span := m.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithTimestamp(start), trace.WithAttributes(attrs...))
			streaming, _ := agent.GetOption(options, agent.Stream)
			var usage message.UsageDetails
			var hasUsage bool
			var finishReason string
			var runErr error
			var providerFailure *message.ErrorContent
			var stopped bool
			defer func() {
				// A complete non-streaming response remains successful if its
				// consumer cancels and stops after receiving it.
				if runErr == nil && providerFailure == nil && stopped && streaming {
					runErr = callCtx.Err()
				}
				var errorType string
				if runErr != nil {
					errorType = telemetry.ErrorTypeName(runErr)
					span.RecordError(runErr)
					span.SetStatus(codes.Error, runErr.Error())
				} else if providerFailure != nil {
					errorType = cmp.Or(providerFailure.ErrorCode, "provider_error")
					span.SetStatus(codes.Error, providerFailure.Message)
				}
				if errorType != "" {
					attr := semconv.ErrorTypeKey.String(errorType)
					span.SetAttributes(attr)
					attrs = append(attrs, attr)
				}
				if metadata.ResponseModelID != "" {
					span.SetAttributes(semconv.GenAIResponseModel(metadata.ResponseModelID))
					attrs = append(attrs, semconv.GenAIResponseModel(metadata.ResponseModelID))
				}
				if metadata.ResponseID != "" {
					span.SetAttributes(semconv.GenAIResponseID(metadata.ResponseID))
				}
				if finishReason != "" {
					span.SetAttributes(semconv.GenAIResponseFinishReasons(finishReason))
				}
				end := time.Now()
				m.operationDuration.RecordSet(callCtx, end.Sub(start).Seconds(), attribute.NewSet(attrs...))
				if hasUsage {
					span.SetAttributes(usageAttributes(usage)...)
					m.tokenUsage.RecordSet(callCtx, usage.InputTokenCount, attribute.NewSet(append(slices.Clone(attrs), semconv.GenAITokenTypeInput)...))
					m.tokenUsage.RecordSet(callCtx, usage.OutputTokenCount, attribute.NewSet(append(slices.Clone(attrs), semconv.GenAITokenTypeOutput)...))
				}
				span.End(trace.WithTimestamp(end))
			}()
			for update, err := range run(callCtx, messages, options...) {
				if update != nil {
					if update.ResponseID != "" {
						metadata.ResponseID = update.ResponseID
					}
					if update.FinishReason != "" {
						finishReason = update.FinishReason
					}
					for _, content := range update.Contents {
						switch value := content.(type) {
						case *message.UsageContent:
							hasUsage = true
							usage.Add(value.Details)
						case *message.ErrorContent:
							if providerFailure == nil {
								providerFailure = value
							}
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

func usageAttributes(usage message.UsageDetails) []attribute.KeyValue {
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
