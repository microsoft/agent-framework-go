// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to define Agent Skills entirely in Go code.
// No SKILL.md files are needed; skills, resources, and scripts are all defined programmatically.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

const unitConverterInstructions = `Use this skill when the user asks to convert between units.

1. Review the conversion-table resource to find the factor for the requested conversion.
2. Check the conversion-policy resource for rounding and formatting rules.
3. Use the convert script, passing the value and factor from the table.`

const conversionTable = `# Conversion Tables

Formula: **result = value * factor**

| From       | To         | Factor   |
|------------|------------|----------|
| miles      | kilometers | 1.60934  |
| kilometers | miles      | 0.621371 |
| pounds     | kilograms  | 0.453592 |
| kilograms  | pounds     | 2.20462  |`

var unitConverterSkill = &skills.Skill{
	Frontmatter: skills.Frontmatter{
		Name:        "unit-converter",
		Description: "Convert between common units using a multiplication factor. Use when asked to convert miles, kilometers, pounds, or kilograms.",
	},
	Content: unitConverterInstructions,
	Resources: []skills.Resource{
		{
			Name:        "conversion-table",
			Description: "Lookup table of multiplication factors for common unit conversions.",
			Read: func(context.Context) (any, error) {
				return conversionTable, nil
			},
		},
		{
			Name:        "conversion-policy",
			Description: "Formatting and rounding rules generated at runtime.",
			Read: func(context.Context) (any, error) {
				return fmt.Sprintf(`# Conversion Policy

**Decimal places:** 4
**Format:** Always show both the original and converted values with units
**Generated at:** %s`, time.Now().UTC().Format(time.RFC3339)), nil
			},
		},
	},
	Scripts: []skills.Script{
		{
			Name:        "convert",
			Description: "Multiplies a value by a conversion factor and returns the result as JSON.",
			Run: func(_ context.Context, _ *skills.Skill, arguments map[string]any) (any, error) {
				value, err := skillhelpers.NumberArg(arguments, "value")
				if err != nil {
					return nil, err
				}
				factor, err := skillhelpers.NumberArg(arguments, "factor")
				if err != nil {
					return nil, err
				}
				return skillhelpers.MultiplyConversion(value, factor, 4), nil
			},
		},
	},
}

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-5.4-mini")
)

var logger = demo.NewLogger(
	"Code-Defined Skills",
	"Using code-defined Agent Skills with static and dynamic resources plus code scripts.",
	"Model", deployment,
)

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	skillsProvider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{unitConverterSkill}})
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
