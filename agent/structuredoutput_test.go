// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
)

func TestAgent_StructuredOutput_NoStructuredOutputOption(t *testing.T) {
	baseCalled := false
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			baseCalled = true
			return singleStructuredOutputTestUpdate("test response")
		},
	)

	resp, err := a.Run(context.Background(), nil).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !baseCalled {
		t.Fatal("expected provider run to be called")
	}
	if got := resp.String(); got != "test response" {
		t.Fatalf("expected test response, got %q", got)
	}
}

func TestAgent_StructuredOutput_NilStructuredOutputOption(t *testing.T) {
	baseCalled := false
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			baseCalled = true
			return singleStructuredOutputTestUpdate("test response")
		},
	)

	resp, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(nil)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !baseCalled {
		t.Fatal("expected provider run to be called")
	}
	if got := resp.String(); got != "test response" {
		t.Fatalf("expected test response, got %q", got)
	}
}

func TestAgent_StructuredOutput_MissingFormat(t *testing.T) {
	a := newStructuredOutputTestAgent(
		nil,
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return singleStructuredOutputTestUpdate(`{"name":"test"}`)
		},
	)

	output := &struct{ Name string }{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err == nil {
		t.Fatal("expected error for missing Format")
	}
	if err.Error() != "structured output not supported" {
		t.Fatalf("expected structured output not supported, got %q", err.Error())
	}
}

func TestAgent_StructuredOutput_MissingUnmarshal(t *testing.T) {
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		nil,
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return singleStructuredOutputTestUpdate(`{"name":"test"}`)
		},
	)

	output := &struct{ Name string }{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err == nil {
		t.Fatal("expected error for missing Unmarshal")
	}
	if err.Error() != "structured output not supported" {
		t.Fatalf("expected structured output not supported, got %q", err.Error())
	}
}

func TestAgent_StructuredOutput_FormatError(t *testing.T) {
	expectedErr := errors.New("format creation failed")
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{}, expectedErr
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return singleStructuredOutputTestUpdate(`{"name":"test"}`)
		},
	)

	output := &struct{ Name string }{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestAgent_StructuredOutput_SuccessfulUnmarshal(t *testing.T) {
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			v, ok := agent.GetOption(options, agent.WithResponseFormat)
			if !ok {
				return structuredOutputTestError(errors.New("ResponseFormat option not found"))
			}
			if v.Kind != "json" {
				return structuredOutputTestError(errors.New("ResponseFormat is not the expected format"))
			}
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				if !yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: `{"name":`}}}, nil) {
					return
				}
				if !yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: `"Alice",`}}}, nil) {
					return
				}
				yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: `"age":30}`}}}, nil)
			}
		},
	)

	output := &struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}{}
	resp, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "Alice" {
		t.Fatalf("expected Name Alice, got %q", output.Name)
	}
	if output.Age != 30 {
		t.Fatalf("expected Age 30, got %d", output.Age)
	}
	// The middleware must forward the provider updates so the assistant response is
	// surfaced alongside the deserialized value, matching .NET AgentRunResponse<T>
	// which keeps Messages/Text next to the parsed Result.
	if got := resp.String(); got != `{"name":"Alice","age":30}` {
		t.Fatalf("expected response text to equal the JSON payload, got %q", got)
	}
}

func TestAgent_StructuredOutput_UsesLastMessageOnly(t *testing.T) {
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				if !yield(&agent.ResponseUpdate{
					ResponseID: "response-1",
					MessageID:  "message-1",
					Contents:   message.Contents{&message.TextContent{Text: "intermediate text"}},
				}, nil) {
					return
				}
				if !yield(&agent.ResponseUpdate{
					ResponseID: "response-1",
					MessageID:  "message-2",
					Contents:   message.Contents{&message.TextContent{Text: `{"name":`}},
				}, nil) {
					return
				}
				yield(&agent.ResponseUpdate{
					ResponseID: "response-1",
					MessageID:  "message-2",
					Contents:   message.Contents{&message.TextContent{Text: `"Grace"}`}},
				}, nil)
			}
		},
	)

	output := &struct {
		Name string `json:"name"`
	}{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "Grace" {
		t.Fatalf("expected Name Grace, got %q", output.Name)
	}
}

func TestAgent_StructuredOutput_NewMessageChunksNotMistakenForDifferentMessage(t *testing.T) {
	// When a new message starts with only some key fields set (e.g. ResponseID only),
	// subsequent chunks that introduce additional key fields (e.g. MessageID) must NOT
	// be treated as a new/different message. Without resetting current on message
	// boundary, stale key values from the previous message would cause isDifferent to
	// return true mid-message, clearing data and breaking JSON unmarshalling.
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				// First message: both ResponseID and MessageID are set.
				if !yield(&agent.ResponseUpdate{
					ResponseID: "response-1",
					MessageID:  "message-1",
					Contents:   message.Contents{&message.TextContent{Text: "ignore this"}},
				}, nil) {
					return
				}
				// Second message starts: only ResponseID changes, MessageID is absent.
				if !yield(&agent.ResponseUpdate{
					ResponseID: "response-2",
					Contents:   message.Contents{&message.TextContent{Text: `{"name":`}},
				}, nil) {
					return
				}
				// Second message continues: now MessageID appears for the first time.
				// Without the fix, isDifferent compares "message-2" against "message-1"
				// (stale from the first message) and wrongly resets data.
				yield(&agent.ResponseUpdate{
					ResponseID: "response-2",
					MessageID:  "message-2",
					Contents:   message.Contents{&message.TextContent{Text: `"Henry"}`}},
				}, nil)
			}
		},
	)

	output := &struct {
		Name string `json:"name"`
	}{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "Henry" {
		t.Fatalf("expected Name Henry, got %q", output.Name)
	}
}

