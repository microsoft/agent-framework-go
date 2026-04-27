// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use a simple Agent with Azure OpenAI as the backend that logs telemetry using OpenTelemetry.

package main

import (
	"cmp"
	"context"
	"log"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/middleware/otel"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	otellib "go.opentelemetry.io/otel"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

var logger = demo.NewLogger(
	"OpenTelemetry Observability",
	"Demonstrates how to use OpenTelemetry for observability in the agent framework.",
	"Model", deployment,
)

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

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

	// Create Azure OpenAI agent with OpenTelemetry instrumentation.
	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions: "You are good at telling jokes.",
				Name:         "Joker",
				Middlewares: []agent.Middleware{
					otel.New(otel.Config{}), // for OpenTelemetry observability
					logger,                  // for logging agent interactions
				},
			},
		},
	)

	ctx := context.Background()

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)

	// Invoke the agent with streaming support.
	for update, err := range a.RunText(ctx, "Tell me a joke about a pirate.", agent.Stream(true)) {
		demo.Response(update, err)
	}
}
