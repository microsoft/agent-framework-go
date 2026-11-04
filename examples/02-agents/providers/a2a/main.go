// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

var cardURL = cmp.Or(os.Getenv("A2A_AGENT_HOST"), "http://127.0.0.1:5000")

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple agent run.",
)

func main() {
	ctx := context.Background()

	a, err := a2aprovider.NewAgentFromResolver(
		ctx, agentcard.DefaultResolver, cardURL,
		a2aprovider.AgentConfig{
			Config: agent.Config{
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	if err != nil {
		demo.Panicf("failed to create A2A agent: %v", err)
	}

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
