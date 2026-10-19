// Copyright (c) Microsoft. All rights reserved.

// This sample uses the reusable harness setup with a normal Foundry agent.
// Agent, tool, and individual model-call tracing are enabled.
// Reference: https://github.com/microsoft/agent-framework/blob/ebcf4db7f9462ef6c377761e0b3159c4c5a73027/dotnet/src/Microsoft.Agents.AI.Harness/HarnessAgent.cs
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Composite Harness",
	"Uses preconfigured harness components on one agent, with session-isolated "+
		"file memory, local history, skills, approvals, web search, and agent tracing.",
	"Model", demo.FoundryModel,
)

func main() {
	if err := run(); err != nil {
		demo.Panic(err)
	}
}

func run() (err error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	h, err := harness.New(harness.Config{WorkingDirectory: workingDirectory})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, h.Close()) }()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		demo.FoundryTokenCredential(),
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Config: h.Configure(agent.Config{
				Name:        "HarnessAssistant",
				Middlewares: []agent.Middleware{logger},
			}),
			DisableStoreOutput: true,
			Instructions:       "Help with software engineering tasks. Use memory and todos to track the current session's work.",
		},
	)

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Memory root: %s\n\n", filepath.Join(workingDirectory, "agent-file-memory"))
	for _, prompt := range []string{
		"Our project uses Go and prefers small changes. Save that in project.md with a description, then plan a health-check endpoint and record the tasks as todos.",
		"Switch to execute mode. Read project.md, review the todo list, and draft the endpoint design. Do not claim to have edited any project files.",
	} {
		if err := runPrompt(ctx, a, session, prompt); err != nil {
			return err
		}
	}

	newSession, err := a.CreateSession(ctx)
	if err != nil {
		return err
	}
	return runPrompt(ctx, a, newSession,
		"This is a new session. List the memory files and todos available here. Do not create any new ones.")
}

func runPrompt(ctx context.Context, a *agent.Agent, session *agent.Session, prompt string) error {
	response, err := a.RunText(ctx, prompt, agent.WithSession(session)).Collect()
	for {
		if err != nil {
			return err
		}
		demo.Response(response, nil)
		var approvals []message.Content
		for content := range response.Contents() {
			if request, ok := content.(*message.ToolApprovalRequestContent); ok {
				approvals = append(approvals, request.CreateResponse(demo.UserInputRequest(request), ""))
			}
		}
		if len(approvals) == 0 {
			return nil
		}
		response, err = a.RunMessage(ctx, message.New(approvals...), agent.WithSession(session)).Collect()
	}
}
