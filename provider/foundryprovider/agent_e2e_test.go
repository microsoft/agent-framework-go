// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3/conversations"
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

func TestE2E_ConformanceRunWithString(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	response, err := a.RunText(t.Context(), "What is the capital of France.", agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) != 1 || !strings.Contains(response.String(), "Paris") {
		t.Fatalf("response = %#v, want one Paris response", response)
	}
	if response.AgentID != a.ID() {
		t.Fatalf("AgentID = %q, want %q", response.AgentID, a.ID())
	}
}

func TestE2E_ConformanceRunWithChatMessage(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	response, err := a.Run(t.Context(), []*message.Message{message.NewText("What is the capital of France.")},
		agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) != 1 || !strings.Contains(response.String(), "Paris") {
		t.Fatalf("response = %#v, want one Paris response", response)
	}
}

func TestE2E_ConformanceRunWithChatMessages(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	response, err := a.Run(t.Context(), []*message.Message{message.NewText("Hello."), message.NewText("What is the capital of France.")},
		agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) != 1 || !strings.Contains(response.String(), "Paris") {
		t.Fatalf("response = %#v, want one Paris response", response)
	}
}

func TestE2E_ConformanceRunWithNoMessage(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	response, err := a.Run(t.Context(), nil, agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil {
		t.Fatal("response is nil")
	}
}

func TestE2E_ConformanceStreamingWithString(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	var text strings.Builder
	for update, err := range a.RunText(t.Context(), "What is the capital of France.", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(update.String())
	}
	if !strings.Contains(text.String(), "Paris") {
		t.Fatalf("streamed response = %q, want Paris", text.String())
	}
}

func TestE2E_ConformanceStreamingWithChatMessage(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	var text strings.Builder
	for update, err := range a.Run(t.Context(), []*message.Message{message.NewText("What is the capital of France.")},
		agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(update.String())
	}
	if !strings.Contains(text.String(), "Paris") {
		t.Fatalf("streamed response = %q, want Paris", text.String())
	}
}

func TestE2E_ConformanceStreamingWithChatMessages(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	var text strings.Builder
	for update, err := range a.Run(t.Context(), []*message.Message{message.NewText("Hello."), message.NewText("What is the capital of France.")},
		agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(update.String())
	}
	if !strings.Contains(text.String(), "Paris") {
		t.Fatalf("streamed response = %q, want Paris", text.String())
	}
}

func TestE2E_ConformanceStreamingWithNoMessage(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	for _, err := range a.Run(t.Context(), nil, agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestE2E_ConformanceRunSessionMaintainsHistory(t *testing.T) {
	history := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "You are a helpful assistant.", Config: agent.Config{HistoryProvider: history},
	})
	session := newE2EFoundrySession(t, a)
	first, err := a.RunText(t.Context(), "What is the capital of France.", agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.RunText(t.Context(), "And Austria?", agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "Paris") || !strings.Contains(second.String(), "Vienna") {
		t.Fatalf("responses = %q / %q, want Paris / Vienna", first.String(), second.String())
	}
	messages, err := history.Invoking(t.Context(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(session)}})
	if err != nil {
		t.Fatal(err)
	}
	assertE2EFoundryConformanceHistory(t, messages)
}

func TestE2E_ConformanceStreamingSessionMaintainsHistory(t *testing.T) {
	history := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "You are a helpful assistant.", Config: agent.Config{HistoryProvider: history},
	})
	session := newE2EFoundrySession(t, a)
	var first, second strings.Builder
	for update, err := range a.RunText(t.Context(), "What is the capital of France.", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		first.WriteString(update.String())
	}
	for update, err := range a.RunText(t.Context(), "And Austria?", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		second.WriteString(update.String())
	}
	if !strings.Contains(first.String(), "Paris") || !strings.Contains(second.String(), "Vienna") {
		t.Fatalf("responses = %q / %q, want Paris / Vienna", first.String(), second.String())
	}
	messages, err := history.Invoking(t.Context(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(session)}})
	if err != nil {
		t.Fatal(err)
	}
	assertE2EFoundryConformanceHistory(t, messages)
}

func TestE2E_ConformanceRunWithInstructionsAndNoMessage(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "ALWAYS RESPOND WITH 'Computer says no', even if there was no user input.",
	})
	session := newE2EFoundrySession(t, a)
	response, err := a.Run(t.Context(), nil, agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) != 1 || strings.TrimSpace(response.String()) == "" {
		t.Fatalf("response = %#v, want one nonempty response", response)
	}
}

