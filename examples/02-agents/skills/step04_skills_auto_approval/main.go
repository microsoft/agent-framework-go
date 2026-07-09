// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to auto-approve Agent Skills tool calls with
// reusable helper rules from the skills package.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

const unitConverterInstructions = `Use this skill when the user asks to convert between units.

1. Review the conversion-table resource to find the factor for the requested conversion.
2. Use the convert script, passing the value and factor as two positional arguments: ["<value>", "<factor>"].`

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
		Description: "Convert between common units using a multiplication factor.",
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
	},
	Scripts: []skills.Script{
		{
			Name:        "convert",
			Description: "Multiplies a value by a conversion factor and returns the result as JSON.",
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

var logger = demo.NewLogger(
	"Skills Auto Approval",
	"Uses skills.AllToolsAutoApprovalRule to auto-approve skill tool calls, including script execution.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	skillsProvider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{unitConverterSkill},
	})
	approvalMiddleware := toolapproval.New(toolapproval.Config{
		AutoApprovalRules: []func(context.Context, *message.FunctionCallContent) (bool, error){
			skills.AllToolsAutoApprovalRule,
		},
	})

	unitConverterAgent := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that can convert units.",
			Config: agent.Config{
				Name:             "UnitConverterAutoApprovalAgent",
				Middlewares:      []agent.Middleware{logger, approvalMiddleware},
				ContextProviders: []agent.ContextProvider{skillsProvider},
			},
		},
	)

	response, err := unitConverterAgent.RunText(
		context.Background(),
		"How many kilometers is a marathon (26.2 miles)? And how many pounds is 75 kilograms?",
	).Collect()
	demo.Response(response, err)
}
