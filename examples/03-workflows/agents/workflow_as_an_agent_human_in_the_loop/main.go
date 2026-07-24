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
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
)

var logger = demo.NewLogger(
	"Workflow as an Agent with Human-in-the-Loop",
	"This sample wraps a workflow whose hosted agent requires tool approval, resumed across the agent boundary.",
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

	// The hosted agent's function tool is wrapped with tool.ApprovalRequiredFunc,
	// so the workflow raises a ToolApprovalRequestContent before the tool runs.
	assistant := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant. Use the weather tool to answer questions about the weather.",
			Config: agent.Config{
				Name:        "WeatherAssistant",
				Middlewares: []agent.Middleware{logger},
				Tools:       []tool.Tool{tool.ApprovalRequiredFunc(weatherTool)},
			},
		},
	)

	wf, err := agentworkflow.NewSequentialWorkflowBuilder(assistant).
		WithName("weather-approval-workflow").
		Build()
	if err != nil {
		demo.Panic(err)
	}

	// Wrapping the workflow as an agent surfaces the workflow's pending
	// approval request as a ResponseUpdate carrying ToolApprovalRequestContent.
	// The caller resumes by passing the matching ToolApprovalResponseContent in
	// the next run on the SAME session; the provider routes it back into the
	// workflow as an ExternalResponse to complete the turn.
	wfAgent, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
		Config: agent.Config{
			Name: "WeatherWorkflowAgent",
		},
	})
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()
	session, err := wfAgent.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	demo.Assistant("User: What is the weather like in Amsterdam?")
	resp, err := wfAgent.RunText(ctx, "What is the weather like in Amsterdam?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	// Detect the approval request surfaced across the NewAgent boundary and
	// prompt the user for each pending tool call.
	var userResponses []message.Content
	var approvedRequests bool
	for c := range resp.Contents() {
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
	if len(userResponses) == 0 {
		demo.Assistant("No approval requests were raised by the workflow.")
		return
	}
	if !approvedRequests {
		demo.Assistant("User did not approve any function calls.")
		return
	}

	// Resume the SAME session with the approval responses so the workflow can
	// finish the turn and produce the final answer.
	resp, err = wfAgent.RunMessage(ctx, message.New(userResponses...), agent.WithSession(session)).Collect()
	demo.Response(resp, err)
}
