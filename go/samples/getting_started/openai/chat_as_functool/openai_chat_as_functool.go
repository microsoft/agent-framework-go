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

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
	return fmt.Sprintf("The weather in %s is %s with a high of %d°C.", location, conditions[rand.Intn(len(conditions))], rand.Intn(21)+10), nil
})

func main() {
	// Create the chat client and agent, and provide the function tool to the agent.
	weatherAgent := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, &chatagent.Options{
		Instructions: "You answer questions about the weather.",
		Name:         "WeatherAgent",
		Description:  "An agent that answers questions about the weather.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, &chatagent.Options{
		Instructions: "You are a helpful assistant who responds in French.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{agent.FuncTool(weatherAgent, nil)},
		},
	})

	fmt.Println(must(agent.RunText(context.Background(), a, "What is the weather like in Amsterdam?")))

}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
