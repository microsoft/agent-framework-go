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
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/agenttool"
)

var (
	deployment = cmp.Or(os.Getenv("FOUNDRY_MODEL"), "gpt-5.4-mini")
	cardURL    = cmp.Or(os.Getenv("A2A_AGENT_HOST"), "http://127.0.0.1:5000")
)

var logger = demo.NewLogger(
	"A2A Agent As Function Tool",
	"Exposes the remote A2A agent as a function tool for a host agent.",
	"Model", deployment,
	"Agent", cardURL,
)

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		demo.Panicf("failed to resolve agent card: %v", err)
	}

	remoteAgent, err := a2aprovider.NewAgentFromCard(ctx, card, a2aprovider.AgentConfig{})
	if err != nil {
		demo.Panicf("failed to create A2A agent: %v", err)
	}

	hostAgent := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(deployment),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant. Use the available tools to answer the user's questions.",
			Config: agent.Config{
				Middlewares: []agent.Middleware{logger},
				Tools:       []tool.Tool{agenttool.New(remoteAgent, agenttool.Config{})},
			},
		},
	)

	resp, err := hostAgent.RunText(
		ctx,
		"What can you help me with?",
	).Collect()
	demo.Response(resp, err)
}
