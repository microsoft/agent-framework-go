// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to use Agent Skills with a chat agent.
// Agent Skills are modular packages of instructions and resources that extend an agent's capabilities.
// Skills follow the progressive disclosure pattern: advertise -> load -> read resources.
//
// This sample includes the expense-report skill:
// - Policy-based expense filing with references and assets

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/middleware/skills"
)

var logger = demo.NewLogger(
	"Agent Skills",
	"Demonstrates how to use Agent Skills with a chat agent, including progressive disclosure and skill resources.",
	"Model", "gpt-4o-mini",
)

func main() {
	// --- Skills Provider ---
	// Discovers skills from the 'skills' directory and makes them available to the agent.
	skillsProvider := skills.New(nil, os.DirFS("skills"))

	// --- Agent Setup ---
	a := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-4o-mini",
		Agent: agent.Config{
			Instructions:        "You are a helpful assistant.",
			Name:                "SkillsAgent",
			DisableFuncAutoCall: true,
			Middlewares: []middleware.Middleware{
				logger,
				skillsProvider,
			},
		},
	})

	ctx := context.Background()

	// --- Example 1: Expense policy question (loads FAQ resource) ---
	fmt.Println("\nExample 1: Checking expense policy FAQ")
	fmt.Println("---------------------------------------")
	resp, err := a.RunText("Are tips reimbursable? I left a 25% tip on a taxi ride and want to know if that's covered.").Collect(ctx)
	demo.Response(resp, err)

	// --- Example 2: Filing an expense report (multi-turn with template asset) ---
	fmt.Println("\nExample 2: Filing an expense report")
	fmt.Println("---------------------------------------")
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp2, err := a.RunText("I had 3 client dinners and a $1,200 flight last week. Return a draft expense report and ask about any missing details.",
		agentopt.Session(session),
	).Collect(ctx)
	demo.Response(resp2, err)
}
