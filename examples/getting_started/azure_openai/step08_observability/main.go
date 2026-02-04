// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use a simple Agent with Azure OpenAI as the backend that logs telemetry using OpenTelemetry.

package main

import (
	"context"
	"log"
	"os"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/agent/middleware/otel"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	otellib "go.opentelemetry.io/otel"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiKey = os.Getenv("AZURE_OPENAI_API_KEY")

var logger = demo.NewLogger(
	"OpenTelemetry Observability",
	"Demonstrates how to use OpenTelemetry for observability in the agent framework.",
	"Model", deployment,
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

	// Create Azure OpenAI agent, and enable OpenTelemetry instrumentation.
	a := openai.NewChatAgentAzure(openai.ClientConfig{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
	}, chatagent.Config{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
		RunOptions: []agentopt.RunOption{
			middleware.With(otel.New(otel.Config{})), // for OpenTelemetry observability
			middleware.With(logger),                  // for logging agent interactions
		},
	})

	ctx := context.Background()

	// Invoke the agent and output the text result.
	resp, err := a.RunText("Tell me a joke about a pirate.").Collect(ctx)
	demo.Response(resp, err)

	// Invoke the agent with streaming support.
	for update, err := range a.RunText("Tell me a joke about a pirate.").All(ctx) {
		demo.Response(update, err)
	}
}
