package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/format/jsonformat"
	"github.com/microsoft/agent-framework/go/message"
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
	fmt.Println("User: ", query)

	var out jsonformat.Value[CityInfo]
	fmt.Println("Agent raw response: ", must(ag.Run(ctx, nil, &agent.RunOptions{Response: &out}, message.NewText(query))))

	city := out.Unwrap()
	fmt.Println("City Name: ", city.Name)
	fmt.Println("Population: ", city.Population)
	fmt.Println("Area: ", city.Area)
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
