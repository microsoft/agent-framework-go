// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Function Tools With User Approvals",
	"Demonstrates how to use function tools that require user approval.",
	"Model", demo.FoundryModel,
)

var weatherTool = functool.MustNew(functool.Config{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	token := demo.FoundryTokenCredential()

	// Create Microsoft Foundry agent.
	// Note that we are wrapping the function tool with tool.ApprovalRequiredFunc to require user approval before invoking it.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant",
			Config: agent.Config{
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
				Tools:       []tool.Tool{tool.ApprovalRequiredFunc(weatherTool)},
			},
		},
	)

	ctx := context.Background()

	// Call the agent and check if there are any user input requests to handle.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText(ctx, "What is the weather like in Amsterdam?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	var userResponses []message.Content
	var approvedRequests bool
	for c := range resp.Contents() {
		// Ask the user to approve each function call request.
		request, ok := c.(*message.ToolApprovalRequestContent)
		if !ok {
			continue
		}
		approved := demo.UserInputRequest(request)
		if approved {
			approvedRequests = true
		}
		userResponses = append(userResponses, request.CreateResponse(approved, ""))
	}
	if !approvedRequests {
		demo.Assistant("User did not approve any function calls.")
		return
	}
	// Pass the user input responses back to the agent for further processing.
	resp, err = a.RunMessage(ctx, message.New(userResponses...), agent.WithSession(session)).Collect()
	demo.Response(resp, err)
}
