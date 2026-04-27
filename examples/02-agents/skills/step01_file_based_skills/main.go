// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to use file-based Agent Skills with a chat agent.
// Skills are discovered from SKILL.md files on disk and follow the progressive disclosure pattern:
// advertise -> load -> read resources -> run scripts.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/memory/skills"
	"github.com/microsoft/agent-framework-go/memory/skills/fsskills"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-5.4-mini")
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
)

var logger = demo.NewLogger(
	"File-Based Skills",
	"Using file-based Agent Skills with progressive disclosure, resources, and scripts.",
	"Model", deployment,
)

func main() {
	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	skillsRoot, err := os.OpenRoot("skills")
	if err != nil {
		panic(err)
	}
	defer func() { _ = skillsRoot.Close() }()
	skillsProvider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{
			fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: skillhelpers.RunSubprocessScript}, skillsRoot.FS()),
		},
	})

	agent := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Name:             "UnitConverterAgent",
				Instructions:     "You are a helpful assistant that can convert units.",
				Middlewares:      []agent.Middleware{logger},
				ContextProviders: []*memory.ContextProvider{skillsProvider},
			},
		},
	)

	ctx := context.Background()
	response, err := agent.RunText(ctx, "How many kilometers is a marathon (26.2 miles)? And how many pounds is 75 kilograms?").Collect()
	demo.Response(response, err)
}
