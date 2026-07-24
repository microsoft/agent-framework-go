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
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// MiddlewareConfig holds configuration for the middleware.
type MiddlewareConfig struct {
	SourceName string
}

// NewMiddleware creates a new middleware that adds OpenTelemetry tracing and metrics to
// agent runs.
func NewMiddleware(cfg MiddlewareConfig) agent.Middleware {
	name := cmp.Or(cfg.SourceName, "github.com/microsoft/agent-framework-go")
	m := &mw{
		tracer: otel.Tracer(name),
	}

	// Metrics complement the span: span attributes describe a single run, whereas these
	// histograms aggregate token spend and latency across runs -- the shape a spend or
	// latency dashboard needs, and what the Python/.NET references emit. A failure to
	// build a histogram must not disable tracing, so we drop only the failed instrument
	// and record() tolerates a nil histogram.
	meter := otel.Meter(name)
	if h, err := meter.Float64Histogram(
		metricTokenUsage,
		metric.WithUnit("{token}"),
		metric.WithDescription("Measures number of input and output tokens used."),
	); err == nil {
		m.tokenUsage = h
	}
	if h, err := meter.Float64Histogram(
		metricOperationDuration,
		metric.WithUnit("s"),
		metric.WithDescription("Duration of GenAI client operations."),
	); err == nil {
		m.operationDuration = h
	}
	return m
}

const (
	opInvoke = "invoke_agent"

	attrKeyProviderName  = "gen_ai.provider.name"
	attrKeyAgentID       = "gen_ai.agent.id"
	attrKeyAgentName     = "gen_ai.agent.name"
	attrKeyAgentDesc     = "gen_ai.agent.description"
	attrKeyOperationName = "gen_ai.operation.name"
	attrKeyErrorType     = "error.type"
	attrKeyTokenType     = "gen_ai.token.type"

	// Metric instrument names from the OpenTelemetry GenAI semantic conventions, matching
	// the histograms emitted by the Python and .NET SDKs so cross-language dashboards
	// aggregate Go runs alongside the rest.
	metricTokenUsage        = "gen_ai.client.token.usage"
	metricOperationDuration = "gen_ai.client.operation.duration"

	// Token usage, per the OpenTelemetry GenAI semantic conventions.
	//
	// Providers already emit message.UsageContent into the response stream (see
	// provider/anthropicprovider/agent.go), and this middleware already iterates that
	// stream -- so the numbers pass through here on their way to the caller and were
	// simply never recorded. Token count is cost, and cost is the main reason to trace
	// an agent at all.
	//
	// Names are the exact ids from the GenAI semantic-conventions registry. The registry
	// namespaces the cache/reasoning counters (they are not `<x>_tokens`) and defines no
	// total; do not infer these from the input/output shape.
	attrKeyUsageInputTokens     = "gen_ai.usage.input_tokens"
	attrKeyUsageOutputTokens    = "gen_ai.usage.output_tokens"
	attrKeyUsageCacheReadTokens = "gen_ai.usage.cache_read.input_tokens"
	attrKeyUsageReasoningTokens = "gen_ai.usage.reasoning.output_tokens"
)

type mw struct {
	tracer            trace.Tracer
	tokenUsage        metric.Float64Histogram
	operationDuration metric.Float64Histogram
}

func (m *mw) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		a, _ := agent.AgentFromContext(ctx)
		start := time.Now()
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
		var errorType string

		for update, err := range next(ctx, messages, options...) {
			if err != nil {
				errorType = otelx.ErrorTypeName(err)
				span.SetAttributes(attribute.String(attrKeyErrorType, errorType))
				span.RecordError(err, trace.WithTimestamp(time.Now()))
				span.SetStatus(codes.Error, err.Error())
			}
			// update.Usage() sums this update's UsageContent (nil-safe), so accumulate
			// its total into the run rather than iterating Contents by hand.
			usage.Add(update.Usage())
			if !yield(update, err) {
				// Set what we have before bailing. A caller that stops reading early
				// still ran (and paid for) the tokens counted so far, and a span that
				// silently reports none of them understates real spend.
				setUsage(span, usage)
				m.recordMetrics(ctx, a, usage, start, errorType)
				return
			}
		}

		setUsage(span, usage)
		m.recordMetrics(ctx, a, usage, start, errorType)
	}
}

