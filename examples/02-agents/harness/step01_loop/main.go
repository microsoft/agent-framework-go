// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Loop Harness",
	"This sample shows how the loop harness keeps re-invoking an agent until it "+
		"signals completion with a marker, so the agent can work across several turns "+
		"on its own before returning.",
	"Model", demo.FoundryModel,
)

// completionMarker is the token the agent is asked to emit once the task is
// fully done; the loop stops as soon as it appears in a response.
const completionMarker = "TASK COMPLETE"

func main() {
	token := demo.FoundryTokenCredential()

	// Wrap the agent with the loop harness: after each iteration the
	// completion-marker evaluator decides whether to run the agent again.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a diligent assistant. Work on the request step by step across " +
				"multiple turns. End your final message with the marker '" + completionMarker +
				"' once, and only once, the request is fully complete.",
			Config: agent.Config{
				Name: "Worker",
				Middlewares: []agent.Middleware{
					// The loop re-invokes the agent (and everything below it,
					// including the logger) until an evaluator stops the loop.
					loop.New(loop.Config{
						Evaluators: []loop.Evaluator{
							loop.NewCompletionMarkerEvaluator(loop.CompletionMarkerConfig{Marker: completionMarker}),
						},
					}),
					logger, // for logging each agent iteration
				},
			},
		},
	)

	ctx := context.Background()
	resp, err := a.RunText(ctx, "Draft a short checklist for planning a small birthday party, "+
		"then review it once for anything missing, and finish.").Collect()
	demo.Response(resp, err)
}
