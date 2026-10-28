// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

type abstractionPortSpecies string

type abstractionPortAnimal struct {
	ID       int                    `json:"id"`
	FullName string                 `json:"fullName"`
	Species  abstractionPortSpecies `json:"species"`
}

func TestAgentResponseGeneric_Result_DeserializesObjectWithoutUnwrapping(t *testing.T) {
	expected := abstractionPortAnimal{ID: 1, FullName: "Tigger", Species: "Tiger"}
	data, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	response := &agent.Response{Messages: []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: string(data)}}}}}
	format, err := jsonformat.FromResponseFormat(jsonformat.MustFor[abstractionPortAnimal]())
	if err != nil {
		t.Fatal(err)
	}

	var result abstractionPortAnimal
	if err := format.UnmarshalStructuredOutput([]byte(response.String()), &result); err != nil {
		t.Fatal(err)
	}

	if result.ID != expected.ID || result.FullName != expected.FullName || result.Species != expected.Species {
		t.Fatalf("result = %+v, want %+v", result, expected)
	}
}

func TestAgentResponseGeneric_Result_UnwrapsPrimitiveFromDataProperty(t *testing.T) {
	response := &agent.Response{Messages: []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: `{"data":42}`}}}}}
	wrapped, err := jsonformat.WrapNonObjectSchema(jsonformat.MustFor[int]())
	if err != nil {
		t.Fatal(err)
	}
	format, err := jsonformat.FromResponseFormat(wrapped)
	if err != nil {
		t.Fatal(err)
	}

	var result int
	if err := format.UnmarshalStructuredOutput([]byte(response.String()), &result); err != nil {
		t.Fatal(err)
	}

	if result != 42 {
		t.Fatalf("result = %d, want 42", result)
	}
}

func TestAgentResponseGeneric_Result_UnwrapsArrayFromDataProperty(t *testing.T) {
	response := &agent.Response{Messages: []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: `{"data":["apple","banana","cherry"]}`}}}}}
	wrapped, err := jsonformat.WrapNonObjectSchema(jsonformat.MustFor[[]string]())
	if err != nil {
		t.Fatal(err)
	}
	format, err := jsonformat.FromResponseFormat(wrapped)
	if err != nil {
		t.Fatal(err)
	}

	var result []string
	if err := format.UnmarshalStructuredOutput([]byte(response.String()), &result); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(result, []string{"apple", "banana", "cherry"}) {
		t.Fatalf("result = %v, want apple, banana, cherry", result)
	}
}

func TestAgentResponseGeneric_Result_UnwrapsEnumFromDataProperty(t *testing.T) {
	response := &agent.Response{Messages: []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: `{"data":"Walrus"}`}}}}}
	wrapped, err := jsonformat.WrapNonObjectSchema(jsonformat.MustFor[abstractionPortSpecies]())
	if err != nil {
		t.Fatal(err)
	}
	format, err := jsonformat.FromResponseFormat(wrapped)
	if err != nil {
		t.Fatal(err)
	}

	var result abstractionPortSpecies
	if err := format.UnmarshalStructuredOutput([]byte(response.String()), &result); err != nil {
		t.Fatal(err)
	}

	if result != abstractionPortSpecies("Walrus") {
		t.Fatalf("result = %q, want Walrus", result)
	}
}

func TestAgentResponseGeneric_Result_FallsBackWhenDataPropertyMissing(t *testing.T) {
	response := &agent.Response{Messages: []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "42"}}}}}
	wrapped, err := jsonformat.WrapNonObjectSchema(jsonformat.MustFor[int]())
	if err != nil {
		t.Fatal(err)
	}
	format, err := jsonformat.FromResponseFormat(wrapped)
	if err != nil {
		t.Fatal(err)
	}

	var result int
	if err := format.UnmarshalStructuredOutput([]byte(response.String()), &result); err != nil {
		t.Fatal(err)
	}

	if result != 42 {
		t.Fatalf("result = %d, want 42", result)
	}
}

