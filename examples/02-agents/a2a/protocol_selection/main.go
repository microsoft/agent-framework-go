// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

var cardURL = cmp.Or(os.Getenv("A2A_AGENT_HOST"), "http://127.0.0.1:5000")

// Change this to a2a.TransportProtocolJSONRPC to prefer JSON-RPC instead.
var preferredTransport = a2a.TransportProtocolHTTPJSON

var logger = demo.NewLogger(
	"A2A Agent Protocol Selection",
	"Creates an A2A client with an explicit preferred transport binding.",
	"Agent", cardURL,
	"Transport", string(preferredTransport),
)

func main() {
	ctx := context.Background()

	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		demo.Panicf("failed to resolve agent card: %v", err)
	}

	client, err := a2aclient.NewFromCard(
		ctx,
		card,
		a2aclient.WithConfig(a2aclient.Config{
			PreferredTransports: []a2a.TransportProtocol{preferredTransport},
		}),
	)
	if err != nil {
		demo.Panicf("failed to create A2A client: %v", err)
	}

	remoteAgent := a2aprovider.NewAgent(
		client,
		a2aprovider.AgentConfig{
			Config: agent.Config{
				Name:        cmp.Or(card.Name, "RemoteA2AAgent"),
				Description: card.Description,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	resp, err := remoteAgent.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