func TestAgent_StructuredOutput_UnmarshalError(t *testing.T) {
	expectedErr := errors.New("unmarshal failed")
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return expectedErr
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return singleStructuredOutputTestUpdate(`{"name":"test"}`)
		},
	)

	output := &struct{ Name string }{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestAgent_StructuredOutput_BaseErrorPropagation(t *testing.T) {
	expectedErr := errors.New("base function error")
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return structuredOutputTestError(expectedErr)
		},
	)

	output := &struct{ Name string }{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestAgent_StructuredOutput_SkipsNilUpdates(t *testing.T) {
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				if !yield(nil, nil) {
					return
				}
				yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: `{"name":"Ada"}`}}}, nil)
			}
		},
	)

	output := &struct {
		Name string `json:"name"`
	}{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "Ada" {
		t.Fatalf("expected Name Ada, got %q", output.Name)
	}
}

func TestAgent_StructuredOutput_ComplexStructure(t *testing.T) {
	type Address struct {
		Street string `json:"street"`
		City   string `json:"city"`
	}
	type Person struct {
		Name    string   `json:"name"`
		Age     int      `json:"age"`
		Address Address  `json:"address"`
		Tags    []string `json:"tags"`
	}

	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			jsonStr := `{"name":"Bob","age":25,"address":{"street":"123 Main St","city":"Springfield"},"tags":["developer","golang"]}`
			return singleStructuredOutputTestUpdate(jsonStr)
		},
	)

	output := &Person{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "Bob" {
		t.Fatalf("expected Name Bob, got %q", output.Name)
	}
	if output.Age != 25 {
		t.Fatalf("expected Age 25, got %d", output.Age)
	}
	if output.Address.Street != "123 Main St" {
		t.Fatalf("expected Street 123 Main St, got %q", output.Address.Street)
	}
	if output.Address.City != "Springfield" {
		t.Fatalf("expected City Springfield, got %q", output.Address.City)
	}
	if len(output.Tags) != 2 || output.Tags[0] != "developer" || output.Tags[1] != "golang" {
		t.Fatalf("expected tags [developer golang], got %v", output.Tags)
	}
}

func TestAgent_StructuredOutput_EmptyResponse(t *testing.T) {
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			if len(data) == 0 {
				return errors.New("empty data")
			}
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {}
		},
	)

	output := &struct{ Name string }{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err == nil {
		t.Fatal("expected error for empty response")
	}
	if err.Error() != "empty data" {
		t.Fatalf("expected empty data error, got %q", err.Error())
	}
}

func TestAgent_StructuredOutput_MultipleContentTypes(t *testing.T) {
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				if !yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: `{"name":`}}}, nil) {
					return
				}
				if !yield(&agent.ResponseUpdate{Contents: message.Contents{&message.FunctionCallContent{CallID: "call1", Name: "test", Arguments: "{}"}}}, nil) {
					return
				}
				yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: `"Charlie"}`}}}, nil)
			}
		},
	)

	output := &struct {
		Name string `json:"name"`
	}{}
	_, err := a.Run(context.Background(), nil, agent.WithStructuredOutput(output)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "Charlie" {
		t.Fatalf("expected Name Charlie, got %q", output.Name)
	}
}

func TestAgent_StructuredOutput_StoresAssistantMessages(t *testing.T) {
	var stored []*message.Message
	historyProvider := agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "structured-output-history",
		Store: func(ctx context.Context, invoked agent.InvokedContext) error {
			stored = append(stored, invoked.ResponseMessages...)
			return nil
		},
	})
	a := agent.New(agent.ProviderConfig{
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return singleStructuredOutputTestUpdate(`{"name":"Ivy"}`)
		},
		Format: func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		Unmarshal: func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}, agent.Config{ID: "structured-output-store-agent", HistoryProvider: historyProvider})

	output := &struct {
		Name string `json:"name"`
	}{}
	_, err := a.Run(context.Background(), nil,
		agent.WithStructuredOutput(output),
		agent.WithSession(agenttest.CreateSession()),
	).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Name != "Ivy" {
		t.Fatalf("expected Name Ivy, got %q", output.Name)
	}
	// The middleware must forward provider updates so the run persists the assistant
	// response. Without forwarding, historyResponse stays empty and nothing is stored.
	if len(stored) == 0 {
		t.Fatal("expected assistant response messages to be stored, got none")
	}
	var text string
	for _, msg := range stored {
		text += msg.String()
	}
	if text != `{"name":"Ivy"}` {
		t.Fatalf("expected stored assistant text to equal the JSON payload, got %q", text)
	}
}

func newStructuredOutputTestAgent(format func(any) (agent.ResponseFormat, error), unmarshal func(agent.ResponseFormat, []byte, any) error, run agent.RunFunc) *agent.Agent {
	return agent.New(agent.ProviderConfig{
		Run:       run,
		Format:    format,
		Unmarshal: unmarshal,
	}, agent.Config{ID: "structured-output-test-agent"})
}

func singleStructuredOutputTestUpdate(text string) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: text}}}, nil)
	}
}

func structuredOutputTestError(err error) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		yield(nil, err)
	}
}
