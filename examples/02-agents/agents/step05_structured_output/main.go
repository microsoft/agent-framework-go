// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
)

var logger = demo.NewLogger(
	"Structured Output",
	"Demonstrates how to produce structured output.",
	"Model", deployment,
)

type PersonInfo struct {
	Name       string `json:"name"`
	Age        int    `json:"age"`
	Occupation string `json:"occupation"`
}

// runFor executes the agent with the given messages and returns the result of type T.
func runFor[T any](ctx context.Context, a *agent.Agent, message string, opts ...agent.Option) (T, error) {
	var v T
	opts = append(opts, agent.WithStructuredOutput(&v), agent.Stream(false))
	for _, err := range a.RunText(ctx, message, opts...) {
		if err != nil {
			return v, err
		}
	}
	return v, nil
}

func main() {
	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	// Create Azure OpenAI agent.
	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions: "You are a helpful assistant.",
				Name:         "HelpfulAssistant",
				Middlewares:  []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()

	// Set PersonInfo as the type parameter of runFor method to specify the expected structured output from the agent and invoke the agent with some unstructured input.
	person, err := runFor[PersonInfo](ctx, a, "Please provide information about John Smith, who is a 35-year-old software engineer.")
	if err != nil {
		demo.Panic(err)
	}

	fmt.Println("Structured Output:")
	fmt.Println("\tName:", person.Name)
	fmt.Println("\tAge:", person.Age)
	fmt.Println("\tOccupation:", person.Occupation)
	fmt.Println()

	// Create the agent with the specified name, instructions, and expected structured output the agent should produce.
	a = openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions: "You are a helpful assistant.",
				Name:         "HelpfulAssistant",
				Middlewares:  []agent.Middleware{logger}, // for logging agent interactions
				RunOptions: []agent.Option{
					agent.WithResponseFormat(jsonformat.MustFor[PersonInfo]()),
				},
			},
		},
	)

	// Invoke the agent with some unstructured input while streaming, to extract the structured information from.
	var personRaw []byte
	for update, err := range a.RunText(ctx, "Please provide information about John Smith, who is a 35-year-old software engineer.", agent.Stream(true)) {
		demo.Response(update, err)
		personRaw = append(personRaw, update.String()...)
	}
	var person2 PersonInfo
	if err := json.Unmarshal(personRaw, &person2); err != nil {
		demo.Panic(err)
	}

	fmt.Println("Structured Output:")
	fmt.Println("\tName:", person2.Name)
	fmt.Println("\tAge:", person2.Age)
	fmt.Println("\tOccupation:", person2.Occupation)
	fmt.Println()
}
