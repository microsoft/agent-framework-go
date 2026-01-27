// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
)

var logger = demo.NewLogger(
	"Background Responses",
	"Demonstrates an agent using background responses to handle long-running tasks.",
	"Model", "gpt-4o-mini",
)

func main() {
	a := openai.NewResponsesAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, chatagent.Config{
		Middlewares: []middleware.Middleware{logger},
	})

	ctx := context.Background()

	session, err := a.NewSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Start the initial run.
	resp, err := agent.RunText(ctx, a,
		"Write a very long novel about otters in space.",
		agentopt.Session(session),
		agentopt.AllowBackgroundResponses(true))

	demo.Response(resp, err)

	// Poll until the response is complete.
	for resp.ContinuationToken != "" {
		// Wait before polling again.
		time.Sleep(2 * time.Second)

		// Continue with the token.
		resp, err = agent.Run(ctx, a, nil,
			agentopt.Session(session),
			agentopt.AllowBackgroundResponses(true),
			agentopt.ContinuationToken(resp.ContinuationToken),
		)
		demo.Response(resp, err)
	}
}
