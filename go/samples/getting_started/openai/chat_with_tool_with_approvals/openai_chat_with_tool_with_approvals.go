package main

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/message"
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
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-5-nano",
	}, &chatagent.Options{
		Instructions: "You are a helpful weather agent.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{tool.ApprovalRequiredFunc(weatherTool)},
		},
	})

	ctx := context.Background()
	options := agent.RunOptions{
		Thread: a.NewThread(),
	}

	resp := must(agent.Run(ctx, a, options, message.NewText("What's the weather like in Amsterdam?")))

	var userResponses []message.Content
	for req := range resp.UserInputRequests() {
		// Ask the user to approve each function call request.
		request, ok := req.(*message.FunctionApprovalRequestContent)
		if !ok {
			panic(fmt.Sprintf("unexpected type %T", req))
		}
		fmt.Println("The agent would like to invoke the following function, please reply Y to approve:", request.FunctionCall.Name)
		var approval string
		fmt.Scanln(&approval)
		if approval != "Y" {
			continue
		}
		userResponses = append(userResponses, request.Response(true))
	}

	// Pass the user input responses back to the agent for further processing.
	fmt.Println("Agent:", must(agent.Run(ctx, a, options, message.New(userResponses...))))
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