func TestAgentResponseGeneric_Result_ThrowsWhenTextIsEmpty(t *testing.T) {
	response := &agent.Response{Messages: []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: ""}}}}}
	format, err := jsonformat.FromResponseFormat(jsonformat.MustFor[int]())
	if err != nil {
		t.Fatal(err)
	}

	var result int
	if err := format.UnmarshalStructuredOutput([]byte(response.String()), &result); err == nil {
		t.Fatal("empty response unexpectedly decoded")
	}
}

func TestAgentResponseGeneric_Result_ThrowsWhenDeserializedValueIsNull(t *testing.T) {
	response := &agent.Response{Messages: []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "null"}}}}}
	format, err := jsonformat.FromResponseFormat(jsonformat.MustFor[abstractionPortAnimal]())
	if err != nil {
		t.Fatal(err)
	}

	var result *abstractionPortAnimal
	if err := format.UnmarshalStructuredOutput([]byte(response.String()), &result); err == nil {
		t.Fatal("null response unexpectedly decoded")
	}
}

func TestAgent_RunGeneric_WithObjectType_DoesNotWrapSchema(t *testing.T) {
	expected := abstractionPortAnimal{ID: 1, FullName: "Test", Species: "Tiger"}
	data, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	a, schema := newAbstractionStructuredOutputAgent(t, string(data))

	var result abstractionPortAnimal
	if _, err := a.RunText(t.Context(), "Get me an animal", agent.WithStructuredOutput(&result)).Collect(); err != nil {
		t.Fatal(err)
	}

	if *schema == nil || (*schema).Type != "object" || (*schema).Properties["data"] != nil {
		t.Fatalf("schema = %+v, want an unwrapped object", *schema)
	}
}

func TestAgent_RunGeneric_WithPrimitiveType_WrapsSchema(t *testing.T) {
	a, schema := newAbstractionStructuredOutputAgent(t, `{"data":42}`)

	var result int
	if _, err := a.RunText(t.Context(), "Give me a number", agent.WithStructuredOutput(&result)).Collect(); err != nil {
		t.Fatal(err)
	}

	if *schema == nil || (*schema).Type != "object" || (*schema).Properties["data"] == nil {
		t.Fatalf("schema = %+v, want a wrapped primitive", *schema)
	}
}

func TestAgent_RunGeneric_WithArrayType_WrapsSchema(t *testing.T) {
	a, schema := newAbstractionStructuredOutputAgent(t, `{"data":["a","b","c"]}`)

	var result []string
	if _, err := a.RunText(t.Context(), "Give me an array of strings", agent.WithStructuredOutput(&result)).Collect(); err != nil {
		t.Fatal(err)
	}

	if *schema == nil || (*schema).Type != "object" || (*schema).Properties["data"] == nil {
		t.Fatalf("schema = %+v, want a wrapped array", *schema)
	}
}

func TestAgent_RunGeneric_WithEnumType_WrapsSchema(t *testing.T) {
	a, schema := newAbstractionStructuredOutputAgent(t, `{"data":"Tiger"}`)

	var result abstractionPortSpecies
	if _, err := a.RunText(t.Context(), "Give me a species", agent.WithStructuredOutput(&result)).Collect(); err != nil {
		t.Fatal(err)
	}

	if *schema == nil || (*schema).Type != "object" || (*schema).Properties["data"] == nil {
		t.Fatalf("schema = %+v, want a wrapped enum", *schema)
	}
}

func newAbstractionStructuredOutputAgent(t *testing.T, responseJSON string) (*agent.Agent, **jsonschema.Schema) {
	t.Helper()
	var capturedSchema *jsonschema.Schema
	a := newStructuredOutputTestAgent(
		func(value any) (agent.ResponseFormat, error) {
			format, err := jsonformat.ForType(reflect.TypeOf(value))
			if err != nil {
				return agent.ResponseFormat{}, err
			}
			return jsonformat.WrapNonObjectSchema(format)
		},
		func(responseFormat agent.ResponseFormat, data []byte, value any) error {
			format, err := jsonformat.FromResponseFormat(responseFormat)
			if err != nil {
				return err
			}
			return format.UnmarshalStructuredOutput(data, value)
		},
		func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			format, ok := agent.GetOption(options, agent.WithResponseFormat)
			if !ok {
				t.Fatal("provider received no response format")
			}
			capturedSchema, ok = format.Schema.(*jsonschema.Schema)
			if !ok {
				t.Fatalf("schema = %T, want *jsonschema.Schema", format.Schema)
			}
			return singleStructuredOutputTestUpdate(responseJSON)
		},
	)
	return a, &capturedSchema
}

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

