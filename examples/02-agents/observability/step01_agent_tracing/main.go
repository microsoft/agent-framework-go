// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var logger = demo.NewLogger(
	"Agent OpenTelemetry",
	"This sample shows how to trace an agent run with OpenTelemetry. The otelprovider "+
		"middleware emits a gen_ai invoke_agent span (model, usage, finish reason) that "+
		"is exported here to stdout.",
	"Model", demo.FoundryModel,
)

func main() {
	// Set up an OpenTelemetry tracer that prints spans to stdout, and install it
	// as the global tracer provider so the otel middleware records to it.
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		demo.Panic(err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	otel.SetTracerProvider(tp)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	token := demo.FoundryTokenCredential()

	// The otelprovider middleware wraps the agent run in a gen_ai span.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name: "Joker",
				Middlewares: []agent.Middleware{
					otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{}),
					logger, // for logging agent interactions
				},
			},
		},
	)

	ctx := context.Background()
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
