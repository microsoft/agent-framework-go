// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Function Tools With User Approvals",
	"Demonstrates how to use function tools that require user approval.",
	"Model", "gpt-4o-mini",
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Create the agent.
	// Note that we are wrapping the function tool with tool.ApprovalRequiredFunc to require user approval before invoking it.
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Config{
		Instructions: "You are a helpful assistant",
		Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		RunOptions: []agentopt.RunOption{
			agentopt.Tool(tool.ApprovalRequiredFunc(weatherTool)),
		},
	})

	ctx := context.Background()

	// Call the agent and check if there are any user input requests to handle.
	thread, err := a.NewThread(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := agent.RunText(ctx, a, "What is the weather like in Amsterdam?", agentopt.Thread(thread))
	if err != nil {
		panic(err)
	}

	var userResponses []message.Content
	var approvedRequests bool
	for req := range resp.UserInputRequests() {
		// Ask the user to approve each function call request.
		request, ok := req.(*message.FunctionApprovalRequestContent)
		if !ok {
			demo.Panicf("unexpected request type: %T", req)
			continue
		}
		approved := demo.UserInputRequest(request)
		if approved {
			approvedRequests = true
		}
		userResponses = append(userResponses, request.Response(approved))
	}
	if !approvedRequests {
		demo.Assistant("User did not approve any function calls.")
		return
	}
	// Pass the user input responses back to the agent for further processing.
	demo.Response(agent.RunMessage(ctx, a, message.New(userResponses...), agentopt.Thread(thread)))
}
