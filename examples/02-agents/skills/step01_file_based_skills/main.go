// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to use file-based Agent Skills with a chat agent.
// Skills are discovered from SKILL.md files on disk and follow the progressive disclosure pattern:
// advertise -> load -> read resources -> run scripts.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-5.4-mini")
)

var logger = demo.NewLogger(
	"File-Based Skills",
	"Using file-based Agent Skills with progressive disclosure, resources, and scripts.",
	"Model", deployment,
)

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	skillsRoot, err := os.OpenRoot("skills")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = skillsRoot.Close() }()
	skillsProvider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{
			fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: skillhelpers.RunSubprocessScript}, skillsRoot.FS()),
		},
	})

	agent := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You are a helpful assistant that can convert units.",
			Config: agent.Config{
				Name:             "UnitConverterAgent",
				Middlewares:      []agent.Middleware{logger},
				ContextProviders: []*agent.ContextProvider{skillsProvider},
			},
		},
	)

	ctx := context.Background()
	response, err := agent.RunText(ctx, "How many kilometers is a marathon (26.2 miles)? And how many pounds is 75 kilograms?").Collect()
	demo.Response(response, err)
}
