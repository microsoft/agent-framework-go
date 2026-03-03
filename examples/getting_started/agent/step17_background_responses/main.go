// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openairesponses"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
)

var logger = demo.NewLogger(
	"Background Responses",
	"Demonstrates an agent using background responses to handle long-running tasks.",
	"Model", "gpt-4o-mini",
)

func main() {
	a := openairesponses.NewAgent(openairesponses.Config{
		Model: "gpt-4o-mini",
		Agent: agent.Config{
			Middlewares: []middleware.Middleware{logger},
		},
	})

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Start the initial run.
	resp, err := a.RunText(ctx, "Write a very long novel about otters in space.",
		agentopt.Session(session),
		agentopt.AllowBackgroundResponses(true)).Collect()

	demo.Response(resp, err)

	// Poll until the response is complete.
	for resp.ContinuationToken != "" {
		// Wait before polling again.
		time.Sleep(2 * time.Second)

		// Continue with the token.
		resp, err = a.Run(ctx, nil,
			agentopt.Session(session),
			agentopt.AllowBackgroundResponses(true),
			agentopt.ContinuationToken(resp.ContinuationToken),
		).Collect()
		demo.Response(resp, err)
	}
}
