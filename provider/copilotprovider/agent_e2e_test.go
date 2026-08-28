// Copyright (c) Microsoft. All rights reserved.

package copilotprovider_test

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
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

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	response, err := canaryAgent.RunText(
		ctx,
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

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	var responseText strings.Builder
	var updates int
	for update, err := range canaryAgent.RunText(
		ctx,
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
	weatherTool := functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Get the weather for a location",
	}, func(_ context.Context, location string) (string, error) {
		invoked.Store(true)
		return "The weather in " + location + " is sunny with a high of 25C.", nil
	})
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

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	response, err := canaryAgent.RunText(
		ctx,
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

func newE2EClient(t *testing.T) *copilot.Client {
	t.Helper()
	skipUnlessE2EEnabled(t)

	options := &copilot.ClientOptions{}
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		options.GitHubToken = token
	}
	client := copilot.NewClient(options)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
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
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := client.DeleteSession(ctx, sessionID); err != nil {
				t.Errorf("delete session: %v", err)
			}
		}
	})
	return session
}