func TestE2E_ConformanceStreamingWithInstructionsAndNoMessage(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "Always respond with 'Computer says no', even if there was no user input.",
	})
	session := newE2EFoundrySession(t, a)
	var text strings.Builder
	for update, err := range a.Run(t.Context(), nil, agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(update.String())
	}
	if !strings.Contains(strings.ToLower(text.String()), "computer says no") {
		t.Fatalf("streamed response = %q, want Computer says no", text.String())
	}
}

func TestE2E_ConformanceRunWithFunctions(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "You are a helpful assistant.", Config: agent.Config{Tools: newE2EFoundryMenuTools()},
	})
	session := newE2EFoundrySession(t, a)
	for _, question := range e2eFoundryMenuQuestions() {
		response, err := a.Run(t.Context(), []*message.Message{message.NewText(question.prompt)},
			agent.WithSession(session), agent.Stream(false)).Collect()
		if err != nil {
			t.Fatal(err)
		}
		if response == nil || !strings.Contains(response.String(), question.answer) {
			t.Fatalf("question %q: response = %#v, want %q", question.prompt, response, question.answer)
		}
	}
}

func TestE2E_ConformanceStreamingWithFunctions(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Instructions: "You are a helpful assistant.", Config: agent.Config{Tools: newE2EFoundryMenuTools()},
	})
	session := newE2EFoundrySession(t, a)
	for _, question := range e2eFoundryMenuQuestions() {
		var text strings.Builder
		for update, err := range a.Run(t.Context(), []*message.Message{message.NewText(question.prompt)},
			agent.WithSession(session), agent.Stream(true)) {
			if err != nil {
				t.Fatal(err)
			}
			text.WriteString(update.String())
		}
		if !strings.Contains(strings.ToLower(text.String()), strings.ToLower(question.answer)) {
			t.Fatalf("question %q: response = %q, want %q", question.prompt, text.String(), question.answer)
		}
	}
}

func TestE2E_ConformanceStructuredOutputWithGenericType(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	var city e2eFoundryCityInfo
	response, err := a.Run(t.Context(), []*message.Message{message.NewText("Provide information about the capital of France.")},
		agent.WithSession(session), agent.Stream(false), agent.WithStructuredOutput(&city)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) != 1 || !strings.Contains(response.String(), "Paris") || city.Name != "Paris" {
		t.Fatalf("response = %#v, city = %+v, want Paris", response, city)
	}
}

func TestE2E_ConformanceStructuredOutputWithPrimitiveType(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	var sum int
	response, err := a.Run(t.Context(), []*message.Message{message.NewText("What is the sum of 15 and 27? Respond with just the number.")},
		agent.WithSession(session), agent.Stream(false), agent.WithStructuredOutput(&sum)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) != 1 || sum != 42 {
		t.Fatalf("response = %#v, result = %d, want 42", response, sum)
	}
}

func TestE2E_ConformanceStructuredOutputWithResponseFormat(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{Instructions: "You are a helpful assistant."})
	session := newE2EFoundrySession(t, a)
	response, err := a.Run(t.Context(), []*message.Message{message.NewText("Provide information about the capital of France.")},
		agent.WithSession(session), agent.Stream(false), agent.WithResponseFormat(e2eFoundryCityFormat(t))).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var city e2eFoundryCityInfo
	if response == nil || len(response.Messages) != 1 || !strings.Contains(response.String(), "Paris") {
		t.Fatalf("response = %#v, want one Paris response", response)
	}
	if err := json.Unmarshal([]byte(response.String()), &city); err != nil {
		t.Fatal(err)
	}
	if city.Name != "Paris" {
		t.Fatalf("city = %+v, want Paris", city)
	}
}

func TestE2E_ResponseFormatAtAgentInitialization(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Config: agent.Config{RunOptions: []agent.Option{agent.WithResponseFormat(e2eFoundryCityFormat(t))}},
	})
	session := newE2EFoundrySession(t, a)
	response, err := a.Run(t.Context(), []*message.Message{message.NewText("Provide information about the capital of France.")},
		agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) != 1 || !strings.Contains(response.String(), "Paris") {
		t.Fatalf("response = %#v, want one Paris response", response)
	}
	var city e2eFoundryCityInfo
	if err := json.Unmarshal([]byte(response.String()), &city); err != nil {
		t.Fatal(err)
	}
	if city.Name != "Paris" {
		t.Fatalf("city = %+v, want Paris", city)
	}
}

