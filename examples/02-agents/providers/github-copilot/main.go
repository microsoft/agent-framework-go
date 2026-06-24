// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/copilotagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
)

var logger = demo.NewLogger(
	"GitHub Copilot Agent",
	"Demonstrates a GitHub Copilot-backed agent with shell command permissions.",
)

func main() {
	ctx := context.Background()

	// Create and start a Copilot client. The SDK uses the bundled Copilot CLI by default,
	// or COPILOT_CLI_PATH when set.
	copilotClient := copilot.NewClient(nil)
	if err := copilotClient.Start(ctx); err != nil {
		demo.Panicf("failed to start GitHub Copilot client: %v", err)
	}
	defer func() { _ = copilotClient.Stop() }()

	a := copilotagent.New(
		copilotClient,
		copilotagent.Config{
			SessionConfig: &copilot.SessionConfig{
				OnPermissionRequest: promptPermission,
			},
			Config: agent.Config{
				Name:        "GitHub Copilot Agent",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)

	for update, err := range a.RunText(ctx, "List all files in the current directory", agent.Stream(true)) {
		if err != nil {
			demo.Panic(err)
		}
		fmt.Print(update)
	}
}

func promptPermission(request copilot.PermissionRequest, _ copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
	fmt.Printf("\n[Permission Request: %s]\n", request.Kind())
	fmt.Print("Approve? (y/n): ")

	input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	input = strings.TrimSpace(strings.ToUpper(input))
	if input == "Y" || input == "YES" {
		return &rpc.PermissionDecisionApproveOnce{}, nil
	}
	return &rpc.PermissionDecisionReject{}, nil
}
