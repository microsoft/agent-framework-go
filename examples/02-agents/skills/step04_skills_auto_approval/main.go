// Copyright (c) Microsoft. All rights reserved.

// This sample auto-approves read-only skill tools and prompts before scripts run.
// Approval responses resume the same session, including when a call is rejected.

package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
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
			Run: func(_ context.Context, _ *skills.Skill, rawScriptArguments json.RawMessage) (any, error) {
				var args []string
				if len(rawScriptArguments) != 0 {
					if err := json.Unmarshal(rawScriptArguments, &args); err != nil {
						return nil, err
					}
				}
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
	"Auto-approves loading skills and reading resources; script execution requires explicit approval.",
	"Model", demo.FoundryModel,
)

func main() {
	if err := run(context.Background()); err != nil {
		demo.Panic(err)
	}
}

func skillsAgentConfig(provider *skills.ContextProvider) agent.Config {
	return agent.Config{
		Name: "UnitConverterAutoApprovalAgent",
		Middlewares: []agent.Middleware{logger, toolapproval.New(toolapproval.Config{
			AutoApprovalRules: []toolapproval.AutoApprovalRule{skills.ReadOnlyToolsAutoApprovalRule},
		})},
		ContextProviders: []agent.ContextProvider{provider},
	}
}

func run(ctx context.Context) (err error) {
	token := demo.FoundryTokenCredential()

	skillsProvider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{unitConverterSkill},
	})
	defer func() { err = errors.Join(err, skillsProvider.Close()) }()

	unitConverterAgent := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that can convert units.",
			Config:       skillsAgentConfig(skillsProvider),
		},
	)

	session, err := unitConverterAgent.CreateSession(ctx)
	if err != nil {
		return err
	}
	response, err := skillhelpers.RunWithApprovals(ctx, unitConverterAgent, session,
		"How many kilometers is a marathon (26.2 miles)? And how many pounds is 75 kilograms?",
		demo.UserInputRequest)
	if err != nil {
		return err
	}
	demo.Response(response, nil)
	return nil
}
