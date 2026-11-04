// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bufio"
	"cmp"
	"context"
	"os"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

var agentURL = cmp.Or(os.Getenv("A2A_AGENT_URL"), "http://localhost:5000/")

func main() {
	ctx := context.Background()

	logger := demo.NewLogger(
		"A2A Client",
		"Discovers and invokes the remote policy agent over A2A.",
		"Agent", agentURL,
	)

	policyAgent, err := a2aprovider.NewAgentFromResolver(
		ctx, agentcard.DefaultResolver, agentURL,
		a2aprovider.AgentConfig{
			Config: agent.Config{
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	if err != nil {
		demo.Panicf("failed to create A2A policy agent: %v", err)
	}

	session, err := policyAgent.CreateSession(ctx)
	if err != nil {
		demo.Panicf("failed to create agent session: %v", err)
	}

	reader := bufio.NewScanner(os.Stdin)
	for {
		_, _ = os.Stdout.WriteString("\nUser (:q or quit to exit): ")
		if !reader.Scan() {
			if err := reader.Err(); err != nil {
				demo.Panicf("failed to read input: %v", err)
			}
			return
		}
		message := strings.TrimSpace(reader.Text())
		if message == "" {
			demo.Assistant("Request cannot be empty.")
			continue
		}
		if message == ":q" || strings.EqualFold(message, "quit") {
			break
		}

		resp, runErr := policyAgent.RunText(ctx, message, agent.WithSession(session)).Collect()
		demo.Response(resp, runErr)
	}
}
