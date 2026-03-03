// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = os.Getenv("AZURE_OPENAI_API_VERSION")
var apiKey = os.Getenv("AZURE_OPENAI_API_KEY")

var logger = demo.NewLogger(
	"Function Tools With User Approvals",
	"Demonstrates how to use function tools that require user approval.",
	"Model", deployment,
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ tool.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	// Create Azure OpenAI agent.
	// Note that we are wrapping the function tool with tool.ApprovalRequiredFunc to require user approval before invoking it.
	a := openaichat.NewAgent(openaichat.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithAPIKey(apiKey),
		),
		Model: deployment,
		Agent: agent.Config{
			Instructions: "You are a helpful assistant",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
			RunOptions: []agentopt.Option{
				agentopt.Tool(tool.ApprovalRequiredFunc(weatherTool)),
			},
		},
	})

	ctx := context.Background()

	// Call the agent and check if there are any user input requests to handle.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText(ctx, "What is the weather like in Amsterdam?", agentopt.Session(session)).Collect()
	demo.Response(resp, err)

	var userResponses []message.Content
	var approvedRequests bool
	for c := range resp.Contents() {
		// Ask the user to approve each function call request.
		request, ok := c.(*message.FunctionApprovalRequestContent)
		if !ok {
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
	resp, err = a.RunMessage(ctx, message.New(userResponses...), agentopt.Session(session)).Collect()
	demo.Response(resp, err)
}
