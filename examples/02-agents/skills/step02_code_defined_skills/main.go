// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to define Agent Skills entirely in Go code.
// No SKILL.md files are needed; skills, resources, and scripts are all defined programmatically.

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

const unitConverterInstructions = `Use this skill when the user asks to convert between units.

1. Review the conversion-table resource to find the factor for the requested conversion.
2. Check the conversion-policy resource for rounding and formatting rules.
3. Use the convert script, passing the value and factor as two positional arguments: ["<value>", "<factor>"].`

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
	GetContent: func(context.Context) (string, error) {
		return unitConverterInstructions, nil
	},
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
			Description: "Multiplies a value by a conversion factor and returns the result as JSON. Pass value and factor as two positional string arguments: [\"<value>\", \"<factor>\"].",
			Run: func(_ context.Context, _ *skills.Skill, args []string) (any, error) {
				value, err := skillhelpers.NumberArg(args, 0)
				if err != nil {
					return nil, err
				}
				factor, err := skillhelpers.NumberArg(args, 1)
				if err != nil {
					return nil, err
				}
				return skillhelpers.MultiplyConversion(value, factor, 4), nil
			},
		},
	},
}

var deployment = demo.FoundryModel

var logger = demo.NewLogger(
	"Code-Defined Skills",
	"Using code-defined Agent Skills with static and dynamic resources plus code scripts.",
	"Model", deployment,
)

func main() {
	token := demo.FoundryTokenCredential()

	skillsProvider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{unitConverterSkill}})
	agent := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(deployment),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that can convert units.",
			Config: agent.Config{
				Name:             "UnitConverterAgent",
				Middlewares:      []agent.Middleware{logger},
				ContextProviders: []agent.ContextProvider{skillsProvider},
			},
		},
	)

	ctx := context.Background()
	response, err := agent.RunText(ctx, "How many kilometers is a marathon (26.2 miles)? And how many pounds is 75 kilograms?").Collect()
	demo.Response(response, err)
}
