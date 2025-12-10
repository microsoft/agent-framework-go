package main

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/openai"
	"github.com/microsoft/agent-framework/go/tool"
)

/*
OpenAI Chat Agent with Web Search Example

This sample demonstrates using hostedtool.WebSearch with OpenAI Chat Agent
for real-time information retrieval and current data access.
*/

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-search-preview",
	}, &chatagent.Options{
		Instructions: "You are a helpful weather agent.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{openai.NewWebSearchTool("Seattle", "", "US", "", "")},
		},
	})

	const message = "What is the current weather? Do not ask for my current location."
	if true {
		nonStreamingExample(a, message)
	} else {
		streamingExample(a, message)
	}
}

func nonStreamingExample(a agent.Agent, query string) {
	fmt.Println("=== Non-streaming Response Example ===")
	fmt.Println("User: ", query)
	resp, err := agent.RunText(a, query)
	if err != nil {
		panic(err)
	}
	fmt.Println(resp)
}

func streamingExample(a agent.Agent, query string) {
	fmt.Println("=== Streaming Response Example ===")
	fmt.Println("User: ", query)
	for update, err := range agent.RunTextStream(a, query) {
		if err != nil {
			fmt.Print(err)
			break
		}
		fmt.Print(update)
	}
	fmt.Print("\n")
}
