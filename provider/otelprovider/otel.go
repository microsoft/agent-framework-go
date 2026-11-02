// Copyright (c) Microsoft. All rights reserved.

package otelprovider

import (
	"cmp"
	"context"
	"iter"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/modelcall"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
)

const defaultSourceName = "github.com/microsoft/agent-framework-go"

// MiddlewareConfig holds configuration for the middleware.
type MiddlewareConfig struct {
	SourceName string

	// DisableModelTracing omits client spans and usage/duration metrics for
	// model-adapter calls, retaining agent-run and function-tool instrumentation.
	// By default, OpenAI, Foundry Responses, Anthropic, and Gemini adapter calls
	// are instrumented. Raw messages and tool arguments are not captured.
	DisableModelTracing bool
}

// NewMiddleware creates middleware that adds OpenTelemetry tracing and metrics
// to agent runs, function tools, and supported model-adapter calls.
// Set [MiddlewareConfig.DisableModelTracing] to omit model-call instrumentation.
//
// Tool tracing is supplied through [toolautocall.WithTracer] on the run context.
// An explicit [toolautocall.Config.Tracer] takes precedence over the injected tracer.
func NewMiddleware(cfg MiddlewareConfig) agent.Middleware {
	name := cmp.Or(cfg.SourceName, defaultSourceName)
	operationDuration, tokenUsage := newClientMetrics(otel.Meter(name))
	return &mw{
		tracer:              otel.Tracer(name),
		operationDuration:   operationDuration,
		tokenUsage:          tokenUsage,
		disableModelTracing: cfg.DisableModelTracing,
	}
}

func newClientMetrics(meter metric.Meter) (genaiconv.ClientOperationDuration, genaiconv.ClientTokenUsage) {
	operationDuration, err := genaiconv.NewClientOperationDuration(
		meter,
		metric.WithExplicitBucketBoundaries(0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92),
	)
	if err != nil {
		otel.Handle(err)
	}
	tokenUsage, err := genaiconv.NewClientTokenUsage(
		meter,
		metric.WithExplicitBucketBoundaries(1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864),
	)
	if err != nil {
		otel.Handle(err)
	}
	return operationDuration, tokenUsage
}

type mw struct {
	tracer              trace.Tracer
	operationDuration   genaiconv.ClientOperationDuration
	tokenUsage          genaiconv.ClientTokenUsage
	disableModelTracing bool
}

func (m *mw) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		a, _ := agent.AgentFromContext(ctx)
		start := time.Now()
		// GenAI conventions name the span "<operation> <target>", mirroring the sibling
		// execute_tool span. Fall back to the agent id when it has no
		// name so the span still carries a target, matching .NET/Python. Default the id to
		// "unknown" (as with the provider name below) so the span always carries a target
		// and gen_ai.agent.id is never an empty string, even when the middleware runs
		// without an agent in context.
		id := cmp.Or(a.ID(), "unknown")
		name := string(genaiconv.OperationNameInvokeAgent) + " " + cmp.Or(a.Name(), id)
		// Only include name/description when present: .NET and Python omit these attributes
		// for an unnamed/undescribed agent rather than emitting empty strings.
		attrs := []attribute.KeyValue{
			semconv.GenAIOperationNameInvokeAgent,
			semconv.GenAIProviderNameKey.String(cmp.Or(a.ProviderName(), "unknown")),
			semconv.GenAIAgentID(id),
		}
		if a.Name() != "" {
			attrs = append(attrs, semconv.GenAIAgentName(a.Name()))
		}
		if a.Description() != "" {
			attrs = append(attrs, semconv.GenAIAgentDescription(a.Description()))
		}
		ctx, span := m.tracer.Start(ctx, name, trace.WithTimestamp(start), trace.WithAttributes(attrs...))
		ctx = toolautocall.WithTracer(ctx, toolTracer{tracer: m.tracer})
		if !m.disableModelTracing {
			ctx = modelcall.WithDecorator(ctx, m.modelRun)
		}

		// Accumulated across the whole run. An agent that makes several LLM round-trips
		// emits one UsageContent per round-trip, so summing them gives the true cost of
		// the agent rather than the cost of its final call.
		var usage message.UsageDetails
		var errorType string
		var responseID string
		var finishReason string
		defer func() {
			end := time.Now()
			if responseID != "" {
				span.SetAttributes(semconv.GenAIResponseID(responseID))
			}
			if finishReason != "" {
				span.SetAttributes(semconv.GenAIResponseFinishReasons(finishReason))
			}
			setUsage(span, usage)
			m.recordOperationDuration(ctx, a, end.Sub(start), errorType)
			m.recordTokenUsage(ctx, a, usage)
			span.End(trace.WithTimestamp(end))
		}()

		for update, err := range next(ctx, messages, options...) {
			if err != nil {
				errorType = telemetry.ErrorTypeName(err)
				span.SetAttributes(semconv.ErrorTypeKey.String(errorType))
				span.RecordError(err, trace.WithTimestamp(time.Now()))
				span.SetStatus(codes.Error, err.Error())
			}
			if update != nil && update.ResponseID != "" {
				responseID = update.ResponseID
			}
			if update != nil && update.FinishReason != "" {
				finishReason = update.FinishReason
			}
			// update.Usage() sums this update's UsageContent (nil-safe), so accumulate
			// its total into the run rather than iterating Contents by hand.
			usage.Add(update.Usage())
			if !yield(update, err) {
				return
			}
		}
	}
}

func (m *mw) recordOperationDuration(ctx context.Context, a *agent.Agent, duration time.Duration, errorType string) {
	var attrs []attribute.KeyValue
	if errorType != "" {
		attrs = append(attrs, m.operationDuration.AttrErrorType(genaiconv.ErrorTypeAttr(errorType)))
	}
	m.operationDuration.Record(
		ctx,
		duration.Seconds(),
		genaiconv.OperationNameInvokeAgent,
		genaiconv.ProviderNameAttr(cmp.Or(a.ProviderName(), "unknown")),
		attrs...,
	)
}

func (m *mw) recordTokenUsage(ctx context.Context, a *agent.Agent, usage message.UsageDetails) {
	if !hasTokenUsage(usage) {
		return
	}

	providerName := genaiconv.ProviderNameAttr(cmp.Or(a.ProviderName(), "unknown"))
	m.tokenUsage.Record(ctx, usage.InputTokenCount, genaiconv.OperationNameInvokeAgent, providerName, genaiconv.TokenTypeInput)
	m.tokenUsage.Record(ctx, usage.OutputTokenCount, genaiconv.OperationNameInvokeAgent, providerName, genaiconv.TokenTypeOutput)
}

// setUsage records token counts on the span. Zero-valued optional counters are left
// off rather than written as 0: a provider that does not report cached or reasoning
// tokens should be distinguishable from one that reports none, and an attribute that
// is always present but always zero trains people to ignore it.
func setUsage(span trace.Span, usage message.UsageDetails) {
	if !hasUsage(usage) {
		return
	}

	span.SetAttributes(usageAttributes(usage)...)
}

func hasTokenUsage(usage message.UsageDetails) bool {
	return usage.InputTokenCount != 0 || usage.OutputTokenCount != 0
}

func hasUsage(usage message.UsageDetails) bool {
	return usage.InputTokenCount != 0 ||
		usage.OutputTokenCount != 0 ||
		usage.TotalTokenCount != 0 ||
		usage.CachedInputTokenCount != 0 ||
		usage.ReasoningTokenCount != 0 ||
		len(usage.AdditionalCounts) > 0
}