func TestAgent_StructuredOutput_CustomFormat(t *testing.T) {
	type schema struct{ Unit string }
	format := agent.ResponseFormat{Kind: "number", Name: "plain", Schema: schema{Unit: "integer"}}
	var formatCalls, unmarshalCalls int
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			formatCalls++
			return format, nil
		},
		func(got agent.ResponseFormat, data []byte, v any) error {
			unmarshalCalls++
			if got != format {
				return errors.New("structured output format was changed")
			}
			number, err := strconv.Atoi(string(data))
			if err != nil {
				return err
			}
			output, ok := v.(*int)
			if !ok {
				return errors.New("structured output target was changed")
			}
			*output = number
			return nil
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			got, ok := agent.GetOption(options, agent.WithResponseFormat)
			if !ok || got != format {
				return structuredOutputTestError(errors.New("provider did not receive its format"))
			}
			return singleStructuredOutputTestUpdate("42")
		},
	)

	var output int
	response, err := a.Run(t.Context(), nil, agent.WithStructuredOutput(&output)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if output != 42 || response.String() != "42" || formatCalls != 1 || unmarshalCalls != 1 {
		t.Fatalf("output=%d, response=%q, format calls=%d, unmarshal calls=%d; want 42, 42, 1, 1",
			output, response.String(), formatCalls, unmarshalCalls)
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

func TestAgent_StructuredOutput_DoesNotMutateInputOptions(t *testing.T) {
	a := newStructuredOutputTestAgent(
		func(v any) (agent.ResponseFormat, error) {
			return agent.ResponseFormat{Kind: "json"}, nil
		},
		func(format agent.ResponseFormat, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
		func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return singleStructuredOutputTestUpdate(`{"name":"Alice"}`)
		},
	)

	output := &struct {
		Name string `json:"name"`
	}{}
	options := make([]agent.Option, 2, 3)
	options[0] = agent.WithStructuredOutput(output)
	options[1] = agent.WithSession(agenttest.CreateSession())
	sentinel := agent.WithInstructions("sentinel")
	backing := options[:cap(options)]
	backing[2] = sentinel

	if _, err := a.Run(context.Background(), nil, options...).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backing[2] != sentinel {
		t.Fatal("expected structured output not to modify the input options backing array")
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

func TestAgent_StructuredOutput_WithAutomaticTools(t *testing.T) {
	providerErr := errors.New("provider failed after tool execution")
	toolErr := errors.New("tool failed")
	for _, tc := range []struct {
		name          string
		intermediate  string
		final         string
		concurrent    bool
		fromContext   bool
		stopEarly     bool
		providerErr   error
		toolErr       error
		wantErr       error
		wantDecodeErr bool
	}{
		{name: "tool calls only", final: `{"name":"Ada"}`},
		{name: "intermediate prose", intermediate: "Looking that up.", final: `{"name":"Ada"}`},
		{name: "intermediate JSON", intermediate: `{"name":"intermediate"}`, final: `{"name":"Ada"}`},
		{name: "concurrent tools", concurrent: true, final: `{"name":"Ada"}`},
		{name: "context provider output", fromContext: true, final: `{"name":"Ada"}`},
		{name: "consumer stops early", stopEarly: true, final: `{"name":"Ada"}`},
		{name: "empty final response", wantErr: io.EOF, wantDecodeErr: true},
		{name: "invalid final response", final: "not JSON", wantDecodeErr: true},
		{name: "provider error", providerErr: providerErr, wantErr: providerErr},
		{name: "tool error", toolErr: toolErr, wantErr: toolErr},
	} {
		for _, stream := range []bool{false, true} {
			for _, perCallHistory := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/per-call-history=%t", tc.name, stream, perCallHistory), func(t *testing.T) {
					output := &struct {
						Name string `json:"name"`
					}{}
					var providerCalls, formatCalls, unmarshalCalls int
					var toolCalls atomic.Int32
					checkProviderCall := func(_ context.Context, _ []*message.Message, options ...agent.Option) {
						providerCalls++
						format, ok := agent.GetOption(options, agent.WithResponseFormat)
						if !ok || format.Kind != "json" {
							t.Fatal("expected JSON response format on every provider call")
						}
						if output.Name != "" || unmarshalCalls != 0 {
							t.Fatal("structured output was decoded before tool execution completed")
						}
					}
					builder := agenttest.NewResponseBuilder(checkProviderCall).
						AddText(tc.intermediate).
						AddFunctionCall("call-1", "lookup", `{}`).
						AddFunctionCall("call-2", "lookup", `{}`).
						NewTurn(checkProviderCall).
						AddFunctionCall("call-3", "lookup", `{}`).
						NewTurn(checkProviderCall)
					if tc.providerErr != nil {
						builder.AddError(tc.providerErr)
					} else {
						builder.AddText(tc.final)
					}
					runner := &agenttest.Runner{Responses: builder.Build()}
					cfg := agent.Config{
						RequirePerServiceCallHistoryPersistence: perCallHistory,
						Tools: []tool.Tool{functool.MustNew(functool.Config{Name: "lookup"},
							func(context.Context, struct{}) (string, error) {
								toolCalls.Add(1)
								return "found", tc.toolErr
							})},
					}
					options := []agent.Option{agent.Stream(stream), agent.WithSession(agenttest.CreateSession())}
					if tc.fromContext {
						cfg.ContextProviders = []agent.ContextProvider{agent.NewContextProvider(agent.ContextProviderConfig{
							SourceID: "structured-output",
							Provide: func(context.Context, agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
								return nil, []agent.Option{agent.WithStructuredOutput(output)}, nil
							},
						})}
					} else {
						options = append(options, agent.WithStructuredOutput(output))
					}
					a := agent.New(agent.ProviderConfig{
						Run: runner.Run,
						Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{
							AllowConcurrentInvocations:         tc.concurrent,
							MaximumConsecutiveErrorsPerRequest: new(0),
						})},
						Format: func(v any) (agent.ResponseFormat, error) {
							formatCalls++
							return jsonformat.ForType(reflect.TypeOf(v))
						},
						Unmarshal: func(format agent.ResponseFormat, data []byte, v any) error {
							unmarshalCalls++
							f, err := jsonformat.FromResponseFormat(format)
							if err != nil {
								return err
							}
							return f.UnmarshalStructuredOutput(data, v)
						},
					}, cfg)
					run := a.RunText(t.Context(), "look up the name", options...)
					if tc.stopEarly {
						for _, err := range run {
							if err != nil {
								t.Fatal(err)
							}
							break
						}
						wantProviderCalls, wantToolCalls := 3, int32(3)
						if stream {
							wantProviderCalls, wantToolCalls = 1, 0
						}
						if providerCalls != wantProviderCalls || toolCalls.Load() != wantToolCalls ||
							formatCalls != 1 || unmarshalCalls != 0 || output.Name != "" {
							t.Fatalf("provider calls=%d, tool calls=%d, format calls=%d, unmarshal calls=%d, output=%+v; want %d, %d, 1, 0, and no output",
								providerCalls, toolCalls.Load(), formatCalls, unmarshalCalls, output, wantProviderCalls, wantToolCalls)
						}
						return
					}
					response, err := run.Collect()
					switch {
					case tc.wantErr != nil:
						if !errors.Is(err, tc.wantErr) {
							t.Fatalf("error = %v, want %v", err, tc.wantErr)
						}
					case tc.wantDecodeErr:
						if err == nil {
							t.Fatal("expected invalid final output to fail decoding")
						}
					case err != nil:
						t.Fatal(err)
					}
					wantProviderCalls, wantToolCalls, wantUnmarshalCalls := 3, int32(3), 1
					if tc.toolErr != nil {
						wantProviderCalls, wantToolCalls = 1, 1
					}
					if tc.providerErr != nil || tc.toolErr != nil {
						wantUnmarshalCalls = 0
					}
					if providerCalls != wantProviderCalls || toolCalls.Load() != wantToolCalls ||
						formatCalls != 1 || unmarshalCalls != wantUnmarshalCalls {
						t.Fatalf("provider calls=%d, tool calls=%d, format calls=%d, unmarshal calls=%d; want %d, %d, 1, %d",
							providerCalls, toolCalls.Load(), formatCalls, unmarshalCalls,
							wantProviderCalls, wantToolCalls, wantUnmarshalCalls)
					}
					if err != nil {
						if output.Name != "" {
							t.Fatalf("output changed on failure: %+v", output)
						}
						return
					}
					if output.Name != "Ada" {
						t.Fatalf("decoded name = %q, want Ada", output.Name)
					}
					if got := response.Messages[len(response.Messages)-1].String(); got != tc.final {
						t.Fatalf("final response text = %q, want %q", got, tc.final)
					}
					var calls, results int
					for content := range response.Contents() {
						switch content.(type) {
						case *message.FunctionCallContent:
							calls++
						case *message.FunctionResultContent:
							results++
						}
					}
					if calls != 3 || results != 3 {
						t.Fatalf("response contains %d tool calls and %d results, want 3 of each", calls, results)
					}
				})
			}
		}
	}
}

