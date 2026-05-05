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

	// Resolve the agent card.
	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		demo.Panicf("failed to resolve agent card: %v", err)
	}

	// Insecure connection is used for example purposes.
	withInsecureGRPC := a2agrpc.WithGRPCTransport(grpc.WithTransportCredentials(insecure.NewCredentials()))

	// Create a client connected to one of the interfaces specified in the agent card.
	client, err := a2aclient.NewFromCard(ctx, card, withInsecureGRPC)
	if err != nil {
		demo.Panicf("failed to create A2A client: %v", err)
	}

	// Create A2A agent.
	a := a2aagent.New(
		client,
		a2aagent.Config{
			Config: agent.Config{
				Name:        "Joker",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
