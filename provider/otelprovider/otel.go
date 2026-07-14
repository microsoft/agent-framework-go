// Copyright (c) Microsoft. All rights reserved.

package otelprovider

import (
	"cmp"
	"context"
	"iter"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/otelx"
	"github.com/microsoft/agent-framework-go/message"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// MiddlewareConfig holds configuration for the middleware.
type MiddlewareConfig struct {
	SourceName string
}

// NewMiddleware creates a new middleware that adds OpenTelemetry tracing to agent runs.
func NewMiddleware(cfg MiddlewareConfig) agent.Middleware {
	tracer := otel.Tracer(cmp.Or(cfg.SourceName, "github.com/microsoft/agent-framework-go"))
	return &mw{
		tracer: tracer,
	}
}

const (
	opInvoke = "invoke_agent"

	attrKeyProviderName  = "gen_ai.provider.name"
	attrKeyAgentID       = "gen_ai.agent.id"
	attrKeyAgentName     = "gen_ai.agent.name"
	attrKeyAgentDesc     = "gen_ai.agent.description"
	attrKeyOperationName = "gen_ai.operation.name"
	attrKeyErrorType     = "error.type"

	// Token usage, per the OpenTelemetry GenAI semantic conventions.
	//
	// Providers already emit message.UsageContent into the response stream (see
	// provider/anthropicprovider/agent.go), and this middleware already iterates that
	// stream -- so the numbers pass through here on their way to the caller and were
	// simply never recorded. Token count is cost, and cost is the main reason to trace
	// an agent at all.
	attrKeyUsageInputTokens       = "gen_ai.usage.input_tokens"
	attrKeyUsageOutputTokens      = "gen_ai.usage.output_tokens"
	attrKeyUsageTotalTokens       = "gen_ai.usage.total_tokens"
	attrKeyUsageCachedInputTokens = "gen_ai.usage.cached_input_tokens"
	attrKeyUsageReasoningTokens   = "gen_ai.usage.reasoning_tokens"
)

type mw struct {
	tracer trace.Tracer
}

func (m *mw) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		a, _ := agent.AgentFromContext(ctx)
		ctx, span := m.tracer.Start(ctx, a.Name(), trace.WithAttributes(
			attribute.String(attrKeyOperationName, opInvoke),
			attribute.String(attrKeyProviderName, cmp.Or(a.ProviderName(), "unknown")),
			attribute.String(attrKeyAgentID, a.ID()),
			attribute.String(attrKeyAgentName, a.Name()),
			attribute.String(attrKeyAgentDesc, a.Description()),
		))
		ctx = otelx.WithTracer(ctx, m.tracer)
		defer span.End()

		// Accumulated across the whole run. An agent that makes several LLM round-trips
		// emits one UsageContent per round-trip, so summing them gives the true cost of
		// the agent rather than the cost of its final call.
		var usage message.UsageDetails

		for update, err := range next(ctx, messages, options...) {
			if err != nil {
				span.SetAttributes(attribute.String(attrKeyErrorType, otelx.ErrorTypeName(err)))
				span.RecordError(err, trace.WithTimestamp(time.Now()))
				span.SetStatus(codes.Error, err.Error())
			}
			if update != nil {
				for _, content := range update.Contents {
					if u, ok := content.(*message.UsageContent); ok {
						usage.Add(u.Details)
					}
				}
			}
			if !yield(update, err) {
				// Set what we have before bailing. A caller that stops reading early
				// still ran (and paid for) the tokens counted so far, and a span that
				// silently reports none of them understates real spend.
				setUsage(span, usage)
				return
			}
		}

		setUsage(span, usage)
	}
}

// setUsage records token counts on the span. Zero-valued optional counters are left
// off rather than written as 0: a provider that does not report cached or reasoning
// tokens should be distinguishable from one that reports none, and an attribute that
// is always present but always zero trains people to ignore it.
func setUsage(span trace.Span, usage message.UsageDetails) {
	if usage.InputTokenCount == 0 && usage.OutputTokenCount == 0 && usage.TotalTokenCount == 0 {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.Int64(attrKeyUsageInputTokens, usage.InputTokenCount),
		attribute.Int64(attrKeyUsageOutputTokens, usage.OutputTokenCount),
		attribute.Int64(attrKeyUsageTotalTokens, usage.TotalTokenCount),
	}
	if usage.CachedInputTokenCount > 0 {
		attrs = append(attrs, attribute.Int64(attrKeyUsageCachedInputTokens, usage.CachedInputTokenCount))
	}
	if usage.ReasoningTokenCount > 0 {
		attrs = append(attrs, attribute.Int64(attrKeyUsageReasoningTokens, usage.ReasoningTokenCount))
	}
	span.SetAttributes(attrs...)
}
