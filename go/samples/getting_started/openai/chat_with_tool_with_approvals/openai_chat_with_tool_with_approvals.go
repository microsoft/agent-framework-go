package main

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/microsoft/agent-framework/go/agent"
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
	ag := openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-5-nano",
		SystemInstructions: "You are a helpful weather agent.",
		Opts: &agent.RunOptions{
			Tools: []tool.Tool{tool.ApprovalRequiredFunc(weatherTool)},
		},
	})

	ctx := &agent.RunContext{
		Thread: ag.NewThread(),
	}

	resp := must(ag.RunText(ctx, "What's the weather like in Amsterdam?"))

	for len(resp.UserInputRequest) > 0 {
		// Ask the user to approve each function call request.
		// For simplicity, we are assuming here that only function approval requests are being made.
		request, ok := resp.UserInputRequest[0].(*message.FunctionApprovalRequestContent)
		if !ok {
			panic(fmt.Sprintf("unexpected type %T", resp.UserInputRequest[0]))
		}
		fmt.Println("The agent would like to invoke the following function, please reply Y to approve:", request.FunctionCall.Name)
		var approval string
		fmt.Scanln(&approval)
		if approval != "Y" {
			continue
		}

		// Pass the user input responses back to the agent for further processing.
		resp = must(ag.Run(ctx, message.New(request.Response(true))))
	}

	fmt.Println("Agent:", resp)
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
