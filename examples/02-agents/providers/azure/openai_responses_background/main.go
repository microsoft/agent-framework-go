// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates long-running agent operations using the OpenAI
// Responses API "background" option. A background run returns quickly with a
// continuation token instead of the final answer; the caller then polls with
// that token until the operation completes.
//
// Ported from the Python getting-started "background responses" sample.
package main

import (
	"context"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

var logger = demo.NewLogger(
	"Background Responses",
	"Starts a background Responses run and polls with the continuation token until it completes.",
	"Model", demo.Deployment,
)

func main() {
	ctx := context.Background()
	token := demo.AzureTokenCredential()

	client := openai.NewClient(
		option.WithBaseURL(demo.Endpoint),
		azure.WithTokenCredential(token),
	)

	researcher := openaiprovider.NewResponsesAgent(
		client,
		openaiprovider.AgentConfig{
			Model:        demo.Deployment,
			Instructions: "You are a helpful research assistant. Be concise.",
			Config: agent.Config{
				Name:        "Researcher",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	// Background runs are tied to a session so that follow-up polls target the
	// same operation.
	session, err := researcher.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Start a background run. It returns quickly with a continuation token
	// rather than the final answer. (If the model or endpoint does not support
	// background execution, the run simply completes inline with no token.)
	resp, err := researcher.RunText(ctx,
		"Briefly explain the theory of relativity in two sentences.",
		agent.WithSession(session),
		agent.AllowBackgroundResponses(true),
	).Collect()
	if err != nil {
		demo.Panic(err)
	}

	// Poll until the operation completes — i.e. until a run no longer returns a
	// continuation token. Continuation runs must not carry any messages, so use
	// Run with a nil message slice.
	for resp.ContinuationToken != "" {
		time.Sleep(2 * time.Second)
		resp, err = researcher.Run(ctx, nil,
			agent.WithSession(session),
			agent.WithContinuationToken(resp.ContinuationToken),
		).Collect()
		if err != nil {
			demo.Panic(err)
		}
	}

	// The final response holds the completed result.
	demo.Response(resp, nil)
}
