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
	"A2A Agent Stream Reconnection",
	"Starts a streamed agent run, captures the continuation token, and resumes the stream after an interruption.",
	"Agent", cardURL,
)

func main() {
	ctx := context.Background()

	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		demo.Panicf("failed to resolve agent card: %v", err)
	}

	remoteAgent, err := a2aprovider.NewAgentFromCard(
		ctx, card,
		a2aprovider.AgentConfig{
			Config: agent.Config{
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	if err != nil {
		demo.Panicf("failed to create A2A agent: %v", err)
	}

	session, err := remoteAgent.CreateSession(ctx)
	if err != nil {
		demo.Panicf("failed to create agent session: %v", err)
	}

	query := "Conduct a comprehensive analysis of quantum computing applications in cryptography, including recent breakthroughs, implementation challenges, and future roadmap. Please include diagrams and visual representations to illustrate complex concepts."

	var continuationToken, continuationContextID string
	for update, err := range remoteAgent.RunText(ctx, query, agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			demo.Panic(err)
		}
		if update != nil {
			if update.ContinuationToken != "" {
				continuationToken = update.ContinuationToken
			}
			if update.ConversationID != nil {
				continuationContextID = *update.ConversationID
			}
		}
		break
	}

	if continuationToken == "" {
		demo.Assistant("The first update did not include a continuation token. Stream reconnection is not applicable.")
		return
	}

	demo.Assistantf("Reconnecting to task %s...", continuationToken)
	options := []agent.Option{
		agent.WithSession(session),
		agent.WithContinuationToken(continuationToken),
		agent.Stream(true),
	}
	if continuationContextID != "" {
		options = append(options, agent.WithServiceID(continuationContextID))
	}
	for update, err := range remoteAgent.Run(
		ctx,
		nil,
		options...,
	) {
		if err != nil {
			demo.Panic(err)
		}
		if update != nil && update.String() != "" {
			demo.Response(update, nil)
		}
	}
}
