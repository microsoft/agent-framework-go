// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/copilotprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const canaryResult = "agent-framework-go-copilot-tool-ok"

type canaryStatusInput struct{}

var canaryStatusTool = functool.MustNew(functool.Config{
	Name:        "canary_status",
	Description: "Return the fixed status for the Agent Framework Go Copilot canary",
}, func(context.Context, canaryStatusInput) (string, error) {
	return canaryResult, nil
})

func main() {
	ctx := context.Background()
	copilotClient := copilot.NewClient(&copilot.ClientOptions{
		GitHubToken: os.Getenv("COPILOT_GITHUB_TOKEN"),
	})
	if err := copilotClient.Start(ctx); err != nil {
		demo.Panicf("failed to start GitHub Copilot client: %v", err)
	}
	defer func() { _ = copilotClient.Stop() }()

	canaryAgent := copilotprovider.NewAgent(
		copilotClient,
		copilotprovider.AgentConfig{
			SessionConfig: &copilot.SessionConfig{
				AvailableTools:                     []string{"canary_status"},
				EnableConfigDiscovery:              copilot.Bool(false),
				EnableOnDemandInstructionDiscovery: copilot.Bool(false),
				EnableFileHooks:                    copilot.Bool(false),
				EnableHostGitOperations:            copilot.Bool(false),
				EnableSessionStore:                 copilot.Bool(false),
				EnableSkills:                       copilot.Bool(false),
				InfiniteSessions: &copilot.InfiniteSessionConfig{
					Enabled: copilot.Bool(false),
				},
			},
			Instructions: "Call canary_status exactly once, then include its result verbatim in your final response.",
			Config: agent.Config{
				Name:  "CopilotCanary",
				Tools: []tool.Tool{canaryStatusTool},
			},
		},
	)

	response, err := canaryAgent.RunText(ctx, "Check the canary status.").Collect()
	demo.Response(response, err)
}
