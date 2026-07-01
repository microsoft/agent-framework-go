// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to pair the shell tool with an environment-aware context provider.
//
// WARNING: This sample executes real shell commands on this machine. Approval gating is disabled
// so the demo can run unattended. In a real application, keep approval enabled or use a
// container-backed executor when isolation matters.

package main

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/shelltool"
)

var logger = demo.NewLogger(
	"Shell With Environment",
	"Demonstrates shell tool calls with live shell environment instructions.",
	"Model", demo.FoundryModel,
)

const instructions = `You are an agent with a single tool: run_shell. Use it to satisfy the user's request.
Do not describe what you would do; actually run the commands. Reply with the final answer derived from real output.`

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	fmt.Println("### Stateless mode")
	fmt.Println()
	runShellEnvironmentDemo(ctx, token, shelltool.ModeStateless, []string{
		"Print the current working directory.",
		"Change directory into the system temp folder, then print the current working directory.",
		"In a NEW shell call, print the current working directory again. Tell me whether it matches the temp folder from the previous call.",
	})

	fmt.Println()
	fmt.Println("### Persistent mode")
	fmt.Println()
	runShellEnvironmentDemo(ctx, token, shelltool.ModePersistent, []string{
		"Change directory into the system temp folder, then print the current working directory.",
		"In a NEW shell call, print the current working directory again. Tell me whether it still matches the temp folder.",
		"Set the environment variable DEMO_TOKEN to the value 'hello-world'.",
		"Print the current value of DEMO_TOKEN. Tell me exactly what value the shell reports.",
	})
}

func runShellEnvironmentDemo(ctx context.Context, token azcore.TokenCredential, mode shelltool.Mode, prompts []string) {
	shell, err := shelltool.NewLocal(shelltool.LocalConfig{
		Mode:              mode,
		AcknowledgeUnsafe: true,
	})
	if err != nil {
		demo.Panic(err)
	}
	defer func() {
		if err := shell.Close(); err != nil {
			demo.Panic(err)
		}
	}()

	envProvider := shelltool.NewEnvironmentProvider(shell, shelltool.EnvironmentProviderConfig{})
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: instructions,
			Config: agent.Config{
				Tools:            []tool.Tool{shell},
				ContextProviders: []agent.ContextProvider{envProvider},
				Middlewares:      []agent.Middleware{logger},
			},
		},
	)

	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	for _, prompt := range prompts {
		resp, err := a.RunText(ctx, prompt, agent.WithSession(session)).Collect()
		demo.Response(resp, err)
		fmt.Println()
	}

	if snapshot, ok := envProvider.CurrentSnapshot(); ok {
		printSnapshot(snapshot)
	}
}

func printSnapshot(snapshot shelltool.ShellEnvironmentSnapshot) {
	fmt.Println("--- Captured environment snapshot ---")
	fmt.Printf("  Family:  %s\n", snapshot.Family)
	fmt.Printf("  OS:      %s\n", snapshot.OSDescription)
	fmt.Printf("  Shell:   %s\n", valueOrUnknown(snapshot.ShellVersion))
	fmt.Printf("  CWD:     %s\n", snapshot.WorkingDirectory)
	for toolName, version := range snapshot.ToolVersions {
		if !version.Found {
			fmt.Printf("  %-8s %s\n", toolName, "(not installed)")
			continue
		}
		fmt.Printf("  %-8s %s\n", toolName, version.Version)
	}
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "(unknown)"
	}
	return value
}
