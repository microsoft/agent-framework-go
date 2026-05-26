// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates an advanced scenario: combining file-based, code-defined,
// and struct-based skills in a single agent using explicit source composition.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

const volumeConverterInstructions = `Use this skill when the user asks to convert between gallons and liters.

1. Review the volume-conversion-table resource to find the correct factor.
2. Use the convert-volume script, passing the value and factor as positional arguments: ["<value>", "<factor>"].
3. Present the result clearly with both units.`

const volumeConversionTable = `# Volume Conversion Table

Formula: **result = value * factor**

| From    | To      | Factor   |
|---------|---------|----------|
| gallons | liters  | 3.78541  |
| liters  | gallons | 0.264172 |`

const temperatureConverterInstructions = `Use this skill when the user asks to convert temperatures.

1. Review the temperature-conversion-formulas resource for the correct formula.
2. Use the convert-temperature script, passing value, source scale, and target scale as positional arguments: ["<value>", "<from>", "<to>"].
3. Present the result clearly with both temperature scales.`

const temperatureConversionFormulas = `# Temperature Conversion Formulas

| From       | To         | Formula               |
|------------|------------|-----------------------|
| Fahrenheit | Celsius    | °C = (°F - 32) * 5/9 |
| Celsius    | Fahrenheit | °F = (°C * 9/5) + 32 |
| Celsius    | Kelvin     | K = °C + 273.15      |
| Kelvin     | Celsius    | °C = K - 273.15      |`

var volumeConverterSkill = &skills.Skill{
	Frontmatter: skills.Frontmatter{
		Name:        "volume-converter",
		Description: "Convert between gallons and liters using a multiplication factor.",
	},
	GetContent: func(context.Context) (string, error) {
		return volumeConverterInstructions, nil
	},
	Resources: []skills.Resource{
		{
			Name:        "volume-conversion-table",
			Description: "Lookup table of multiplication factors for volume conversions.",
			Read: func(context.Context) (any, error) {
				return volumeConversionTable, nil
			},
		},
	},
	Scripts: []skills.Script{
		{
			Name:        "convert-volume",
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

var temperatureConverterSkill = skills.Skill{
	Frontmatter: skills.Frontmatter{
		Name:        "temperature-converter",
		Description: "Convert between temperature scales such as Fahrenheit, Celsius, and Kelvin.",
	},
	GetContent: func(context.Context) (string, error) {
		return temperatureConverterInstructions, nil
	},
	Resources: []skills.Resource{
		{
			Name:        "temperature-conversion-formulas",
			Description: "Formulas for converting between Fahrenheit, Celsius, and Kelvin.",
			Read: func(context.Context) (any, error) {
				return temperatureConversionFormulas, nil
			},
		},
	},
	Scripts: []skills.Script{
		{
			Name:        "convert-temperature",
			Description: "Converts a temperature value from one scale to another. Pass value, source scale, and target scale as three positional string arguments: [\"<value>\", \"<from>\", \"<to>\"].",
			Run: func(_ context.Context, _ *skills.Skill, args []string) (any, error) {
				value, err := skillhelpers.NumberArg(args, 0)
				if err != nil {
					return nil, err
				}
				from, err := skillhelpers.StringArg(args, 1)
				if err != nil {
					return nil, err
				}
				to, err := skillhelpers.StringArg(args, 2)
				if err != nil {
					return nil, err
				}

				var result float64
				switch strings.ToUpper(from) + "->" + strings.ToUpper(to) {
				case "FAHRENHEIT->CELSIUS":
					result = skillhelpers.Round((value-32)*5.0/9.0, 2)
				case "CELSIUS->FAHRENHEIT":
					result = skillhelpers.Round(value*9.0/5.0+32, 2)
				case "CELSIUS->KELVIN":
					result = skillhelpers.Round(value+273.15, 2)
				case "KELVIN->CELSIUS":
					result = skillhelpers.Round(value-273.15, 2)
				default:
					return nil, fmt.Errorf("unsupported conversion: %s -> %s", from, to)
				}

				return map[string]any{
					"value":  value,
					"from":   from,
					"to":     to,
					"result": result,
				}, nil
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
	"Mixed Skills",
	"Combining file-based, code-defined, and struct-based Agent Skills with explicit source composition.",
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
		Skills: []*skills.Skill{volumeConverterSkill, &temperatureConverterSkill},
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
			Instructions: "You are a helpful assistant that can convert units, volumes, and temperatures.",
			Config: agent.Config{
				Name:             "MultiConverterAgent",
				Middlewares:      []agent.Middleware{logger},
				ContextProviders: []*agent.ContextProvider{skillsProvider},
			},
		},
	)

	response, err := agent.RunText(
		context.Background(),
		"I need three conversions: How many kilometers is a marathon (26.2 miles)? How many liters is a 5-gallon bucket? What is 98.6 Fahrenheit in Celsius?",
	).Collect()
	demo.Response(response, err)
}