func TestE2E_GenericRunWithResponseFormatAtAgentInitialization(t *testing.T) {
	a := newE2EFoundryAgent(t, foundryprovider.AgentConfig{
		Config: agent.Config{RunOptions: []agent.Option{agent.WithResponseFormat(e2eFoundryCityFormat(t))}},
	})
	session := newE2EFoundrySession(t, a)
	var city e2eFoundryCityInfo
	response, err := a.Run(t.Context(), []*message.Message{message.NewText("Provide information about the capital of France.")},
		agent.WithSession(session), agent.Stream(false), agent.WithStructuredOutput(&city)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || len(response.Messages) != 1 || !strings.Contains(response.String(), "Paris") || city.Name != "Paris" {
		t.Fatalf("response = %#v, city = %+v, want Paris", response, city)
	}
}

func TestE2E_AgentOptionsAndConversationRun(t *testing.T) {
	skipUnlessFoundryE2EEnabled(t)
	endpoint, model := strings.TrimSpace(os.Getenv("FOUNDRY_PROJECT_ENDPOINT")), strings.TrimSpace(os.Getenv("FOUNDRY_MODEL"))
	if endpoint == "" || model == "" {
		t.Fatal("FOUNDRY_PROJECT_ENDPOINT and FOUNDRY_MODEL are required")
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := foundryprovider.NewOpenAIClient(endpoint, credential)
	conversation, err := client.Conversations.New(t.Context(), conversations.ConversationNewParams{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := client.Conversations.Delete(context.WithoutCancel(t.Context()), conversation.ID); err != nil {
			t.Errorf("delete conversation: %v", err)
		}
	})
	a := foundryprovider.NewAgent(endpoint, credential, foundryprovider.ModelDeployment(model), foundryprovider.AgentConfig{
		Instructions: "You are a helpful assistant. When asked for verification, reply with exactly 'integration-options-ok'.",
		Config: agent.Config{
			Name:        "ResponsesAgentExtensionOptions",
			Description: "Integration test agent created from AIProjectClient.AsAIAgent(options).",
		},
	})
	session, err := a.CreateSession(t.Context(), agent.WithServiceID(conversation.ID))
	if err != nil {
		t.Fatal(err)
	}
	response, err := a.RunText(t.Context(), "Return the verification token.", agent.WithSession(session), agent.Stream(false)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.ToLower(session.ServiceID()), "conv_") ||
		a.Name() != "ResponsesAgentExtensionOptions" ||
		a.Description() != "Integration test agent created from AIProjectClient.AsAIAgent(options)." ||
		!strings.Contains(strings.ToLower(response.String()), "integration-options-ok") {
		t.Fatalf("agent/session/response = %q / %q / %q", a.Name(), session.ServiceID(), response.String())
	}
}

type e2eFoundryCityInfo struct {
	Name string `json:"name"`
}

func e2eFoundryCityFormat(t *testing.T) agent.ResponseFormat {
	t.Helper()
	format, err := jsonformat.ForType(reflect.TypeFor[e2eFoundryCityInfo]())
	if err != nil {
		t.Fatal(err)
	}
	return format
}

func newE2EFoundrySession(t *testing.T, a *agent.Agent) *agent.Session {
	t.Helper()
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func assertE2EFoundryConformanceHistory(t *testing.T, messages []*message.Message) {
	t.Helper()
	if len(messages) != 4 {
		t.Fatalf("history length = %d, want 4", len(messages))
	}
	for i, role := range []message.Role{message.RoleUser, message.RoleAssistant, message.RoleUser, message.RoleAssistant} {
		if messages[i].Role != role {
			t.Errorf("history[%d].Role = %q, want %q", i, messages[i].Role, role)
		}
	}
	if messages[0].String() != "What is the capital of France." || messages[2].String() != "And Austria?" ||
		!strings.Contains(messages[1].String(), "Paris") || !strings.Contains(messages[3].String(), "Vienna") {
		t.Fatalf("history = %q / %q / %q / %q", messages[0].String(), messages[1].String(), messages[2].String(), messages[3].String())
	}
}

func newE2EFoundryMenuTools() []tool.Tool {
	return []tool.Tool{
		functool.MustNew(functool.Config{Name: "GetSpecials", Description: "Provides a list of specials from the menu."},
			func(context.Context, struct{}) (string, error) {
				return "Special Soup: Clam Chowder\nSpecial Salad: Cobb Salad\nSpecial Drink: Chai Tea", nil
			}),
		functool.MustNew(functool.Config{Name: "GetItemPrice", Description: "Provides the price of the requested menu item."},
			func(context.Context, string) (string, error) { return "$9.99", nil }),
	}
}

func e2eFoundryMenuQuestions() []struct{ prompt, answer string } {
	return []struct{ prompt, answer string }{
		{"Hello", ""},
		{"What is the special soup?", "Clam Chowder"},
		{"What is the special drink?", "Chai Tea"},
		{"What is the special salad?", "Cobb Salad"},
		{"Thank you", ""},
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
