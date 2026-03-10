// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to use Agent Skills with a chat agent.
// Agent Skills are modular packages of instructions and resources that extend an agent's capabilities.
// Skills follow the progressive disclosure pattern: advertise -> load -> read resources.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/memory"
	memskills "github.com/microsoft/agent-framework-go/memory/skills"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")

var logger = demo.NewLogger(
	"Basic Skills",
	"Using Agent Skills with progressive disclosure and skill resources.",
	"Model", deployment,
)

func main() {
	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	a := openaichatagent.New(openaichatagent.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		Model: deployment,
		Agent: agent.Config{
			Name:             "SkillsAgent",
			Instructions:     "You are a helpful assistant.",
			Middlewares:      []middleware.Middleware{logger},
			ContextProviders: []*memory.ContextProvider{memskills.New(nil, os.DirFS("skills"))},
		},
	})

	ctx := context.Background()

	// --- Example 1: Expense policy question (loads FAQ resource) ---
	resp, err := a.RunText(ctx, "Are tips reimbursable? I left a 25% tip on a taxi ride and want to know if that's covered.").Collect()
	demo.Response(resp, err)

	// --- Example 2: Filing an expense report (multi-turn with template asset) ---
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err = a.RunText(ctx, "I had 3 client dinners and a $1,200 flight last week. Return a draft expense report and ask about any missing details.", agentopt.Session(session)).Collect()
	demo.Response(resp, err)
}
