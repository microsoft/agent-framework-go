// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to run an Agent to produce structured output.

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
)

type PersonInfo struct {
	Name       string `json:"name"`
	Age        int    `json:"age"`
	Occupation string `json:"occupation"`
}

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		Instructions: "You are a helpful assistant.",
		Name:         "HelpfulAssistant",
	})

	ctx := context.Background()

	// Set PersonInfo as the type parameter of RunFor method to specify the expected structured output from the agent and invoke the agent with some unstructured input.
	person, _, err := agent.RunFor[PersonInfo](ctx, a, agent.WithMessage(message.NewText("Please provide information about John Smith, who is a 35-year-old software engineer.")))
	if err != nil {
		panic(err)
	}

	fmt.Println("Assistant Output:")
	fmt.Println("Name:", person.Name)
	fmt.Println("Age:", person.Age)
	fmt.Println("Occupation:", person.Occupation)

	// Create the agent with the specified name, instructions, and expected structured output the agent should produce.
	a = openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		Instructions: "You are a helpful assistant.",
		Name:         "HelpfulAssistant",
		ChatOptions: &chatclient.ChatOptions{
			ResponseFormat: jsonformat.MustFor[PersonInfo](),
		},
	})

	// Invoke the agent with some unstructured input while streaming, to extract the structured information from.
	var personRaw []byte
	for update, err := range agent.RunTextStream(ctx, a, "Please provide information about John Smith, who is a 35-year-old software engineer.") {
		if err != nil {
			panic(err)
		}
		personRaw = append(personRaw, update.String()...)
	}
	var person2 PersonInfo
	if err := json.Unmarshal(personRaw, &person2); err != nil {
		panic(err)
	}
	fmt.Println("Assistant Output:")
	fmt.Println("Name:", person2.Name)
	fmt.Println("Age:", person2.Age)
	fmt.Println("Occupation:", person2.Occupation)
}
