// Copyright (c) Microsoft. All rights reserved.

package otel

import (
	"cmp"
	"context"
	"iter"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/message"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Config holds configuration for the middleware.
type Config struct {
	SourceName string
}

// New creates a new middleware that adds OpenTelemetry tracing to agent runs.
func New(cfg Config) middleware.Middleware {
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
)

type mw struct {
	tracer trace.Tracer
}

func (m *mw) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		a, _ := agent.AgentFromContext(ctx)
		ctx, span := m.tracer.Start(ctx, a.Name(), trace.WithAttributes(
			attribute.String(attrKeyOperationName, opInvoke),
			attribute.String(attrKeyProviderName, cmp.Or(a.ProviderName(), "unknown")),
			attribute.String(attrKeyAgentID, a.ID()),
			attribute.String(attrKeyAgentName, a.Name()),
			attribute.String(attrKeyAgentDesc, a.Description()),
		))
		defer span.End()

		for update, err := range next(ctx, messages, options...) {
			if err != nil {
				span.RecordError(err, trace.WithTimestamp(time.Now()))
				span.SetStatus(codes.Error, err.Error())
			}
			if !yield(update, err) {
				break
			}
		}
	}
}
