// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"os"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

var cardURL = cmp.Or(os.Getenv("A2A_AGENT_HOST"), "http://127.0.0.1:5000")

var logger = demo.NewLogger(
	"A2A Agent Polling For Task Completion",
	"Starts a background A2A task and polls it with continuation tokens until it finishes.",
	"Agent", cardURL,
)

func main() {
	ctx := context.Background()

	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		demo.Panicf("failed to resolve agent card: %v", err)
	}

	client, err := a2aclient.NewFromCard(ctx, card)
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

	session, err := remoteAgent.CreateSession(ctx)
	if err != nil {
		demo.Panicf("failed to create agent session: %v", err)
	}

	resp, err := remoteAgent.RunText(
		ctx,
		"Conduct a comprehensive analysis of quantum computing applications in cryptography, including recent breakthroughs, implementation challenges, and future roadmap. Please include diagrams and visual representations to illustrate complex concepts.",
		agent.WithSession(session),
		agent.AllowBackgroundResponses(true),
	).Collect()
	if err != nil {
		demo.Panic(err)
	}

	for resp.ContinuationToken != "" {
		demo.Assistantf("Task %s is still running. Polling again in 2 seconds...", resp.ContinuationToken)
		time.Sleep(2 * time.Second)

		resp, err = remoteAgent.Run(
			ctx,
			nil,
			agent.WithSession(session),
			agent.WithContinuationToken(resp.ContinuationToken),
		).Collect()
		if err != nil {
			demo.Panic(err)
		}
	}

	demo.Response(resp, nil)
}
