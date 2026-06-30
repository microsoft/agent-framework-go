// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	workflowotel "github.com/microsoft/agent-framework-go/workflow/observability/opentelemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const sourceName = "Workflow.Sample.WorkflowAsAgent"

var logger = demo.NewLogger(
	"Observable Workflow as Agent",
	"This sample wraps a telemetry-enabled workflow as an agent.",
	"Model", demo.Deployment,
)

func main() {
	ctx := context.Background()
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		demo.Panic(err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = provider.Shutdown(ctx) }()
	otel.SetTracerProvider(provider)

	french := demo.NewAzureChatAgent("French", "Answer in French, concisely.", logger)
	english := demo.NewAzureChatAgent("English", "Answer in English, concisely.", logger)
	frenchBinding := agentworkflow.New(french, agentworkflow.Config{})
	englishBinding := agentworkflow.New(english, agentworkflow.Config{})
	wf, err := workflow.NewBuilder(frenchBinding).
		AddEdge(frenchBinding, englishBinding).
		WithOutputFrom(englishBinding).
		WithTelemetry(workflowotel.New(workflowotel.Config{SourceName: sourceName}), workflow.TelemetryOptions{EnableSensitiveData: true}).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	wfAgent, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{IncludeOutputsInResponse: true})
	if err != nil {
		demo.Panic(err)
	}
	session, err := wfAgent.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for update, err := range wfAgent.RunText(ctx, "Describe one benefit of observability.", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			demo.Panic(err)
		}
		if text := update.String(); text != "" {
			demo.Assistantf("%s", text)
		}
	}
}
