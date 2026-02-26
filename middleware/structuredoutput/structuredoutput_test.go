// Copyright (c) Microsoft. All rights reserved.

package structuredoutput_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/middleware/structuredoutput"
)

type testFormat struct {
	kind string
}

func (t testFormat) Kind() string {
	return t.kind
}

func TestStructuredOutput_NoStructuredOutputOption(t *testing.T) {
	// Test that middleware passes through when no structured output option is provided
	baseCalled := false
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		baseCalled = true
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: "test response"},
				},
			}, nil)
		}
	}

	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()

	var updates []*message.ResponseUpdate
	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{})
	for update, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updates = append(updates, update)
	}

	if !baseCalled {
		t.Error("expected base function to be called")
	}

	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}

	if updates[0].String() != "test response" {
		t.Errorf("expected 'test response', got '%s'", updates[0].String())
	}
}

func TestStructuredOutput_NilStructuredOutputOption(t *testing.T) {
	// Test that middleware passes through when structured output option is nil
	baseCalled := false
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		baseCalled = true
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: "test response"},
				},
			}, nil)
		}
	}

	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()

	var updates []*message.ResponseUpdate
	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(nil))
	for update, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updates = append(updates, update)
	}

	if !baseCalled {
		t.Error("expected base function to be called")
	}

	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
}

func TestStructuredOutput_MissingFormat(t *testing.T) {
	// Test that middleware returns error when Format is nil
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `{"name":"test"}`},
				},
			}, nil)
		}
	}

	cfg := structuredoutput.Config{
		Format: nil, // Missing Format
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &struct{ Name string }{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected error for missing Format")
		}
		if err.Error() != "structured output not supported" {
			t.Errorf("expected 'structured output not supported', got '%s'", err.Error())
		}
		return
	}
	t.Fatal("expected error to be yielded")
}

func TestStructuredOutput_MissingUnmarshal(t *testing.T) {
	// Test that middleware returns error when Unmarshal is nil
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `{"name":"test"}`},
				},
			}, nil)
		}
	}

	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: nil, // Missing Unmarshal
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &struct{ Name string }{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected error for missing Unmarshal")
		}
		if err.Error() != "structured output not supported" {
			t.Errorf("expected 'structured output not supported', got '%s'", err.Error())
		}
		return
	}
	t.Fatal("expected error to be yielded")
}

func TestStructuredOutput_FormatError(t *testing.T) {
	// Test that middleware propagates Format errors
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `{"name":"test"}`},
				},
			}, nil)
		}
	}

	expectedErr := errors.New("format creation failed")
	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return nil, expectedErr
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &struct{ Name string }{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected error for Format failure")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error to be %v, got %v", expectedErr, err)
		}
		return
	}
	t.Fatal("expected error to be yielded")
}

func TestStructuredOutput_SuccessfulUnmarshal(t *testing.T) {
	// Test successful structured output parsing
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			// Verify that ResponseFormat option was added
			v, ok := agentopt.Get(options, agentopt.ResponseFormat)
			if !ok {
				yield(nil, errors.New("ResponseFormat option not found"))
				return
			}
			if v.Kind() != "json" {
				yield(nil, errors.New("ResponseFormat is not the expected format"))
				return
			}

			// Simulate streaming response in parts
			if !yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `{"name":`},
				},
			}, nil) {
				return
			}
			if !yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `"Alice",`},
				},
			}, nil) {
				return
			}
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `"age":30}`},
				},
			}, nil)
		}
	}

	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if output.Name != "Alice" {
		t.Errorf("expected Name to be 'Alice', got '%s'", output.Name)
	}
	if output.Age != 30 {
		t.Errorf("expected Age to be 30, got %d", output.Age)
	}
}

func TestStructuredOutput_UnmarshalError(t *testing.T) {
	// Test that middleware propagates Unmarshal errors
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `{"name":"test"}`},
				},
			}, nil)
		}
	}

	expectedErr := errors.New("unmarshal failed")
	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return expectedErr
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &struct{ Name string }{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected error for Unmarshal failure")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error to be %v, got %v", expectedErr, err)
		}
		return
	}
	t.Fatal("expected error to be yielded")
}

func TestStructuredOutput_BaseErrorPropagation(t *testing.T) {
	// Test that errors from the base function are propagated
	expectedErr := errors.New("base function error")
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, expectedErr)
		}
	}

	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &struct{ Name string }{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected error from base function")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error to be %v, got %v", expectedErr, err)
		}
		return
	}
	t.Fatal("expected error to be yielded")
}

func TestStructuredOutput_ComplexStructure(t *testing.T) {
	// Test with a more complex nested structure
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

	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			jsonStr := `{"name":"Bob","age":25,"address":{"street":"123 Main St","city":"Springfield"},"tags":["developer","golang"]}`
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: jsonStr},
				},
			}, nil)
		}
	}

	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &Person{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if output.Name != "Bob" {
		t.Errorf("expected Name to be 'Bob', got '%s'", output.Name)
	}
	if output.Age != 25 {
		t.Errorf("expected Age to be 25, got %d", output.Age)
	}
	if output.Address.Street != "123 Main St" {
		t.Errorf("expected Street to be '123 Main St', got '%s'", output.Address.Street)
	}
	if output.Address.City != "Springfield" {
		t.Errorf("expected City to be 'Springfield', got '%s'", output.Address.City)
	}
	if len(output.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(output.Tags))
	}
	if output.Tags[0] != "developer" || output.Tags[1] != "golang" {
		t.Errorf("expected tags to be [developer, golang], got %v", output.Tags)
	}
}

func TestStructuredOutput_EmptyResponse(t *testing.T) {
	// Test with an empty response
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			// Empty response
		}
	}

	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			if len(data) == 0 {
				return errors.New("empty data")
			}
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &struct{ Name string }{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected error for empty response")
		}
		if err.Error() != "empty data" {
			t.Errorf("expected 'empty data' error, got '%s'", err.Error())
		}
		return
	}
	t.Fatal("expected error to be yielded")
}

func TestStructuredOutput_MultipleContentTypes(t *testing.T) {
	// Test that only text content is collected for unmarshaling
	baseFunc := func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			if !yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `{"name":`},
				},
			}, nil) {
				return
			}
			if !yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.FunctionCallContent{
						CallID:    "call1",
						Name:      "test",
						Arguments: "{}",
					},
				},
			}, nil) {
				return
			}
			yield(&message.ResponseUpdate{
				Contents: message.Contents{
					&message.TextContent{Text: `"Charlie"}`},
				},
			}, nil)
		}
	}

	cfg := structuredoutput.Config{
		Format: func(v any) (format.Format, error) {
			return testFormat{kind: "json"}, nil
		},
		Unmarshal: func(format format.Format, data []byte, v any) error {
			return json.Unmarshal(data, v)
		},
	}

	mw := structuredoutput.New(cfg)
	ctx := context.Background()
	output := &struct {
		Name string `json:"name"`
	}{}

	seq := middleware.RunChain(ctx, baseFunc, []middleware.Middleware{mw}, []*message.Message{}, agentopt.StructuredOutput(output))
	for _, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if output.Name != "Charlie" {
		t.Errorf("expected Name to be 'Charlie', got '%s'", output.Name)
	}
}
