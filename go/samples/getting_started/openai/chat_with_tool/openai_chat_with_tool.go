package main

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/openai"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

/*
OpenAI Chat Agent Basic Example

This sample demonstrates basic usage of openai.Agent for direct chat-based
interactions, showing both streaming and non-streaming responses.
*/

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
	return fmt.Sprintf("The weather in %s is %s with a high of %d°C.", location, conditions[rand.Intn(len(conditions))], rand.Intn(21)+10), nil
})

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, &chatagent.Options{
		Instructions: "You are a helpful weather agent.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	fmt.Println(must(agent.RunText(context.Background(), a, "What's the weather like in Amsterdam?")))

	for update, err := range agent.RunTextStream(context.Background(), a, "What is the weather like in Amsterdam?") {
		if err != nil {
			fmt.Print(err)
			break
		}
		fmt.Print(update)
	}
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