// recordMetrics emits the operation-duration and token-usage histograms for a completed
// run. Both span and metric are recorded from the same accumulated totals so a dashboard
// aggregating across runs sees the same numbers the span carries for a single run. It is a
// no-op when the histograms failed to build (see NewMiddleware), keeping the middleware
// usable for tracing alone.
func (m *mw) recordMetrics(ctx context.Context, a *agent.Agent, usage message.UsageDetails, start time.Time, errorType string) {
	// Attributes shared by every data point, mirroring the span's identifying attributes
	// plus error.type when the run faulted.
	attrs := []attribute.KeyValue{
		attribute.String(attrKeyOperationName, opInvoke),
		attribute.String(attrKeyProviderName, cmp.Or(a.ProviderName(), "unknown")),
		attribute.String(attrKeyAgentName, a.Name()),
	}
	if errorType != "" {
		attrs = append(attrs, attribute.String(attrKeyErrorType, errorType))
	}

	if m.operationDuration != nil {
		m.operationDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
	}

	if m.tokenUsage != nil {
		// One data point per token direction, each tagged gen_ai.token.type, so the same
		// histogram splits input from output spend the way the GenAI conventions define.
		// tokenAttrs builds a fresh slice per call so the two records never alias.
		tokenAttrs := func(tokenType string) []attribute.KeyValue {
			return append(attrs[:len(attrs):len(attrs)], attribute.String(attrKeyTokenType, tokenType))
		}
		m.tokenUsage.Record(ctx, float64(usage.InputTokenCount), metric.WithAttributes(tokenAttrs("input")...))
		m.tokenUsage.Record(ctx, float64(usage.OutputTokenCount), metric.WithAttributes(tokenAttrs("output")...))
	}
}

// setUsage records token counts on the span. Zero-valued optional counters are left
// off rather than written as 0: a provider that does not report cached or reasoning
// tokens should be distinguishable from one that reports none, and an attribute that
// is always present but always zero trains people to ignore it.
func setUsage(span trace.Span, usage message.UsageDetails) {
	// "Did we observe ANY usage?" -- and that must consider every counter, not just the
	// three required ones. Guarding on input/output/total alone would silently drop the
	// whole attribute set for a provider that reported only cached or reasoning tokens,
	// which is the exact silent-drop this function exists to avoid.
	if !hasUsage(usage) {
		return
	}

	// input + output are the only usage-token attributes the registry defines (no
	// total); cache_read is a subset of input and reasoning a subset of output, so
	// consumers sum rather than reading a provider-side total.
	attrs := []attribute.KeyValue{
		attribute.Int64(attrKeyUsageInputTokens, usage.InputTokenCount),
		attribute.Int64(attrKeyUsageOutputTokens, usage.OutputTokenCount),
	}
	if usage.CachedInputTokenCount > 0 {
		attrs = append(attrs, attribute.Int64(attrKeyUsageCacheReadTokens, usage.CachedInputTokenCount))
	}
	if usage.ReasoningTokenCount > 0 {
		attrs = append(attrs, attribute.Int64(attrKeyUsageReasoningTokens, usage.ReasoningTokenCount))
	}
	span.SetAttributes(attrs...)
}

// hasUsage reports whether any counter was populated. UsageDetails carries a map
// (AdditionalCounts) so it is not comparable with ==; the fields are checked directly.
func hasUsage(u message.UsageDetails) bool {
	return u.InputTokenCount != 0 ||
		u.OutputTokenCount != 0 ||
		u.TotalTokenCount != 0 ||
		u.CachedInputTokenCount != 0 ||
		u.ReasoningTokenCount != 0 ||
		len(u.AdditionalCounts) > 0
}
