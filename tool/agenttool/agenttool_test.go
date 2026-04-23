// Copyright (c) Microsoft. All rights reserved.

package agenttool_test

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/agenttool"
)

func TestNew_ExposesAgentMetadataAndSchemas(t *testing.T) {
	a := agenttest.New(agenttest.NewResponseBuilder().AddText("ok").Build())
	tl := agenttool.New(a, agenttool.Config{})

	if got := tl.Name(); got != "TestAgent" {
		t.Fatalf("Name() = %q, want %q", got, "TestAgent")
	}
	if got := tl.Description(); got != "A test agent" {
		t.Fatalf("Description() = %q, want %q", got, "A test agent")
	}

	schema, ok := tl.Schema().(map[string]any)
	if !ok {
		t.Fatalf("Schema() type = %T, want map[string]any", tl.Schema())
	}
	if got := schema["type"]; got != "object" {
		t.Fatalf("Schema()[\"type\"] = %#v, want %q", got, "object")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Schema()[\"properties\"] type = %T, want map[string]any", schema["properties"])
	}
	query, ok := properties["query"].(map[string]any)
	if !ok {
		t.Fatalf("properties[\"query\"] type = %T, want map[string]any", properties["query"])
	}
	if got := query["type"]; got != "string" {
		t.Fatalf("query schema type = %#v, want %q", got, "string")
	}
	if got := query["description"]; got != "input query to invoke the agent" {
		t.Fatalf("query schema description = %#v, want %q", got, "input query to invoke the agent")
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("Schema()[\"required\"] type = %T, want []string", schema["required"])
	}
	if len(required) != 1 || required[0] != "query" {
		t.Fatalf("required = %#v, want []string{\"query\"}", required)
	}

	returnSchema, ok := tl.ReturnSchema().(map[string]any)
	if !ok {
		t.Fatalf("ReturnSchema() type = %T, want map[string]any", tl.ReturnSchema())
	}
	if got := returnSchema["type"]; got != "string" {
		t.Fatalf("ReturnSchema()[\"type\"] = %#v, want %q", got, "string")
	}
}

func TestCall_PassesQueryAndRunOptionsAndReturnsResponse(t *testing.T) {
	var capturedMessages []*message.Message
	var capturedOptions []agent.Option

	a := agenttest.New(agenttest.NewResponseBuilder(
		func(_ context.Context, messages []*message.Message, opts ...agent.Option) {
			capturedMessages = messages
			capturedOptions = opts
		},
	).AddText("agent response").Build())

	tl := agenttool.New(a, agenttool.Config{
		RunOptions: []agent.Option{agent.Stream(false)},
	})

	ret, err := tl.Call(tool.Context{Context: t.Context()}, `{"query":"hello"}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got, ok := ret.(string); !ok || got != "agent response" {
		t.Fatalf("Call() result = %#v, want %q", ret, "agent response")
	}

	if len(capturedMessages) != 1 {
		t.Fatalf("captured %d messages, want 1", len(capturedMessages))
	}
	if capturedMessages[0].Role != message.RoleUser {
		t.Fatalf("message role = %q, want %q", capturedMessages[0].Role, message.RoleUser)
	}
	text, ok := capturedMessages[0].Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("message content type = %T, want *message.TextContent", capturedMessages[0].Contents[0])
	}
	if text.Text != "hello" {
		t.Fatalf("message text = %q, want %q", text.Text, "hello")
	}

	stream, ok := agent.GetOption(capturedOptions, agent.Stream)
	if !ok {
		t.Fatal("expected configured run options to be forwarded")
	}
	if stream {
		t.Fatal("stream option = true, want false")
	}
}

func TestCall_EmptyArgsUsesEmptyQuery(t *testing.T) {
	var capturedMessages []*message.Message

	a := agenttest.New(agenttest.NewResponseBuilder(
		func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
			capturedMessages = messages
		},
	).AddText("empty response").Build())

	tl := agenttool.New(a, agenttool.Config{})

	ret, err := tl.Call(tool.Context{Context: t.Context()}, "")
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got := ret.(string); got != "empty response" {
		t.Fatalf("Call() result = %q, want %q", got, "empty response")
	}
	if len(capturedMessages) != 1 {
		t.Fatalf("captured %d messages, want 1", len(capturedMessages))
	}
	text, ok := capturedMessages[0].Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("message content type = %T, want *message.TextContent", capturedMessages[0].Contents[0])
	}
	if text.Text != "" {
		t.Fatalf("message text = %q, want empty string", text.Text)
	}
}

func TestCall_InvalidJSONReturnsError(t *testing.T) {
	a := agenttest.New(agenttest.NewResponseBuilder().AddText("unused").Build())
	tl := agenttool.New(a, agenttool.Config{})

	_, err := tl.Call(tool.Context{Context: t.Context()}, "{")
	if err == nil {
		t.Fatal("expected JSON decoding error")
	}
}

func TestCall_PropagatesAgentError(t *testing.T) {
	expectedErr := errors.New("agent failed")
	a := agenttest.New(agenttest.NewResponseBuilder().AddError(expectedErr).Build())
	tl := agenttool.New(a, agenttool.Config{})

	_, err := tl.Call(tool.Context{Context: t.Context()}, `{"query":"hello"}`)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Call() error = %v, want %v", err, expectedErr)
	}
}