func TestAgent_StructuredOutput_WithMessageInjection(t *testing.T) {
	type result struct {
		Name string `json:"name"`
	}
	for _, tc := range []struct {
		name          string
		payloads      [2]string
		want          any
		wantErr       error
		wantDecodeErr bool
	}{
		{name: "object", payloads: [2]string{`{"name":"superseded"}`, `{"name":"final"}`}, want: result{Name: "final"}},
		{name: "wrapped array", payloads: [2]string{`{"data":["superseded"]}`, `{"data":["final"]}`}, want: []string{"final"}},
		{name: "empty final response", payloads: [2]string{`{"name":"superseded"}`, ""}, want: result{}, wantErr: io.EOF},
		{name: "invalid final response", payloads: [2]string{`{"name":"superseded"}`, "not JSON"}, want: result{}, wantDecodeErr: true},
	} {
		for _, ids := range []string{"absent", "provided", "late"} {
			for _, stream := range []bool{false, true} {
				for _, perCallHistory := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/ids=%s/stream=%t/per-call-history=%t", tc.name, ids, stream, perCallHistory), func(t *testing.T) {
						injection := &agent.MessageInjector{}
						session := agenttest.CreateSession()
						output := reflect.New(reflect.TypeOf(tc.want)).Interface()
						var calls, formatCalls, unmarshalCalls int
						a := agent.New(agent.ProviderConfig{
							Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
							Run: func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
								return func(yield func(*agent.ResponseUpdate, error) bool) {
									if _, ok := agent.GetOption(options, agent.WithResponseFormat); !ok {
										t.Fatal("provider did not receive a response format")
									}
									if calls >= len(tc.payloads) {
										t.Fatal("unexpected extra provider call")
									}
									payload := tc.payloads[calls]
									calls++
									if calls == 1 {
										if err := injection.EnqueueMessages(session, message.NewText("revise the answer")); err != nil {
											t.Fatal(err)
										}
									}
									if payload == "" {
										return
									}
									split := len(payload) / 2
									updates := []*agent.ResponseUpdate{
										{},
										{Contents: message.Contents{&message.TextContent{Text: payload[:split]}}},
										{Contents: message.Contents{&message.TextContent{Text: payload[split:]}}},
										{
											AdditionalProperties: map[string]any{"last-call": calls},
											Contents:             message.Contents{&message.UsageContent{Details: message.UsageDetails{TotalTokenCount: 1}}},
										},
									}
									for i, update := range updates {
										update.AgentID = "provider-agent"
										update.AuthorName = "provider-author"
										update.Role = message.RoleAssistant
										if ids == "provided" || ids == "late" && i >= 2 {
											update.ResponseID = fmt.Sprintf("response-%d", calls)
											if i == 1 || i == 2 {
												update.MessageID = fmt.Sprintf("message-%d", calls)
											}
										}
										original := *update
										if !yield(update, nil) {
											return
										}
										if !reflect.DeepEqual(*update, original) {
											t.Fatal("message injection mutated a provider update")
										}
									}
								}
							},
							Format: func(v any) (agent.ResponseFormat, error) {
								formatCalls++
								format, err := jsonformat.ForType(reflect.TypeOf(v))
								if err != nil {
									return agent.ResponseFormat{}, err
								}
								return jsonformat.WrapNonObjectSchema(format)
							},
							Unmarshal: func(format agent.ResponseFormat, data []byte, v any) error {
								unmarshalCalls++
								f, err := jsonformat.FromResponseFormat(format)
								if err != nil {
									return err
								}
								return f.UnmarshalStructuredOutput(data, v)
							},
						}, agent.Config{
							MessageInjector:                         injection,
							RequirePerServiceCallHistoryPersistence: perCallHistory,
						})
						response, err := a.RunText(t.Context(), "answer", agent.WithSession(session), agent.Stream(stream), agent.WithStructuredOutput(output)).Collect()
						if tc.wantErr != nil {
							if !errors.Is(err, tc.wantErr) {
								t.Fatalf("error = %v, want %v", err, tc.wantErr)
							}
						} else if tc.wantDecodeErr {
							if err == nil {
								t.Fatal("expected invalid final output to fail decoding")
							}
						} else if err != nil {
							t.Fatal(err)
						}
						if got := reflect.ValueOf(output).Elem().Interface(); !reflect.DeepEqual(got, tc.want) {
							t.Fatalf("structured output = %+v, want %+v", got, tc.want)
						}
						if calls != 2 || formatCalls != 1 || unmarshalCalls != 1 {
							t.Fatalf("provider calls=%d, format calls=%d, unmarshal calls=%d; want 2, 1, 1", calls, formatCalls, unmarshalCalls)
						}
						if err != nil {
							return
						}
						wantResponseID := ""
						if ids != "absent" {
							wantResponseID = "response-2"
						}
						if response.ID != wantResponseID || response.Usage().TotalTokenCount != 2 || response.AdditionalProperties["last-call"] != 2 {
							t.Fatalf("response ID=%q, usage=%+v, properties=%v; want %q, 2 tokens, and last-call=2",
								response.ID, response.Usage(), response.AdditionalProperties, wantResponseID)
						}
					})
				}
			}
		}
	}
}

func TestAgent_StructuredOutput_MessageInjectionWithoutOutput(t *testing.T) {
	for _, explicitNil := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-nil=%t", explicitNil), func(t *testing.T) {
			injection := &agent.MessageInjector{}
			session := agenttest.CreateSession()
			var calls, updates int
			a := agent.New(agent.ProviderConfig{
				Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						calls++
						if calls == 1 {
							if err := injection.EnqueueMessages(session, message.NewText("injected")); err != nil {
								t.Fatal(err)
							}
							yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: "reply"}}}, nil)
						}
					}
				},
			}, agent.Config{MessageInjector: injection})
			options := []agent.Option{agent.WithSession(session), agent.Stream(true)}
			if explicitNil {
				options = append(options, agent.WithStructuredOutput(nil))
			}
			for update, err := range a.RunText(t.Context(), "start", options...) {
				if err != nil {
					t.Fatal(err)
				}
				updates++
				if update.String() != "reply" {
					t.Fatalf("unexpected update: %+v", update)
				}
			}
			if calls != 2 || updates != 1 {
				t.Fatalf("provider calls=%d, updates=%d; want 2 and 1", calls, updates)
			}
		})
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
