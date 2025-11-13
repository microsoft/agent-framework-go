package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/format/jsonformat"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Chat Client with Structured Output Example

This sample demonstrates using structured output capabilities with OpenAI Chat Client,
showing jsonschema model integration for type-safe response parsing and data extraction.
*/

type CityInfo struct {
	Name       string `json:"name"`
	Population int
	Area       float64 `jsonschema:"area in square kilometers"`
}

func main() {
	ctx := context.Background()
	ag := openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-5-nano",
		Name:               "CityAgent",
		SystemInstructions: "You are a helpful agent that describes cities in a structured format.",
	})

	const query = "Tell me about Paris, France"
	fmt.Println("User: " + query)

	var out jsonformat.Value[CityInfo]
	resp, err := ag.Run(ctx, nil, &agent.RunOptions{Response: &out}, agent.NewTextMessage(query))
	if err != nil {
		fmt.Print(err)
		return
	}

	fmt.Printf("Agent raw response: %s\n", resp.Text())

	city := out.Unwrap()
	fmt.Printf("City Name: %v\n", city.Name)
	fmt.Printf("Population: %v\n", city.Population)
	fmt.Printf("Area: %v\n", city.Area)
}
