// Copyright (c) Microsoft. All rights reserved.

package copilotprovider_test

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/copilotprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestE2E_RunTextReturnsResponse(t *testing.T) {
	client := newE2EClient(t)
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{
		SessionConfig: restrictedSessionConfig(nil),
	})
	session := newE2ESession(t, canaryAgent, client)

	response, err := canaryAgent.RunText(
		t.Context(),
		"What is 2 + 2? Answer with just the number.",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) == 0 {
		t.Fatal("response contains no messages")
	}
	if !strings.Contains(response.String(), "4") {
		t.Fatalf("response = %q, want it to contain 4", response.String())
	}
}

func TestE2E_RunTextStreamingReturnsUpdates(t *testing.T) {
	client := newE2EClient(t)
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{
		SessionConfig: restrictedSessionConfig(nil),
	})
	session := newE2ESession(t, canaryAgent, client)

	var responseText strings.Builder
	var updates int
	for update, err := range canaryAgent.RunText(
		t.Context(),
		"What is 2 + 2? Answer with just the number.",
		agent.WithSession(session),
		agent.Stream(true),
	) {
		if err != nil {
			t.Fatal(err)
		}
		updates++
		responseText.WriteString(update.String())
	}
	if updates == 0 {
		t.Fatal("stream contains no updates")
	}
	if !strings.Contains(responseText.String(), "4") {
		t.Fatalf("streamed response = %q, want it to contain 4", responseText.String())
	}
}

func TestE2E_RunTextInvokesFunctionTool(t *testing.T) {
	client := newE2EClient(t)
	var invoked atomic.Bool
	weatherTool := newE2EWeatherTool(&invoked)
	config := restrictedSessionConfig([]string{"get_weather"})
	config.OnPermissionRequest = copilot.PermissionHandler.ApproveAll
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{
		SessionConfig: config,
		Instructions:  "Always use the get_weather tool to answer weather questions.",
		Config: agent.Config{
			Tools: []tool.Tool{weatherTool},
		},
	})
	session := newE2ESession(t, canaryAgent, client)

	response, err := canaryAgent.RunText(
		t.Context(),
		"What is the weather like in Seattle?",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) == 0 {
		t.Fatal("response contains no messages")
	}
	if !invoked.Load() {
		t.Fatal("get_weather was not invoked")
	}
}

func TestE2E_RunTextWithApprovalRequiredTool(t *testing.T) {
	client := newE2EClient(t)
	var invoked atomic.Bool
	var permissionRequested atomic.Bool
	weatherTool := tool.ApprovalRequiredFunc(newE2EWeatherTool(&invoked))
	config := restrictedSessionConfig([]string{"get_weather"})
	config.OnPermissionRequest = func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
		permissionRequested.Store(true)
		return &rpc.PermissionDecisionApproveOnce{}, nil
	}
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{
		SessionConfig: config,
		Instructions:  "Always use the get_weather tool to answer weather questions.",
		Config: agent.Config{
			Tools: []tool.Tool{weatherTool},
		},
	})
	session := newE2ESession(t, canaryAgent, client)

	response, err := canaryAgent.RunText(
		t.Context(),
		"What is the weather like in Seattle?",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) == 0 {
		t.Fatal("response contains no messages")
	}
	if !permissionRequested.Load() {
		t.Fatal("get_weather did not request permission")
	}
	if !invoked.Load() {
		t.Fatal("get_weather was not invoked")
	}
}

func TestE2E_RunTextMaintainsSessionContext(t *testing.T) {
	client := newE2EClient(t)
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{
		SessionConfig: restrictedSessionConfig(nil),
		Instructions:  "Keep your answers short.",
	})
	session := newE2ESession(t, canaryAgent, client)

	ctx := t.Context()
	if _, err := canaryAgent.RunText(ctx, "My name is Alice.", agent.WithSession(session), agent.Stream(false)).Collect(); err != nil {
		t.Fatal(err)
	}
	response, err := canaryAgent.RunText(ctx, "What is my name?", agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(response.String()), "alice") {
		t.Fatalf("response = %q, want it to contain Alice", response.String())
	}
}

