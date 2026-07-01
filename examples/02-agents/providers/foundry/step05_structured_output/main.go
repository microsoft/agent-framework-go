// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

type PersonInfo struct {
	Name       string `json:"name"`
	Age        int    `json:"age"`
	Occupation string `json:"occupation"`
}

var logger = demo.NewLogger(
	"Foundry Structured Output",
	"Demonstrates structured output with a Foundry agent.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that extracts structured information about people.",
			Config: agent.Config{
				Name:        "StructuredOutputAssistant",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	var person PersonInfo
	_, err := a.RunText(
		context.Background(),
		"Please provide information about John Smith, who is a 35-year-old software engineer.",
		agent.WithStructuredOutput(&person),
		agent.Stream(false),
	).Collect()
	if err != nil {
		demo.Panic(err)
	}

	fmt.Println("Structured Output:")
	fmt.Println("\tName:", person.Name)
	fmt.Println("\tAge:", person.Age)
	fmt.Println("\tOccupation:", person.Occupation)
}
