// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/internal/agenttest"
)

func TestAgent_BasicRun(t *testing.T) {
	client := agenttest.NewClient()

	a := agent.New(client, &agent.Config{
		ID:   "test-agent",
		Name: "Test Agent",
	}, nil)

	resp, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Hello"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response, got nil")
	}

	if client.GetRunCallCount() != 1 {
		t.Errorf("expected 1 call to Run, got %d", client.GetRunCallCount())
	}

	lastCall := client.GetLastRunCall()
	if lastCall == nil {
		t.Fatal("expected last call to be recorded")
	}
	if want := a.ID(); lastCall.Config.ID != want {
		t.Errorf("expected config ID %q, got %q", want, lastCall.Config.ID)
	}
}

func TestAgent_CustomResponse(t *testing.T) {
	client := agenttest.NewClient()

	const respTest = "Custom response text"

	customResponse := &agent.RunResponse{
		AgentID:    "custom-agent",
		ResponseID: "custom-response",
		Messages: []*agent.Message{
			agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respTest}),
		},
	}
	client.SetResponse(customResponse)

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp.Text() != respTest {
		t.Errorf("expected %q, got %q", respTest, resp.Text())
	}
}

func TestAgent_ErrorHandling(t *testing.T) {
	client := agenttest.NewClient()

	expectedError := errors.New("an error")
	client.SetError(expectedError)

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	_, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != expectedError {
		t.Errorf("expected error %v, got %v", expectedError, err)
	}
}

func TestAgent_RunStream(t *testing.T) {
	client := agenttest.NewClient()

	client.SetStreamUpdates([]*agent.RunResponseUpdate{
		{
			Role:     agent.RoleAssistant,
			Contents: []agent.Content{&agent.TextContent{Text: "Hello "}},
		},
		{
			Role:     agent.RoleAssistant,
			Contents: []agent.Content{&agent.TextContent{Text: "world!"}},
		},
	})

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	var receivedUpdates []*agent.RunResponseUpdate
	for update, err := range a.RunStream(t.Context(), nil, nil, agent.NewTextMessage("Test")) {
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		receivedUpdates = append(receivedUpdates, update)
	}

	if len(receivedUpdates) < 1 {
		t.Errorf("expected at least 1 update, got %d", len(receivedUpdates))
	}

	if client.GetRunStreamCallCount() == 0 {
		t.Error("expected RunStream to be called")
	}
}

func TestAgent_ResponseSequence(t *testing.T) {
	client := agenttest.NewClient()

	const respText1 = "First response"
	const respText2 = "Second response"

	responses := []*agent.RunResponse{
		{
			Messages: []*agent.Message{agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respText1})},
		},
		{
			Messages: []*agent.Message{agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respText2})},
		},
	}
	client.WithResponseSequence(responses...)

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp1, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test 1"))
	if err != nil {
		t.Fatalf("expected no error on first call, got: %v", err)
	}
	if resp1.Text() != respText1 {
		t.Errorf("expected %q, got %q", respText1, resp1.Text())
	}

	resp2, err := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test 2"))
	if err != nil {
		t.Fatalf("expected no error on second call, got: %v", err)
	}
	if resp2.Text() != respText2 {
		t.Errorf("expected %q, got %q", respText2, resp2.Text())
	}
}

func TestAgent_WithToolCalls(t *testing.T) {
	client := agenttest.NewClient()

	const respText = "The weather in Seattle is sunny"

	toolCalls := []*agent.FunctionCallContent{
		{
			Name:      "get_weather",
			Arguments: `{"location": "Seattle"}`,
		},
	}
	client.WithToolCalls(toolCalls, respText)

	tool := &agenttest.Tool{
		NameValue: "get_weather",
		CallFunc: func(ctx context.Context, args map[string]any) (any, error) {
			return map[string]any{"temperature": "72F", "condition": "sunny"}, nil
		},
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp, err := a.Run(t.Context(), nil, &agent.RunOptions{
		Tools: []agent.Tool{tool},
	}, agent.NewTextMessage("What's the weather?"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if resp.Text() != respText {
		t.Errorf("expected final response %q, got %q", respText, resp.Text())
	}

	if client.GetRunCallCount() < 2 {
		t.Errorf("expected at least 2 calls to Run, got %d", client.GetRunCallCount())
	}

	if tool.CallCount != 1 {
		t.Errorf("expected tool to be called once, got %d", tool.CallCount)
	}
}

func TestAgent_CustomFunction(t *testing.T) {
	client := agenttest.NewClient()

	const respText1 = "Please confirm"
	const respText2 = "Confirmed!"

	callCount := 0
	client.RunFunc = func(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
		callCount++
		if callCount == 1 {
			return &agent.RunResponse{
				AgentID:    config.ID,
				ResponseID: "resp-1",
				Messages:   []*agent.Message{agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respText1})},
			}, nil
		}
		return &agent.RunResponse{
			AgentID:    config.ID,
			ResponseID: "resp-2",
			Messages:   []*agent.Message{agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: respText2})},
		}, nil
	}

	a := agent.New(client, &agent.Config{ID: "test-agent"}, nil)

	resp1, _ := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Test"))
	if resp1.Text() != respText1 {
		t.Errorf("expected %q, got %q", respText1, resp1.Text())
	}

	resp2, _ := a.Run(t.Context(), nil, nil, agent.NewTextMessage("Yes"))
	if resp2.Text() != respText2 {
		t.Errorf("expected %q, got %q", respText2, resp2.Text())
	}
}
