// Copyright (c) Microsoft. All rights reserved.

// This sample shares one application service between distance and weight skills.
// Constructors inject the service; resource and script callbacks capture it.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

type conversionService struct {
	precision int
}

func (*conversionService) distanceTable() string {
	return `# Distance Conversions

Formula: **result = value * factor**

| From       | To         | Factor   |
|------------|------------|----------|
| miles      | kilometers | 1.60934  |
| kilometers | miles      | 0.621371 |`
}

func (*conversionService) weightTable() string {
	return `# Weight Conversions

Formula: **result = value * factor**

| From      | To        | Factor   |
|-----------|-----------|----------|
| pounds    | kilograms | 0.453592 |
| kilograms | pounds    | 2.20462  |`
}

func (s *conversionService) convert(value, factor float64) map[string]any {
	scale := math.Pow10(s.precision)
	return map[string]any{
		"value":  value,
		"factor": factor,
		"result": math.RoundToEven(value*factor*scale) / scale,
	}
}

func newDistanceSkill(service *conversionService) *skills.Skill {
	return &skills.Skill{
		Frontmatter: skills.Frontmatter{
			Name:        "distance-converter",
			Description: "Convert between miles and kilometers using an application service.",
		},
		GetContent: func(context.Context) (string, error) {
			return `Use this skill to convert miles and kilometers.
1. Read the distance-table resource to find the conversion factor.
2. Run convert with named JSON arguments: {"value": <value>, "factor": <factor>}.
3. Present the result with both units.`, nil
		},
		Resources: []skills.Resource{{
			Name:        "distance-table",
			Description: "Distance conversion factors supplied by the application service.",
			Read: func(context.Context) (any, error) {
				return service.distanceTable(), nil
			},
		}},
		Scripts: []skills.Script{newConversionScript(service)},
	}
}

func newWeightSkill(service *conversionService) *skills.Skill {
	return &skills.Skill{
		Frontmatter: skills.Frontmatter{
			Name:        "weight-converter",
			Description: "Convert between pounds and kilograms using an application service.",
		},
		GetContent: func(context.Context) (string, error) {
			return `Use this skill to convert pounds and kilograms.
1. Read the weight-table resource to find the conversion factor.
2. Run convert with named JSON arguments: {"value": <value>, "factor": <factor>}.
3. Present the result with both units.`, nil
		},
		Resources: []skills.Resource{{
			Name:        "weight-table",
			Description: "Weight conversion factors supplied by the application service.",
			Read: func(context.Context) (any, error) {
				return service.weightTable(), nil
			},
		}},
		Scripts: []skills.Script{newConversionScript(service)},
	}
}

func newConversionScript(service *conversionService) skills.Script {
	return skills.Script{
		Name:             "convert",
		Description:      "Multiply a numeric value by a conversion factor using the application service.",
		ParametersSchema: `{"type":"object","properties":{"value":{"type":"number"},"factor":{"type":"number"}},"required":["value","factor"],"additionalProperties":false}`,
		Run: func(_ context.Context, _ *skills.Skill, args json.RawMessage) (any, error) {
			var input struct {
				Value  *float64 `json:"value"`
				Factor *float64 `json:"factor"`
			}
			decoder := json.NewDecoder(bytes.NewReader(args))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return nil, fmt.Errorf("decode conversion arguments: %w", err)
			}
			if input.Value == nil || input.Factor == nil {
				return nil, fmt.Errorf("arguments must contain numeric value and factor")
			}
			return service.convert(*input.Value, *input.Factor), nil
		},
	}
}

var logger = demo.NewLogger(
	"Skills With Dependencies",
	"Distance and weight skills share a constructor-injected conversion service. Script execution requires approval.",
	"Model", demo.FoundryModel,
)

func main() {
	if err := run(context.Background()); err != nil {
		demo.Panic(err)
	}
}

func run(ctx context.Context) (err error) {
	service := &conversionService{precision: 4}
	skillsProvider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{newDistanceSkill(service), newWeightSkill(service)},
	})
	defer func() { err = errors.Join(err, skillsProvider.Close()) }()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		demo.FoundryTokenCredential(),
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that converts distances and weights.",
			Config: agent.Config{
				Name: "DependencyInjectedConverterAgent",
				Middlewares: []agent.Middleware{logger, toolapproval.New(toolapproval.Config{
					AutoApprovalRules: []toolapproval.AutoApprovalRule{skills.ReadOnlyToolsAutoApprovalRule},
				})},
				ContextProviders: []agent.ContextProvider{skillsProvider},
			},
		},
	)
	session, err := a.CreateSession(ctx)
	if err != nil {
		return err
	}
	response, err := skillhelpers.RunWithApprovals(ctx, a, session,
		"How many kilometers is a marathon (26.2 miles)? And how many pounds is 75 kilograms?",
		demo.UserInputRequest)
	if err != nil {
		return err
	}
	demo.Response(response, nil)
	return nil
}
