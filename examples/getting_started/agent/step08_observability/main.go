// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use a simple Agent with OpenAI as the backend that logs telemetry using OpenTelemetry.

package main

import (
	"context"
	"log"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/middleware/otel"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	otellib "go.opentelemetry.io/otel"
)

var logger = demo.NewLogger(
	"OpenTelemetry Observability",
	"Demonstrates how to use OpenTelemetry for observability in the agent framework.",
	"Model", "gpt-4o-mini",
)

func main() {
	// Create TracerProvider with console exporter.
	// This will output the telemetry data to the console.
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatalf("failed to create stdout exporter: %v", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
	)
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("failed to shutdown tracer provider: %v", err)
		}
	}()
	otellib.SetTracerProvider(tp)

	// Create the agent, and enable OpenTelemetry instrumentation.
	a := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-4o-mini",
		Agent: agent.Config{
			Instructions: "You are good at telling jokes.",
			Name:         "Joker",
			Middlewares: []middleware.Middleware{
				otel.New(otel.Config{}), // for OpenTelemetry observability
				logger,                  // for logging agent interactions
			},
		},
	})

	ctx := context.Background()

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)

	// Invoke the agent with streaming support.
	for update, err := range a.RunText(ctx, "Tell me a joke about a pirate.", agentopt.Stream(true)) {
		demo.Response(update, err)
	}
}
