// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates how to use an Agent with function tools that require a human in the loop for approvals.
// It shows both non-streaming and streaming agent interactions using menu-related tools.
// If the agent is hosted in a service, with a remote user, combine this sample with the Persisted Conversations sample to persist the chat history
// while the agent is waiting for user input.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
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
	}, chatagent.Options{
		Instructions: "You are a helpful assistant",
		ChatOptions: &chatclient.ChatOptions{
			Tools: []tool.Tool{tool.ApprovalRequiredFunc(weatherTool)},
		},
	})

	ctx := context.Background()

	// Call the agent and check if there are any user input requests to handle.
	thread := a.NewThread()
	resp, err := agent.RunText(ctx, a, "What is the weather like in Amsterdam?", agent.WithThread(thread))
	if err != nil {
		panic(err)
	}

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
	fmt.Println(agent.Run(ctx, a, agent.WithMessage(message.New(userResponses...)), agent.WithThread(thread)))
}
