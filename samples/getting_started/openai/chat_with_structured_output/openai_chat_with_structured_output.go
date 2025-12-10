package main

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
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
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, &chatagent.Options{
		Name:         "CityAgent",
		Instructions: "You are a helpful agent that describes cities in a structured format.",
	})

	const query = "Tell me about Paris, France"
	fmt.Println("User: ", query)

	city, resp, err := agent.RunFor[CityInfo](a, agent.WithMessage(message.NewText(query)))
	if err != nil {
		panic(err)
	}
	fmt.Println("Agent raw response: ", resp)

	fmt.Println("City Name: ", city.Name)
	fmt.Println("Population: ", city.Population)
	fmt.Println("Area: ", city.Area)
}
