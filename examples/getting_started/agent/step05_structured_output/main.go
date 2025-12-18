// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/microsoft/agent-framework-go/openai"
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

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Options{
		Instructions: "You are a helpful assistant.",
		Name:         "HelpfulAssistant",
		Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
	})

	ctx := context.Background()

	// Set PersonInfo as the type parameter of RunTextFor method to specify the expected structured output from the agent and invoke the agent with some unstructured input.
	person, err := agent.RunTextFor[PersonInfo](ctx, a, "Please provide information about John Smith, who is a 35-year-old software engineer.")
	if err != nil {
		demo.Panic(err)
	}

	fmt.Println("Structured Output:")
	fmt.Println("\tName:", person.Name)
	fmt.Println("\tAge:", person.Age)
	fmt.Println("\tOccupation:", person.Occupation)
	fmt.Println()

	// Create the agent with the specified name, instructions, and expected structured output the agent should produce.
	a = openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Options{
		Instructions: "You are a helpful assistant.",
		Name:         "HelpfulAssistant",
		Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		RunOptions: []agentopt.RunOption{
			agentopt.ResponseFormat(jsonformat.MustFor[PersonInfo]()),
		},
	})

	// Invoke the agent with some unstructured input while streaming, to extract the structured information from.
	var personRaw []byte
	for update, err := range agent.RunTextStream(ctx, a, "Please provide information about John Smith, who is a 35-year-old software engineer.") {
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
