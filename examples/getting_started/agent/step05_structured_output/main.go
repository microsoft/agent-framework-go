// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/microsoft/agent-framework-go/middleware"
)

var logger = demo.NewLogger(
	"Structured Output",
	"Demonstrates how to produce structured output.",
	"Model", "gpt-4o-mini",
)

type PersonInfo struct {
	Name       string `json:"name"`
	Age        int    `json:"age"`
	Occupation string `json:"occupation"`
}

// runFor executes the agent with the given messages and returns the result of type T.
func runFor[T any](ctx context.Context, a *agent.Agent, message string, opts ...agentopt.Option) (T, error) {
	var v T
	opts = append(opts, agentopt.StructuredOutput(&v), agentopt.Stream(false))
	for _, err := range a.RunText(message, opts...).All(ctx) {
		if err != nil {
			return v, err
		}
	}
	return v, nil
}

func main() {
	a := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-4o-mini",
		Agent: agent.Config{
			Instructions: "You are a helpful assistant.",
			Name:         "HelpfulAssistant",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		},
	})

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
	a = openaichat.NewAgent(openaichat.Config{
		Model: "gpt-4o-mini",
		Agent: agent.Config{
			Instructions: "You are a helpful assistant.",
			Name:         "HelpfulAssistant",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
			RunOptions: []agentopt.Option{
				agentopt.ResponseFormat(jsonformat.MustFor[PersonInfo]()),
			},
		},
	})

	// Invoke the agent with some unstructured input while streaming, to extract the structured information from.
	var personRaw []byte
	for update, err := range a.RunText("Please provide information about John Smith, who is a 35-year-old software engineer.").All(ctx) {
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
