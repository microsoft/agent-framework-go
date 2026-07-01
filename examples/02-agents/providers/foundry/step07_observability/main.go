// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"log"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var logger = demo.NewLogger(
	"Foundry Observability",
	"Demonstrates OpenTelemetry tracing for a Foundry agent.",
	"Model", demo.FoundryModel,
)

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatalf("failed to create stdout exporter: %v", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("failed to shutdown tracer provider: %v", err)
		}
	}()
	otel.SetTracerProvider(tp)

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
					logger,
				},
			},
		},
	)

	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)

	for update, err := range a.RunText(ctx, "Tell me a joke about a pirate.", agent.Stream(true)) {
		demo.Response(update, err)
	}
}
