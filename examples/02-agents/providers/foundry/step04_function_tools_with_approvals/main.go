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
	"Foundry Function Tools With User Approvals",
	"Demonstrates function tools that require user approval with a Foundry agent.",
	"Model", demo.FoundryModel,
)

var weatherTool = functool.MustNew(functool.Config{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that can get weather information.",
			Config: agent.Config{
				Name:        "WeatherAssistant",
				Middlewares: []agent.Middleware{logger},
				Tools:       []tool.Tool{tool.ApprovalRequiredFunc(weatherTool)},
			},
		},
	)

	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	resp, err := a.RunText(ctx, "What is the weather like in Amsterdam?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	for {
		var userResponses []message.Content
		for c := range resp.Contents() {
			request, ok := c.(*message.ToolApprovalRequestContent)
			if !ok {
				continue
			}
			approved := demo.UserInputRequest(request)
			userResponses = append(userResponses, request.CreateResponse(approved, ""))
		}
		if len(userResponses) == 0 {
			return
		}
		resp, err = a.RunMessage(ctx, message.New(userResponses...), agent.WithSession(session)).Collect()
		demo.Response(resp, err)
	}
}