func TestE2E_RunTextResumesSession(t *testing.T) {
	client1 := newE2EClient(t)
	agent1 := copilotprovider.NewAgent(client1, copilotprovider.AgentConfig{
		SessionConfig: restrictedSessionConfig(nil),
		Instructions:  "Keep your answers short.",
	})
	session1 := newE2ESession(t, agent1, client1)

	ctx := t.Context()
	if _, err := agent1.RunText(ctx, "Remember this number: 42.", agent.WithSession(session1), agent.Stream(false)).Collect(); err != nil {
		t.Fatal(err)
	}
	sessionID := session1.ServiceID()
	if sessionID == "" {
		t.Fatal("session service ID is empty")
	}

	client2 := newE2EClient(t)
	agent2 := copilotprovider.NewAgent(client2, copilotprovider.AgentConfig{
		SessionConfig: restrictedSessionConfig(nil),
		Instructions:  "Keep your answers short.",
	})
	session2, err := agent2.CreateSession(ctx, agent.WithServiceID(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	response, err := agent2.RunText(ctx, "What number did I ask you to remember?", agent.WithSession(session2), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.String(), "42") {
		t.Fatalf("response = %q, want it to contain 42", response.String())
	}
}

func TestE2E_RunTextExecutesShellCommand(t *testing.T) {
	client := newE2EClient(t)
	config := restrictedSessionConfig(nil)
	config.OnPermissionRequest = copilot.PermissionHandler.ApproveAll
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{SessionConfig: config})
	session := newE2ESession(t, canaryAgent, client)

	response, err := canaryAgent.RunText(
		t.Context(),
		"Run a shell command to print 'hello world'.",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(response.String()), "hello") {
		t.Fatalf("response = %q, want it to contain hello", response.String())
	}
}

func TestE2E_RunTextFetchesURL(t *testing.T) {
	client := newE2EClient(t)
	config := restrictedSessionConfig(nil)
	config.OnPermissionRequest = copilot.PermissionHandler.ApproveAll
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{SessionConfig: config})
	session := newE2ESession(t, canaryAgent, client)

	response, err := canaryAgent.RunText(
		t.Context(),
		"Fetch https://learn.microsoft.com/agent-framework/tutorials/quick-start and summarize its contents in one sentence.",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(response.String()), "agent framework") {
		t.Fatalf("response = %q, want it to contain Agent Framework", response.String())
	}
}

func TestE2E_RunTextUsesLocalMCPServer(t *testing.T) {
	client := newE2EClient(t)
	config := restrictedSessionConfig(nil)
	config.OnPermissionRequest = copilot.PermissionHandler.ApproveAll
	config.MCPServers = map[string]copilot.MCPServerConfig{
		"filesystem": copilot.MCPStdioServerConfig{
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
			Tools:   []string{"*"},
		},
	}
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{SessionConfig: config})
	session := newE2ESession(t, canaryAgent, client)

	response, err := canaryAgent.RunText(
		t.Context(),
		"List the files in the current directory.",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(response.String()) == "" {
		t.Fatal("response is empty")
	}
}

func TestE2E_RunTextUsesRemoteMCPServer(t *testing.T) {
	t.Skip("remote MCP integration is disabled upstream")

	client := newE2EClient(t)
	config := restrictedSessionConfig(nil)
	config.OnPermissionRequest = copilot.PermissionHandler.ApproveAll
	config.MCPServers = map[string]copilot.MCPServerConfig{
		"microsoft-learn": copilot.MCPHTTPServerConfig{
			URL:   "https://learn.microsoft.com/api/mcp",
			Tools: []string{"*"},
		},
	}
	canaryAgent := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{SessionConfig: config})
	session := newE2ESession(t, canaryAgent, client)

	response, err := canaryAgent.RunText(
		t.Context(),
		"Search Microsoft Learn for 'Azure Functions' and summarize the top result.",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(response.String()), "azure functions") {
		t.Fatalf("response = %q, want it to contain Azure Functions", response.String())
	}
}

func newE2EWeatherTool(invoked *atomic.Bool) tool.FuncTool {
	return functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Get the weather for a location",
	}, func(_ context.Context, location string) (string, error) {
		invoked.Store(true)
		return "The weather in " + location + " is sunny with a high of 25C.", nil
	})
}

func newE2EClient(t *testing.T) *copilot.Client {
	t.Helper()
	skipUnlessE2EEnabled(t)

	options := &copilot.ClientOptions{}
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		options.GitHubToken = token
	}
	client := copilot.NewClient(options)
	if err := client.Start(t.Context()); err != nil {
		t.Fatalf("start Copilot client: %v", err)
	}
	t.Cleanup(client.ForceStop)
	return client
}

func skipUnlessE2EEnabled(t *testing.T) {
	t.Helper()
	actionsAuth := strings.EqualFold(os.Getenv("GITHUB_ACTIONS"), "true") && strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) != ""
	localOptIn := strings.EqualFold(os.Getenv("RUN_COPILOT_INTEGRATION_TESTS"), "true")
	if !actionsAuth && !localOptIn {
		t.Skip("GitHub Actions auth is unavailable and RUN_COPILOT_INTEGRATION_TESTS is not true")
	}
}

func restrictedSessionConfig(availableTools []string) *copilot.SessionConfig {
	return &copilot.SessionConfig{
		AvailableTools:                     availableTools,
		EnableConfigDiscovery:              new(false),
		EnableOnDemandInstructionDiscovery: new(false),
		EnableFileHooks:                    new(false),
		EnableHostGitOperations:            new(false),
		EnableSessionStore:                 new(false),
		EnableSkills:                       new(false),
		InfiniteSessions: &copilot.InfiniteSessionConfig{
			Enabled: new(false),
		},
	}
}

func newE2ESession(t *testing.T, canaryAgent *agent.Agent, client *copilot.Client) *agent.Session {
	t.Helper()
	session, err := canaryAgent.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		if sessionID := session.ServiceID(); sessionID != "" {
			if err := client.DeleteSession(context.Background(), sessionID); err != nil {
				t.Errorf("delete session: %v", err)
			}
		}
	})
	return session
}
