// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/a2aagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var cardURL = cmp.Or(os.Getenv("A2A_AGENT_HOST"), "http://127.0.0.1:9001")

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple agent run.",
)

func main() {
	ctx := context.Background()

	// Resolve an AgentCard
	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		demo.Panicf("Failed to resolve an AgentCard: %v", err)
	}

	// Insecure connection is used for example purposes
	withInsecureGRPC := a2agrpc.WithGRPCTransport(grpc.WithTransportCredentials(insecure.NewCredentials()))

	// Create a client connected to one of the interfaces specified in the AgentCard.
	client, err := a2aclient.NewFromCard(ctx, card, withInsecureGRPC)
	if err != nil {
		demo.Panicf("Failed to create a client: %v", err)
	}

	a := a2aagent.New(
		client,
		a2aagent.Config{
			Config: agent.Config{
				Instructions: "You are good at telling jokes.",
				Name:         "Joker",
				Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
