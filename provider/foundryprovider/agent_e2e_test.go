// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestE2E_RunTextReturnsResponse(t *testing.T) {
	foundryAgent := newE2EFoundryAgent(t, foundryprovider.AgentConfig{})

	response, err := foundryAgent.RunText(
		t.Context(),
		"What is the capital of France? Answer with just the city name.",
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) == 0 {
		t.Fatal("response contains no messages")
	}
	if !strings.Contains(strings.ToLower(response.String()), "paris") {
		t.Fatalf("response = %q, want it to contain Paris", response.String())
	}
}

func TestE2E_RunTextStreamingReturnsUpdates(t *testing.T) {
	foundryAgent := newE2EFoundryAgent(t, foundryprovider.AgentConfig{})

	var responseText strings.Builder
	var updates int
	for update, err := range foundryAgent.RunText(
		t.Context(),
		"What is the capital of France? Answer with just the city name.",
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
	if !strings.Contains(strings.ToLower(responseText.String()), "paris") {
		t.Fatalf("streamed response = %q, want it to contain Paris", responseText.String())
	}
}

func TestE2E_RunTextInvokesFunctionTool(t *testing.T) {
	var invoked atomic.Bool
	weatherTool := newE2EFoundryWeatherTool(&invoked)
	foundryAgent := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "Always use the get_weather tool to answer weather questions.",
		Config: agent.Config{
			Tools: []tool.Tool{weatherTool},
		},
	})

	response, err := foundryAgent.RunText(
		t.Context(),
		"What is the weather like in Seattle?",
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

func TestE2E_RunTextMaintainsSessionContext(t *testing.T) {
	foundryAgent := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "Keep your answers short.",
	})
	session, err := foundryAgent.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := foundryAgent.RunText(
		t.Context(),
		"Remember that my name is Alice.",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect(); err != nil {
		t.Fatal(err)
	}
	response, err := foundryAgent.RunText(
		t.Context(),
		"What is my name? Answer with just the name.",
		agent.WithSession(session),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(response.String()), "alice") {
		t.Fatalf("response = %q, want it to contain Alice", response.String())
	}
}

func TestE2E_RunTextReturnsStructuredOutput(t *testing.T) {
	type cityInfo struct {
		City    string `json:"city"`
		Country string `json:"country"`
	}

	foundryAgent := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "Return the requested city information.",
	})
	var city cityInfo
	response, err := foundryAgent.RunText(
		t.Context(),
		"Provide the city and country for the capital of France.",
		agent.WithStructuredOutput(&city),
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) == 0 {
		t.Fatal("response contains no messages")
	}
	if !strings.EqualFold(city.City, "Paris") || !strings.EqualFold(city.Country, "France") {
		t.Fatalf("structured output = %+v, want Paris, France", city)
	}
}

func TestE2E_ServerAgentRunTextReturnsResponse(t *testing.T) {
	skipUnlessFoundryE2EEnabled(t)
	agentName := strings.TrimSpace(os.Getenv("FOUNDRY_AGENT_NAME"))
	if agentName == "" {
		t.Skip("FOUNDRY_AGENT_NAME is not set")
	}
	foundryAgent := newE2EFoundryAgentForTarget(
		t,
		foundryprovider.ServerAgent(agentName),
		foundryprovider.AgentConfig{},
	)

	response, err := foundryAgent.RunText(
		t.Context(),
		"What is the capital of France? Answer with just the city name.",
		agent.Stream(false),
	).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) == 0 {
		t.Fatal("response contains no messages")
	}
	if !strings.Contains(strings.ToLower(response.String()), "paris") {
		t.Fatalf("response = %q, want it to contain Paris", response.String())
	}
}

func newE2EFoundryWeatherTool(invoked *atomic.Bool) tool.FuncTool {
	return functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Get the weather for a location",
	}, func(_ context.Context, location string) (string, error) {
		invoked.Store(true)
		return "The weather in " + location + " is sunny with a high of 25C.", nil
	})
}

func newE2EFoundryAgent(t *testing.T, config foundryprovider.AgentConfig) *agent.Agent {
	t.Helper()
	skipUnlessFoundryE2EEnabled(t)

	model := strings.TrimSpace(os.Getenv("FOUNDRY_MODEL"))
	if model == "" {
		t.Fatal("FOUNDRY_MODEL is required when RUN_FOUNDRY_INTEGRATION_TESTS is true")
	}
	return newE2EFoundryAgentForTarget(t, foundryprovider.ModelDeployment(model), config)
}

func newE2EFoundryAgentForTarget(t *testing.T, target foundryprovider.AgentTarget, config foundryprovider.AgentConfig) *agent.Agent {
	t.Helper()
	skipUnlessFoundryE2EEnabled(t)

	endpoint := strings.TrimSpace(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"))
	if endpoint == "" {
		t.Fatal("FOUNDRY_PROJECT_ENDPOINT is required when RUN_FOUNDRY_INTEGRATION_TESTS is true")
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("create Azure credential: %v", err)
	}
	// Keep test runs stateless so shared Foundry projects do not accumulate response records.
	config.DisableStoreOutput = true

	return foundryprovider.NewAgent(
		endpoint,
		credential,
		target,
		config,
	)
}

func skipUnlessFoundryE2EEnabled(t *testing.T) {
	t.Helper()
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("RUN_FOUNDRY_INTEGRATION_TESTS")), "true") {
		t.Skip("RUN_FOUNDRY_INTEGRATION_TESTS is not true")
	}
}
