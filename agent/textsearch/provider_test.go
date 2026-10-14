// Copyright (c) Microsoft. All rights reserved.

package textsearch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/agent/textsearch"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const (
	contextPrompt   = "## Additional Context\nConsider the following information from source documents when responding to the user:"
	citationsPrompt = "Include citations to the source document with document name and link if document name and link is available."
)

func expectedFormattedContext(prompt, body, citations string) string {
	newline := "\n"
	if runtime.GOOS == "windows" {
		newline = "\r\n"
	}
	return prompt + newline + strings.ReplaceAll(body, "\n", newline) + citations + newline + newline
}

func invokeProvider(provider agent.ContextProvider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

func storeMessages(t *testing.T, provider agent.ContextProvider, session *agent.Session, request, response []*message.Message) {
	t.Helper()
	if err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: request, ResponseMessages: response,
		Options: []agent.Option{agent.WithSession(session)},
	}); err != nil {
		t.Fatalf("Invoked: %v", err)
	}
}

func textMessage(role message.Role, text string) *message.Message {
	msg := message.NewText(text)
	msg.Role = role
	return msg
}

func searchTool(t *testing.T, provider agent.ContextProvider, options ...agent.Option) tool.FuncTool {
	t.Helper()
	request := message.NewText("request")
	messages, providedOptions, err := invokeProvider(provider, t.Context(), []*message.Message{request}, options...)
	if err != nil {
		t.Fatalf("Invoking: %v", err)
	}
	if len(messages) != 1 || messages[0] != request {
		t.Fatal("on-demand mode changed the input messages")
	}
	tools := slices.Collect(agent.AllOptions(providedOptions, agent.WithTool))
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	search, ok := tools[0].(tool.FuncTool)
	if !ok {
		t.Fatalf("search tool has type %T, want tool.FuncTool", tools[0])
	}
	return search
}

func TestTextSearchProvider_ConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config textsearch.Config
		search func(context.Context, string) ([]textsearch.Result, error)
	}{
		{name: "nil search"},
		{name: "negative limit", config: textsearch.Config{RecentMessageMemoryLimit: -1}},
		{name: "empty state key", config: textsearch.Config{StateKey: new("")}},
		{name: "blank state key", config: textsearch.Config{StateKey: new(" \t")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			search := tc.search
			if tc.name != "nil search" {
				search = func(context.Context, string) ([]textsearch.Result, error) { return nil, nil }
			}
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			textsearch.NewContextProvider(search, tc.config)
		})
	}
}

func TestTextSearchProvider_StateKeysAndInitialization(t *testing.T) {
	for _, tc := range []struct {
		key  *string
		want string
	}{
		{want: "TextSearchProvider"},
		{key: new("knowledge"), want: "knowledge"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
				return nil, nil
			}, textsearch.Config{StateKey: tc.key})
			keys := provider.StateKeys()
			if !slices.Equal(keys, []string{tc.want}) {
				t.Fatalf("StateKeys = %v", keys)
			}
			keys[0] = "changed"
			if provider.StateKeys()[0] != tc.want {
				t.Fatal("changing returned keys changed provider state")
			}
			session := agenttest.CreateSession()
			if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
				t.Fatal(err)
			}
			var state *textsearch.State
			if ok, err := session.Get(tc.want, &state); err != nil || !ok || state == nil || state.RecentMessagesText != nil {
				t.Fatalf("initialized state = (%v, %v, %v)", state, ok, err)
			}
		})
	}
}

func TestTextSearchProvider_State_NullAndEmptyMemory(t *testing.T) {
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, nil
	}, textsearch.Config{RecentMessageMemoryLimit: 3, RecentMessageRolesIncluded: []message.Role{}})
	session := agenttest.CreateSession()
	session.Set(provider.StateKeys()[0], (*textsearch.State)(nil))
	if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	var state *textsearch.State
	if ok, err := session.Get(provider.StateKeys()[0], &state); err != nil || !ok || state == nil {
		t.Fatalf("null state was not initialized: (%v, %v, %v)", state, ok, err)
	}
	if data, err := json.Marshal(state); err != nil || string(data) != "{}" {
		t.Fatalf("uninitialized memory JSON = (%s, %v)", data, err)
	}
	storeMessages(t, provider, session, []*message.Message{message.NewText("excluded")}, nil)
	if ok, err := session.Get(provider.StateKeys()[0], &state); err != nil || !ok {
		t.Fatalf("Get stored state = (%v, %v)", ok, err)
	}
	if data, err := json.Marshal(state); err != nil || string(data) != `{"recentMessagesText":[]}` {
		t.Fatalf("empty memory JSON = (%s, %v)", data, err)
	}
}

func TestTextSearchProvider_State_SeededMemoryUsesStoredOrder(t *testing.T) {
	var query string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		query = input
		return nil, nil
	}, textsearch.Config{RecentMessageMemoryLimit: 1})
	session := agenttest.CreateSession()
	state := &textsearch.State{RecentMessagesText: []string{"", "A", ""}}
	session.Set(provider.StateKeys()[0], state)
	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Q")}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if query != "A\n\nQ" {
		t.Fatalf("query = %q, want stored memory without read-time truncation", query)
	}
	storeMessages(t, provider, session, []*message.Message{message.NewText("next")}, nil)
	if !slices.Equal(state.RecentMessagesText, []string{"", "A", ""}) {
		t.Fatal("storing memory changed the previously stored state object")
	}
}

func TestTextSearchProvider_ConfigCapturesScalarValues(t *testing.T) {
	name, description, key, prompt, citations := "Find", "Find documents", "knowledge", "Context", "Cite"
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return []textsearch.Result{{Text: "answer"}}, nil
	}, textsearch.Config{
		SearchTime: textsearch.OnDemandFunctionCalling, FunctionToolName: &name, FunctionToolDescription: &description,
		StateKey: &key, ContextPrompt: &prompt, CitationsPrompt: &citations,
	})
	name, description, key, prompt, citations = "changed", "changed", "changed", "changed", "changed"
	search := searchTool(t, provider)
	if search.Name() != "Find" || search.Description() != "Find documents" || provider.StateKeys()[0] != "knowledge" {
		t.Fatal("changing configuration variables changed captured values")
	}
	if result, err := search.Call(t.Context(), `{"userQuestion":"Q"}`); err != nil || result != expectedFormattedContext("Context", "Contents: answer\n----\n", "Cite") {
		t.Fatalf("captured formatting = (%v, %v)", result, err)
	}
}

func TestTextSearchProvider_Invoking_FormatsResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config textsearch.Config
		want   string
	}{
		{
			name: "defaults",
			want: expectedFormattedContext(contextPrompt, "SourceDocName: Doc1\nSourceDocLink: https://example.com/doc1\nContents: Content 1\n----\nContents: Content 2\n----\n", citationsPrompt),
		},
		{
			name: "custom prompts",
			config: textsearch.Config{
				ContextPrompt: new("Context"), CitationsPrompt: new("Cite"),
			},
			want: expectedFormattedContext("Context", "SourceDocName: Doc1\nSourceDocLink: https://example.com/doc1\nContents: Content 1\n----\nContents: Content 2\n----\n", "Cite"),
		},
		{
			name: "empty prompts",
			config: textsearch.Config{
				ContextPrompt: new(""), CitationsPrompt: new(""),
			},
			want: expectedFormattedContext("", "SourceDocName: Doc1\nSourceDocLink: https://example.com/doc1\nContents: Content 1\n----\nContents: Content 2\n----\n", ""),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var query string
			provider := textsearch.NewContextProvider(func(ctx context.Context, input string) ([]textsearch.Result, error) {
				if ctx != t.Context() {
					t.Error("search did not receive the invocation context")
				}
				query = input
				return []textsearch.Result{
					{SourceName: "Doc1", SourceLink: "https://example.com/doc1", Text: "Content 1"},
					{SourceName: " \t", SourceLink: "\n", Text: "Content 2"},
				}, nil
			}, tc.config)
			request := []*message.Message{
				message.NewText("Question?"), nil, message.NewText(" \t"),
				textMessage(message.RoleAssistant, "Additional part"),
				message.NewText("history").WithSource(message.Source{Type: agent.SourceTypeHistoryProvider}),
				message.NewText("other context").WithSource(message.Source{Type: agent.SourceTypeContextProvider}),
			}
			original := slices.Clone(request)
			options := []agent.Option{agent.WithSession(agenttest.CreateSession()), agent.WithInstructions("instructions")}
			messages, returnedOptions, err := invokeProvider(provider, t.Context(), request, options...)
			if err != nil {
				t.Fatal(err)
			}
			if query != "Question?\nAdditional part" {
				t.Fatalf("query = %q", query)
			}
			if !slices.Equal(request, original) || !slices.Equal(messages[:len(request)], original) {
				t.Fatal("input messages were changed")
			}
			if !reflect.DeepEqual(returnedOptions, options) {
				t.Fatal("input options were changed")
			}
			if len(messages) != len(request)+1 {
				t.Fatalf("messages = %d, want %d", len(messages), len(request)+1)
			}
			result := messages[len(request)]
			if result.Role != message.RoleUser || result.String() != tc.want {
				t.Fatalf("result = (%s, %q), want user with %q", result.Role, result.String(), tc.want)
			}
			if result.Source != (message.Source{Type: agent.SourceTypeContextProvider, ID: "TextSearchProvider"}) {
				t.Fatalf("source = %#v", result.Source)
			}
		})
	}
}

func TestTextSearchProvider_Invoking_EmptyInputStillSearches(t *testing.T) {
	calls := 0
	provider := textsearch.NewContextProvider(func(_ context.Context, query string) ([]textsearch.Result, error) {
		calls++
		if query != "" {
			t.Fatalf("query = %q, want empty", query)
		}
		return nil, nil
	}, textsearch.Config{RecentMessageMemoryLimit: 3})
	if messages, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession())); err != nil || len(messages) != 0 {
		t.Fatalf("Invoking = (%v, %v)", messages, err)
	}
	if calls != 1 {
		t.Fatalf("search calls = %d, want 1", calls)
	}
}

func TestTextSearchProvider_ContextFormatter(t *testing.T) {
	payload := &struct{ ID string }{ID: "raw"}
	for _, formatted := range []string{"custom raw", ""} {
		t.Run(fmt.Sprintf("formatted=%q", formatted), func(t *testing.T) {
			provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
				return []textsearch.Result{{Text: "ignored", RawRepresentation: payload}}, nil
			}, textsearch.Config{
				ContextPrompt: new("ignored prompt"), CitationsPrompt: new("ignored citations"),
				ContextFormatter: func(results []textsearch.Result) (string, error) {
					if len(results) != 1 || results[0].RawRepresentation != payload {
						t.Fatal("formatter did not receive the raw representation")
					}
					return formatted, nil
				},
			})
			messages, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Q")})
			if err != nil || len(messages) != 2 || messages[1].String() != formatted {
				t.Fatalf("Invoking = (%v, %v), want appended %q", messages, err, formatted)
			}
		})
	}
}

func TestTextSearchProvider_NoResults(t *testing.T) {
	for _, behavior := range []textsearch.SearchBehavior{textsearch.BeforeInvoke, textsearch.OnDemandFunctionCalling} {
		t.Run(fmt.Sprint(behavior), func(t *testing.T) {
			formatterCalls := 0
			provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
				return nil, nil
			}, textsearch.Config{
				SearchTime: behavior,
				ContextFormatter: func(results []textsearch.Result) (string, error) {
					formatterCalls++
					if len(results) != 0 {
						t.Fatalf("results = %v, want empty", results)
					}
					return "no documents", nil
				},
			})
			if behavior == textsearch.BeforeInvoke {
				request := message.NewText("Q")
				messages, _, err := invokeProvider(provider, t.Context(), []*message.Message{request})
				if err != nil || len(messages) != 1 || messages[0] != request || formatterCalls != 0 {
					t.Fatalf("empty automatic search changed context or invoked formatter: %v", err)
				}
			} else {
				result, err := searchTool(t, provider).Call(t.Context(), `{"userQuestion":"Q"}`)
				if err != nil || result != "no documents" || formatterCalls != 1 {
					t.Fatalf("Call = (%v, %v), formatter calls = %d", result, err, formatterCalls)
				}
			}
		})
	}
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, nil
	}, textsearch.Config{SearchTime: textsearch.OnDemandFunctionCalling})
	if result, err := searchTool(t, provider).Call(t.Context(), `{"userQuestion":"Q"}`); err != nil || result != "" {
		t.Fatalf("empty on-demand search = (%v, %v), want empty string", result, err)
	}
}

func TestTextSearchProvider_OnDemand_ExposesTypedSearchTool(t *testing.T) {
	for _, tc := range []struct {
		name        *string
		description *string
		wantName    string
		wantDesc    string
	}{
		{wantName: "Search", wantDesc: "Allows searching for additional information to help answer the user question."},
		{name: new("Find"), description: new("Find documents"), wantName: "Find", wantDesc: "Find documents"},
		{name: new("Find"), description: new(""), wantName: "Find"},
		{name: new(""), description: new(""), wantName: ""},
	} {
		t.Run(tc.wantName+"/"+tc.wantDesc, func(t *testing.T) {
			calls := 0
			provider := textsearch.NewContextProvider(func(ctx context.Context, query string) ([]textsearch.Result, error) {
				calls++
				if ctx != t.Context() || query != "tool query" {
					t.Fatalf("search context or query changed: %q", query)
				}
				return []textsearch.Result{{Text: "answer"}}, nil
			}, textsearch.Config{
				SearchTime: textsearch.OnDemandFunctionCalling, FunctionToolName: tc.name,
				FunctionToolDescription: tc.description, RecentMessageMemoryLimit: 3,
			})
			session := agenttest.CreateSession()
			storeMessages(t, provider, session, []*message.Message{message.NewText("memory")}, nil)
			search := searchTool(t, provider, agent.WithSession(session))
			if search.Name() != tc.wantName || search.Description() != tc.wantDesc || calls != 0 {
				t.Fatalf("tool = (%q, %q), eager calls = %d", search.Name(), search.Description(), calls)
			}
			data, err := json.Marshal(search.Schema())
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Type       string
				Required   []string
				Properties map[string]struct{ Type string }
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			if schema.Type != "object" || !slices.Equal(schema.Required, []string{"userQuestion"}) || schema.Properties["userQuestion"].Type != "string" {
				t.Fatalf("search input schema = %s", data)
			}
			for _, args := range []string{`{}`, `{"userQuestion":42}`, `not json`} {
				if _, err := search.Call(t.Context(), args); err == nil {
					t.Fatalf("expected argument validation error for %q", args)
				}
			}
			if calls != 0 {
				t.Fatal("invalid arguments reached the search callback")
			}
			result, err := search.Call(t.Context(), `{"userQuestion":"tool query"}`)
			want := expectedFormattedContext(contextPrompt, "Contents: answer\n----\n", citationsPrompt)
			if err != nil || result != want || calls != 1 {
				t.Fatalf("Call = (%v, %v), calls = %d", result, err, calls)
			}
		})
	}
}

func TestTextSearchProvider_OnDemand_PreservesConfiguredTools(t *testing.T) {
	baseline := functool.MustNew(functool.Config{Name: "baseline"}, func(context.Context, struct{}) (string, error) {
		return "", nil
	})
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		t.Fatal("search must not run while exposing tools")
		return nil, nil
	}, textsearch.Config{SearchTime: textsearch.OnDemandFunctionCalling})
	options := []agent.Option{agent.WithTool(baseline)}
	for range 2 {
		_, returned, err := invokeProvider(provider, t.Context(), nil, options...)
		if err != nil {
			t.Fatal(err)
		}
		tools := slices.Collect(agent.AllOptions(returned, agent.WithTool))
		if len(tools) != 2 || tools[0] != baseline || tools[1].Name() != "Search" || len(options) != 1 {
			t.Fatalf("tools = %v, original options = %v", tools, options)
		}
	}
}

func TestTextSearchProvider_SearchErrors(t *testing.T) {
	searchErr := errors.New("backend unavailable")
	for _, withLogger := range []bool{false, true} {
		t.Run(fmt.Sprintf("logger=%t", withLogger), func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			config := textsearch.Config{Logger: logger}
			if !withLogger {
				previous := slog.Default()
				slog.SetDefault(logger)
				t.Cleanup(func() { slog.SetDefault(previous) })
				config.Logger = nil
			}
			provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
				return nil, searchErr
			}, config)
			request := message.NewText("Q")
			options := []agent.Option{agent.WithInstructions("keep")}
			messages, returned, err := invokeProvider(provider, t.Context(), []*message.Message{request}, options...)
			if err != nil || len(messages) != 1 || messages[0] != request || !reflect.DeepEqual(returned, options) {
				t.Fatalf("automatic error did not preserve context: %v", err)
			}
			if withLogger {
				if !strings.Contains(logs.String(), `"level":"ERROR"`) || !strings.Contains(logs.String(), searchErr.Error()) {
					t.Fatalf("search failure was not logged: %s", logs.String())
				}
			} else if logs.Len() != 0 {
				t.Fatalf("nil logger wrote to the default logger: %s", logs.String())
			}
			if !withLogger {
				config.ContextFormatter = func([]textsearch.Result) (string, error) { return "", searchErr }
				provider = textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
					return []textsearch.Result{{Text: "document"}}, nil
				}, config)
				messages, _, err = invokeProvider(provider, t.Context(), []*message.Message{request})
				if err != nil || len(messages) != 1 || messages[0] != request || logs.Len() != 0 {
					t.Fatalf("nil logger formatter failure = (%v, %v), default logs = %s", messages, err, logs.String())
				}
			}
		})
	}
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, searchErr
	}, textsearch.Config{SearchTime: textsearch.OnDemandFunctionCalling})
	if _, err := searchTool(t, provider).Call(t.Context(), `{"userQuestion":"Q"}`); !errors.Is(err, searchErr) {
		t.Fatalf("tool error = %v, want original backend error", err)
	}
}

func TestTextSearchProvider_Cancellation(t *testing.T) {
	for _, behavior := range []textsearch.SearchBehavior{textsearch.BeforeInvoke, textsearch.OnDemandFunctionCalling} {
		for _, searchErr := range []error{context.Canceled, fmt.Errorf("search: %w", context.DeadlineExceeded)} {
			t.Run(fmt.Sprintf("%d/%v", behavior, searchErr), func(t *testing.T) {
				var logs bytes.Buffer
				provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
					return nil, searchErr
				}, textsearch.Config{SearchTime: behavior, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
				var err error
				if behavior == textsearch.BeforeInvoke {
					_, _, err = invokeProvider(provider, t.Context(), nil)
				} else {
					_, err = searchTool(t, provider).Call(t.Context(), `{"userQuestion":"Q"}`)
				}
				if behavior == textsearch.BeforeInvoke {
					if err != nil || !strings.Contains(logs.String(), searchErr.Error()) {
						t.Fatalf("automatic cancellation = %v, logs = %s", err, logs.String())
					}
				} else if err != searchErr {
					t.Fatalf("error = %v, want original %v", err, searchErr)
				}
			})
		}
		t.Run(fmt.Sprintf("%d/already canceled", behavior), func(t *testing.T) {
			calls := 0
			var logs bytes.Buffer
			provider := textsearch.NewContextProvider(func(ctx context.Context, _ string) ([]textsearch.Result, error) {
				calls++
				return nil, ctx.Err()
			}, textsearch.Config{SearchTime: behavior, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			var err error
			if behavior == textsearch.BeforeInvoke {
				_, _, err = invokeProvider(provider, ctx, nil)
			} else {
				_, err = searchTool(t, provider).Call(ctx, `{"userQuestion":"Q"}`)
			}
			if behavior == textsearch.BeforeInvoke {
				if err != nil || !strings.Contains(logs.String(), context.Canceled.Error()) {
					t.Fatalf("automatic cancellation = %v, logs = %s", err, logs.String())
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("tool cancellation = %v", err)
			}
			if calls != 1 {
				t.Fatalf("error = %v, backend calls = %d", err, calls)
			}
		})
	}
}

func TestTextSearchProvider_FormatterErrors(t *testing.T) {
	formatErr := errors.New("formatting failed")
	for _, behavior := range []textsearch.SearchBehavior{textsearch.BeforeInvoke, textsearch.OnDemandFunctionCalling} {
		t.Run(fmt.Sprint(behavior), func(t *testing.T) {
			var logs bytes.Buffer
			provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
				return []textsearch.Result{{Text: "document"}}, nil
			}, textsearch.Config{
				SearchTime: behavior, Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
				ContextFormatter: func([]textsearch.Result) (string, error) { return "partial", formatErr },
			})
			if behavior == textsearch.BeforeInvoke {
				request := message.NewText("Q")
				messages, _, err := invokeProvider(provider, t.Context(), []*message.Message{request})
				if err != nil || len(messages) != 1 || messages[0] != request {
					t.Fatalf("automatic formatter error changed context: %v", err)
				}
				decoder := json.NewDecoder(&logs)
				var retrieved, failed map[string]any
				if err := decoder.Decode(&retrieved); err != nil {
					t.Fatal(err)
				}
				if err := decoder.Decode(&failed); err != nil {
					t.Fatal(err)
				}
				if retrieved["count"] != float64(1) || failed["error"] != formatErr.Error() {
					t.Fatalf("formatter failure logs = (%v, %v)", retrieved, failed)
				}
			} else {
				result, err := searchTool(t, provider).Call(t.Context(), `{"userQuestion":"Q"}`)
				if err != formatErr || result != nil || logs.Len() != 0 {
					t.Fatalf("on-demand formatter failure = (%v, %v), logs = %s", result, err, logs.String())
				}
			}
		})
	}
}

func TestTextSearchProvider_LoggingRedaction(t *testing.T) {
	for _, behavior := range []textsearch.SearchBehavior{textsearch.BeforeInvoke, textsearch.OnDemandFunctionCalling} {
		for _, sensitive := range []bool{false, true} {
			for _, custom := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/sensitive=%t/custom=%t", behavior, sensitive, custom), func(t *testing.T) {
					var logs bytes.Buffer
					redactions := 0
					config := textsearch.Config{
						SearchTime: behavior, LogSensitiveData: sensitive,
						Logger:           slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
						ContextFormatter: func([]textsearch.Result) (string, error) { return "secret result", nil },
					}
					if custom {
						config.Redactor = func(string) string {
							redactions++
							return "***"
						}
					}
					provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
						return []textsearch.Result{{Text: "secret result"}}, nil
					}, config)
					if behavior == textsearch.BeforeInvoke {
						if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("secret query")}); err != nil {
							t.Fatal(err)
						}
					} else if _, err := searchTool(t, provider).Call(t.Context(), `{"userQuestion":"secret query"}`); err != nil {
						t.Fatal(err)
					}
					decoder := json.NewDecoder(&logs)
					var count, resultLog map[string]any
					if err := decoder.Decode(&count); err != nil {
						t.Fatal(err)
					}
					if err := decoder.Decode(&resultLog); err != nil {
						t.Fatal(err)
					}
					if count["count"] != float64(1) {
						t.Fatalf("count log = %v", count)
					}
					query, result := "<redacted>", "<redacted>"
					if sensitive {
						query, result = "secret query", "secret result"
					} else if custom {
						query, result = "***", "***"
					}
					if resultLog["query"] != query || resultLog["context"] != result {
						t.Fatalf("result log = %v", resultLog)
					}
					wantRedactions := 0
					if custom && !sensitive {
						wantRedactions = 2
					}
					if redactions != wantRedactions {
						t.Fatalf("redactions = %d, want %d", redactions, wantRedactions)
					}
				})
			}
		}
	}
}

func TestTextSearchProvider_Memory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config textsearch.Config
		want   string
	}{
		{name: "disabled", want: "E"},
		{name: "default user roles", config: textsearch.Config{RecentMessageMemoryLimit: 3}, want: "A\nC\nE"},
		{name: "both roles and truncation", config: textsearch.Config{RecentMessageMemoryLimit: 3, RecentMessageRolesIncluded: []message.Role{message.RoleUser, message.RoleAssistant}}, want: "B\nC\nD\nE"},
		{name: "assistant roles", config: textsearch.Config{RecentMessageMemoryLimit: 4, RecentMessageRolesIncluded: []message.Role{message.RoleAssistant}}, want: "B\nD\nE"},
		{name: "case-insensitive roles", config: textsearch.Config{RecentMessageMemoryLimit: 4, RecentMessageRolesIncluded: []message.Role{"USER", "ASSISTANT"}}, want: "A\nB\nC\nD\nE"},
		{name: "empty roles", config: textsearch.Config{RecentMessageMemoryLimit: 4, RecentMessageRolesIncluded: []message.Role{}}, want: "E"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var query string
			provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
				query = input
				return nil, nil
			}, tc.config)
			session := agenttest.CreateSession()
			storeMessages(t, provider, session,
				[]*message.Message{message.NewText("A"), textMessage(message.RoleAssistant, "B"), nil, message.NewText(" \t")},
				[]*message.Message{message.NewText("C"), textMessage(message.RoleAssistant, "D"), textMessage(message.RoleSystem, "ignored")},
			)
			if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("E")}, agent.WithSession(session)); err != nil {
				t.Fatal(err)
			}
			if query != tc.want {
				t.Fatalf("query = %q, want %q", query, tc.want)
			}
		})
	}
}

func TestTextSearchProvider_Memory_FiltersAndFailedRuns(t *testing.T) {
	history := message.NewText("history").WithSource(message.Source{Type: agent.SourceTypeHistoryProvider})
	contextMessage := message.NewText("context").WithSource(message.Source{Type: agent.SourceTypeContextProvider})
	for _, tc := range []struct {
		name   string
		config textsearch.Config
		runErr error
		want   string
	}{
		{name: "default request filter", want: "request\nresponse\nQ"},
		{name: "custom request filter", config: textsearch.Config{StorageInputRequestMessageFilter: messagefilter.PassThrough}, want: "request\nhistory\ncontext\nresponse\nQ"},
		{name: "custom response filter", config: textsearch.Config{StorageInputResponseMessageFilter: messagefilter.None}, want: "request\nQ"},
		{name: "failed run", runErr: errors.New("run failed"), want: "Q"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.config.RecentMessageMemoryLimit = 10
			tc.config.RecentMessageRolesIncluded = []message.Role{message.RoleUser, message.RoleAssistant}
			var query string
			provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
				query = input
				return nil, nil
			}, tc.config)
			session := agenttest.CreateSession()
			request := []*message.Message{message.NewText("request"), history, contextMessage}
			response := []*message.Message{textMessage(message.RoleAssistant, "response").WithSource(message.Source{Type: agent.SourceTypeContextProvider})}
			if err := provider.Invoked(t.Context(), agent.InvokedContext{
				RequestMessages: request, ResponseMessages: response,
				Options: []agent.Option{agent.WithSession(session)}, Err: tc.runErr,
			}); err != nil {
				t.Fatal(err)
			}
			if request[1] != history || request[2] != contextMessage || len(response) != 1 {
				t.Fatal("storage filters changed the original slices")
			}
			if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Q")}, agent.WithSession(session)); err != nil {
				t.Fatal(err)
			}
			if query != tc.want {
				t.Fatalf("query = %q, want %q", query, tc.want)
			}
		})
	}
}

func TestTextSearchProvider_Memory_RetainsConfiguredRoles(t *testing.T) {
	roles := []message.Role{message.RoleUser}
	var query string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		query = input
		return nil, nil
	}, textsearch.Config{RecentMessageMemoryLimit: 3, RecentMessageRolesIncluded: roles})
	roles[0] = message.RoleAssistant
	session := agenttest.CreateSession()
	storeMessages(t, provider, session,
		[]*message.Message{message.NewText("user")},
		[]*message.Message{textMessage(message.RoleAssistant, "assistant")},
	)
	if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if query != "assistant" {
		t.Fatalf("query = %q, want updated configured role", query)
	}
}

func TestTextSearchProvider_MessageFilterErrors(t *testing.T) {
	filterErr := errors.New("filter failed")
	filter := func(context.Context, []*message.Message) ([]*message.Message, error) { return nil, filterErr }
	for _, stage := range []string{"search", "store request", "store response"} {
		t.Run(stage, func(t *testing.T) {
			config := textsearch.Config{RecentMessageMemoryLimit: 3}
			switch stage {
			case "search":
				config.SearchInputMessageFilter = filter
			case "store request":
				config.StorageInputRequestMessageFilter = filter
			case "store response":
				config.StorageInputResponseMessageFilter = filter
			}
			provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
				t.Fatal("filter error should prevent searching")
				return nil, nil
			}, config)
			var err error
			if stage == "search" {
				_, _, err = invokeProvider(provider, t.Context(), nil)
			} else {
				err = provider.Invoked(t.Context(), agent.InvokedContext{Options: []agent.Option{agent.WithSession(agenttest.CreateSession())}})
			}
			if err != filterErr {
				t.Fatalf("error = %v, want original filter error", err)
			}
		})
	}
}

func TestTextSearchProvider_CustomSearchInputFilter(t *testing.T) {
	var query string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		query = input
		return nil, nil
	}, textsearch.Config{
		SearchInputMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.Role != message.RoleSystem }), nil
		},
	})
	system := textMessage(message.RoleSystem, "system").WithSource(message.Source{Type: agent.SourceTypeHistoryProvider})
	request := []*message.Message{message.NewText("user"), system}
	if _, _, err := invokeProvider(provider, t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if query != "system" || len(request) != 2 || request[1] != system {
		t.Fatalf("query = %q, input = %v", query, request)
	}
}

func TestTextSearchProvider_Memory_SessionRoundTripAndIsolation(t *testing.T) {
	var query string
	search := func(_ context.Context, input string) ([]textsearch.Result, error) { query = input; return nil, nil }
	config := textsearch.Config{StateKey: new("knowledge"), RecentMessageMemoryLimit: 3}
	provider := textsearch.NewContextProvider(search, config)
	session := agenttest.CreateSession()
	storeMessages(t, provider, session, []*message.Message{message.NewText("A")}, nil)
	storeMessages(t, provider, session, []*message.Message{message.NewText("B")}, nil)
	for _, text := range []string{"C", "D"} {
		data, err := json.Marshal(session)
		if err != nil {
			t.Fatal(err)
		}
		var restored agent.Session
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		session = &restored
		provider = textsearch.NewContextProvider(search, config)
		storeMessages(t, provider, session, []*message.Message{message.NewText(text)}, nil)
	}
	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Q")}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if query != "B\nC\nD\nQ" {
		t.Fatalf("restored query = %q", query)
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var encoded struct {
		State map[string]struct{ RecentMessagesText []string }
	}
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(encoded.State["knowledge"].RecentMessagesText, []string{"B", "C", "D"}) {
		t.Fatalf("persisted state = %s", data)
	}
	for _, tc := range []struct {
		name        string
		config      textsearch.Config
		session     *agent.Session
		initialWant string
		want        string
	}{
		{name: "another key", config: textsearch.Config{StateKey: new("other"), RecentMessageMemoryLimit: 3}, session: session, want: "X\nQ"},
		{name: "another session", config: config, session: agenttest.CreateSession(), want: "X\nQ"},
		{name: "no session", config: config, want: "Q"},
		{name: "memory disabled", config: textsearch.Config{StateKey: new("knowledge")}, session: session, want: "Q"},
		{name: "reduced limit", config: textsearch.Config{StateKey: new("knowledge"), RecentMessageMemoryLimit: 1}, session: session, initialWant: "B\nC\nD\nQ", want: "X\nQ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := textsearch.NewContextProvider(search, tc.config)
			if tc.initialWant != "" {
				if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Q")}, agent.WithSession(tc.session)); err != nil {
					t.Fatal(err)
				}
				if query != tc.initialWant {
					t.Fatalf("initial query = %q, want %q", query, tc.initialWant)
				}
			}
			storeMessages(t, provider, tc.session, []*message.Message{message.NewText("X")}, nil)
			if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Q")}, agent.WithSession(tc.session)); err != nil {
				t.Fatal(err)
			}
			if query != tc.want {
				t.Fatalf("query = %q, want %q", query, tc.want)
			}
		})
	}
}

func TestTextSearchProvider_Memory_ConcurrentStores(t *testing.T) {
	const count = 32
	var query string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		query = input
		return nil, nil
	}, textsearch.Config{RecentMessageMemoryLimit: count})
	session := agenttest.CreateSession()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			<-start
			if err := provider.Invoked(t.Context(), agent.InvokedContext{
				RequestMessages: []*message.Message{message.NewText(fmt.Sprint(i))},
				Options:         []agent.Option{agent.WithSession(session)},
			}); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	got := strings.Split(query, "\n")
	want := make([]string, count)
	for i := range count {
		want[i] = fmt.Sprint(i)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("concurrent stores lost messages: %v", got)
	}
}

func TestTextSearchProvider_AgentLifecycle(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var queries []string
			provider := textsearch.NewContextProvider(func(_ context.Context, query string) ([]textsearch.Result, error) {
				queries = append(queries, query)
				return []textsearch.Result{{Text: "context"}}, nil
			}, textsearch.Config{RecentMessageMemoryLimit: 3})
			runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().AddText("reply A").NewTurn().AddText("reply B").Build()}
			a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ContextProviders: []agent.ContextProvider{provider}})
			session, err := a.CreateSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, question := range []string{"A", "B"} {
				if _, err := a.RunText(t.Context(), question, agent.WithSession(session), agent.Stream(stream)).Collect(); err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Equal(queries, []string{"A", "A\nB"}) {
				t.Fatalf("queries = %q, want no duplicate history or retrieved context", queries)
			}
		})
	}
}

func TestTextSearchProvider_AgentOnDemandToolExecution(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			calls := 0
			provider := textsearch.NewContextProvider(func(_ context.Context, query string) ([]textsearch.Result, error) {
				calls++
				if query != "model query" {
					t.Fatalf("query = %q", query)
				}
				return []textsearch.Result{{Text: "retrieved"}}, nil
			}, textsearch.Config{SearchTime: textsearch.OnDemandFunctionCalling})
			runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().
				AddFunctionCall("call", "Search", `{"userQuestion":"model query"}`).
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					result := messages[len(messages)-1].Contents[0].(*message.FunctionResultContent)
					formatted, ok := result.Result.(string)
					if !ok || !strings.Contains(formatted, "Contents: retrieved") || result.CallID != "call" {
						t.Fatalf("function result = %#v", result)
					}
				}).
				AddText("answer").Build()}
			a := agent.New(agent.ProviderConfig{
				Run: runner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{ContextProviders: []agent.ContextProvider{provider}})
			response, err := a.RunText(t.Context(), "question", agent.Stream(stream)).Collect()
			if err != nil || response.String() != "answer" || calls != 1 {
				t.Fatalf("run = (%v, %v), search calls = %d", response, err, calls)
			}
		})
	}
}

func TestTextSearchProvider_StateKeys_ReturnsDefaultKey_WhenNoOptionsProvided(t *testing.T) {
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, nil
	}, textsearch.Config{})

	keys := provider.StateKeys()
	if len(keys) != 1 || !slices.Contains(keys, "TextSearchProvider") {
		t.Fatalf("StateKeys = %v, want [TextSearchProvider]", keys)
	}
}

func TestTextSearchProvider_StateKeys_ReturnsCustomKey_WhenSetViaOptions(t *testing.T) {
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, nil
	}, textsearch.Config{StateKey: new("custom-key")})

	keys := provider.StateKeys()
	if len(keys) != 1 || !slices.Contains(keys, "custom-key") {
		t.Fatalf("StateKeys = %v, want [custom-key]", keys)
	}
}

func TestTextSearchProvider_InvokingAsync_ShouldInjectFormattedResultsAsync(t *testing.T) {
	results := []textsearch.Result{
		{SourceName: "Doc1", SourceLink: "http://example.com/doc1", Text: "Content of Doc1"},
		{SourceName: "Doc2", SourceLink: "http://example.com/doc2", Text: "Content of Doc2"},
	}
	for _, tc := range []struct {
		name            string
		contextPrompt   *string
		citationsPrompt *string
		withLogging     bool
	}{
		{name: "defaults with logging", withLogging: true},
		{name: "custom prompts", contextPrompt: new("Custom context prompt"), citationsPrompt: new("Custom citations prompt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capturedInput string
			var logs bytes.Buffer
			config := textsearch.Config{
				SearchTime:       textsearch.BeforeInvoke,
				ContextPrompt:    tc.contextPrompt,
				CitationsPrompt:  tc.citationsPrompt,
				LogSensitiveData: true,
			}
			if tc.withLogging {
				config.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			}
			provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
				capturedInput = input
				return results, nil
			}, config)
			request := []*message.Message{message.NewText("Sample user question?"), message.NewText("Additional part")}

			messages, options, err := invokeProvider(provider, t.Context(), request, agent.WithSession(agenttest.CreateSession()))
			if err != nil {
				t.Fatal(err)
			}
			if capturedInput != "Sample user question?\nAdditional part" {
				t.Fatalf("search input = %q", capturedInput)
			}
			if len(options) != 1 {
				t.Fatalf("options = %d, want original session option", len(options))
			}
			if len(messages) != 3 || messages[0] != request[0] || messages[1] != request[1] {
				t.Fatalf("messages = %v, want two original messages plus context", messages)
			}
			if messages[0].Source.Type != message.SourceTypeExternal || messages[1].Source.Type != message.SourceTypeExternal {
				t.Fatal("original messages lost external source attribution")
			}
			provided := messages[2]
			if provided.Role != message.RoleUser || provided.Source.Type != agent.SourceTypeContextProvider {
				t.Fatalf("provided message role/source = (%q, %#v)", provided.Role, provided.Source)
			}
			text := provided.String()
			if tc.contextPrompt == nil {
				for _, want := range []string{"## Additional Context", "Consider the following information from source documents when responding to the user:"} {
					if !strings.Contains(text, want) {
						t.Fatalf("context %q does not contain %q", text, want)
					}
				}
			} else if !strings.Contains(text, *tc.contextPrompt) {
				t.Fatalf("context %q does not contain custom prompt %q", text, *tc.contextPrompt)
			}
			for _, want := range []string{
				"SourceDocName: Doc1", "SourceDocLink: http://example.com/doc1", "Contents: Content of Doc1",
				"SourceDocName: Doc2", "SourceDocLink: http://example.com/doc2", "Contents: Content of Doc2",
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("context %q does not contain %q", text, want)
				}
			}
			wantCitations := citationsPrompt
			if tc.citationsPrompt != nil {
				wantCitations = *tc.citationsPrompt
			}
			if !strings.Contains(text, wantCitations) {
				t.Fatalf("context %q does not contain citations prompt %q", text, wantCitations)
			}
			if tc.withLogging {
				logText := logs.String()
				if !strings.Contains(logText, `"count":2`) ||
					!strings.Contains(logText, `"query":"Sample user question?\nAdditional part"`) ||
					!strings.Contains(logText, "Content of Doc1") {
					t.Fatalf("logs do not contain retrieval details: %s", logText)
				}
			}
		})
	}
}

func TestTextSearchProvider_InvokingAsync_RedactsLogDataBasedOnOptionsAsync(t *testing.T) {
	for _, sensitive := range []bool{false, true} {
		for _, customRedactor := range []bool{false, true} {
			t.Run(fmt.Sprintf("sensitive=%t/custom=%t", sensitive, customRedactor), func(t *testing.T) {
				var logs bytes.Buffer
				config := textsearch.Config{
					SearchTime:       textsearch.BeforeInvoke,
					LogSensitiveData: sensitive,
					Logger:           slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				}
				if customRedactor {
					config.Redactor = func(string) string { return "***" }
				}
				provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
					return []textsearch.Result{{SourceName: "Doc1", SourceLink: "http://example.com/doc1", Text: "Content of Doc1"}}, nil
				}, config)

				if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Sample user question?")}); err != nil {
					t.Fatal(err)
				}

				decoder := json.NewDecoder(&logs)
				var info, debug map[string]any
				if err := decoder.Decode(&info); err != nil {
					t.Fatal(err)
				}
				if err := decoder.Decode(&debug); err != nil {
					t.Fatal(err)
				}
				wantInput, wantOutput := "<redacted>", "<redacted>"
				if sensitive {
					wantInput = "Sample user question?"
					wantOutput = "Content of Doc1"
				} else if customRedactor {
					wantInput, wantOutput = "***", "***"
				}
				if debug["query"] != wantInput {
					t.Fatalf("logged query = %v, want %q", debug["query"], wantInput)
				}
				output, _ := debug["context"].(string)
				if sensitive {
					if !strings.Contains(output, wantOutput) {
						t.Fatalf("logged context = %q, want document content", output)
					}
				} else if output != wantOutput {
					t.Fatalf("logged context = %q, want %q", output, wantOutput)
				}
			})
		}
	}
}

func TestTextSearchProvider_InvokingAsync_OnDemand_ShouldExposeSearchToolAsync(t *testing.T) {
	for _, tc := range []struct {
		name            string
		toolName        *string
		toolDescription *string
		wantName        string
		wantDescription string
	}{
		{name: "defaults", wantName: "Search", wantDescription: "Allows searching for additional information to help answer the user question."},
		{name: "overrides", toolName: new("CustomSearch"), toolDescription: new("CustomDescription"), wantName: "CustomSearch", wantDescription: "CustomDescription"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
				return nil, nil
			}, textsearch.Config{
				SearchTime:              textsearch.OnDemandFunctionCalling,
				FunctionToolName:        tc.toolName,
				FunctionToolDescription: tc.toolDescription,
			})
			request := message.NewText("Q?")

			messages, options, err := invokeProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0] != request || messages[0].String() != "Q?" {
				t.Fatalf("messages = %v, want preserved request", messages)
			}
			tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
			if len(tools) != 1 || tools[0].Name() != tc.wantName || tools[0].Description() != tc.wantDescription {
				t.Fatalf("tools = %v, want %q/%q", tools, tc.wantName, tc.wantDescription)
			}
		})
	}
}

func TestTextSearchProvider_InvokingAsync_ShouldNotThrow_WhenSearchFailsAsync(t *testing.T) {
	searchErr := errors.New("Search Failed")
	var logs bytes.Buffer
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, searchErr
	}, textsearch.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	request := message.NewText("Q?")

	messages, options, err := invokeProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != request || messages[0].String() != "Q?" {
		t.Fatalf("messages = %v, want preserved request", messages)
	}
	if len(slices.Collect(agent.AllOptions(options, agent.WithTool))) != 0 {
		t.Fatal("automatic search failure unexpectedly provided tools")
	}
	if !strings.Contains(logs.String(), `"level":"ERROR"`) || !strings.Contains(logs.String(), searchErr.Error()) {
		t.Fatalf("search failure was not logged: %s", logs.String())
	}
}

func TestTextSearchProvider_SearchAsync_ShouldReturnFormattedResultsAsync(t *testing.T) {
	results := []textsearch.Result{
		{SourceName: "Doc1", SourceLink: "http://example.com/doc1", Text: "Content of Doc1"},
		{SourceName: "Doc2", SourceLink: "http://example.com/doc2", Text: "Content of Doc2"},
	}
	for _, tc := range []struct {
		name            string
		contextPrompt   *string
		citationsPrompt *string
	}{
		{name: "defaults"},
		{name: "custom prompts", contextPrompt: new("Custom context prompt"), citationsPrompt: new("Custom citations prompt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capturedInput string
			provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
				capturedInput = input
				return results, nil
			}, textsearch.Config{
				SearchTime:      textsearch.OnDemandFunctionCalling,
				ContextPrompt:   tc.contextPrompt,
				CitationsPrompt: tc.citationsPrompt,
			})

			result, err := searchTool(t, provider).Call(t.Context(), `{"userQuestion":"Sample user question?"}`)
			if err != nil {
				t.Fatal(err)
			}
			if capturedInput != "Sample user question?" {
				t.Fatalf("search input = %q", capturedInput)
			}
			formatted, ok := result.(string)
			if !ok {
				t.Fatalf("result type = %T, want string", result)
			}
			if tc.contextPrompt == nil {
				for _, want := range []string{"## Additional Context", "Consider the following information from source documents when responding to the user:"} {
					if !strings.Contains(formatted, want) {
						t.Fatalf("formatted result does not contain %q: %q", want, formatted)
					}
				}
			} else if !strings.Contains(formatted, *tc.contextPrompt) {
				t.Fatalf("formatted result does not contain custom prompt: %q", formatted)
			}
			for _, want := range []string{
				"SourceDocName: Doc1", "SourceDocLink: http://example.com/doc1", "Contents: Content of Doc1",
				"SourceDocName: Doc2", "SourceDocLink: http://example.com/doc2", "Contents: Content of Doc2",
			} {
				if !strings.Contains(formatted, want) {
					t.Fatalf("formatted result does not contain %q: %q", want, formatted)
				}
			}
			wantCitations := citationsPrompt
			if tc.citationsPrompt != nil {
				wantCitations = *tc.citationsPrompt
			}
			if !strings.Contains(formatted, wantCitations) {
				t.Fatalf("formatted result does not contain citations prompt: %q", formatted)
			}
		})
	}
}

func TestTextSearchProvider_InvokingAsync_ShouldUseContextFormatterWhenProvidedAsync(t *testing.T) {
	results := []textsearch.Result{
		{SourceName: "Doc1", SourceLink: "http://example.com/doc1", Text: "Content of Doc1"},
		{SourceName: "Doc2", SourceLink: "http://example.com/doc2", Text: "Content of Doc2"},
	}
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return results, nil
	}, textsearch.Config{
		ContextFormatter: func(results []textsearch.Result) (string, error) {
			return fmt.Sprintf("Custom formatted context with %d results.", len(results)), nil
		},
	})
	request := message.NewText("Q?")

	messages, _, err := invokeProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0] != request || messages[0].String() != "Q?" ||
		messages[1].String() != "Custom formatted context with 2 results." {
		t.Fatalf("messages = %v", messages)
	}
}

func TestTextSearchProvider_InvokingAsync_WithRawRepresentations_ContextFormatterCanAccessAsync(t *testing.T) {
	type rawPayload struct{ ID string }
	payload1 := &rawPayload{ID: "R1"}
	payload2 := &rawPayload{ID: "R2"}
	results := []textsearch.Result{
		{SourceName: "Doc1", Text: "Content 1", RawRepresentation: payload1},
		{SourceName: "Doc2", Text: "Content 2", RawRepresentation: payload2},
	}
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return results, nil
	}, textsearch.Config{
		ContextFormatter: func(results []textsearch.Result) (string, error) {
			ids := make([]string, len(results))
			for i, result := range results {
				ids[i] = result.RawRepresentation.(*rawPayload).ID
			}
			return strings.Join(ids, ","), nil
		},
	})
	request := message.NewText("Q?")

	messages, _, err := invokeProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0] != request || messages[0].String() != "Q?" || messages[1].String() != "R1,R2" {
		t.Fatalf("messages = %v", messages)
	}
}

func TestTextSearchProvider_InvokingAsync_WithNoResults_ShouldReturnEmptyContextAsync(t *testing.T) {
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, nil
	}, textsearch.Config{SearchTime: textsearch.BeforeInvoke})
	request := message.NewText("Q?")

	messages, options, err := invokeProvider(provider, t.Context(), []*message.Message{request}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != request || messages[0].String() != "Q?" {
		t.Fatalf("messages = %v, want original request only", messages)
	}
	if len(slices.Collect(agent.AllOptions(options, agent.WithTool))) != 0 {
		t.Fatal("empty automatic search unexpectedly provided tools")
	}
}

func TestTextSearchProvider_InvokingAsync_DefaultFilter_ExcludesNonExternalMessagesFromSearchInputAsync(t *testing.T) {
	var capturedInput string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, textsearch.Config{})
	request := []*message.Message{
		message.NewText("External message"),
		textMessage(message.RoleSystem, "From history").WithSource(message.Source{Type: agent.SourceTypeHistoryProvider, ID: "HistorySource"}),
		textMessage(message.RoleSystem, "From context provider").WithSource(message.Source{Type: agent.SourceTypeContextProvider, ID: "ContextSource"}),
	}

	if _, _, err := invokeProvider(provider, t.Context(), request, agent.WithSession(agenttest.CreateSession())); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "External message" {
		t.Fatalf("search input = %q, want external message only", capturedInput)
	}
}

func TestTextSearchProvider_InvokingAsync_CustomSearchInputFilter_OverridesDefaultAsync(t *testing.T) {
	var capturedInput string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, textsearch.Config{
		SearchInputMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.Role != message.RoleSystem }), nil
		},
	})
	request := []*message.Message{message.NewText("User message"), textMessage(message.RoleSystem, "System message")}

	if _, _, err := invokeProvider(provider, t.Context(), request, agent.WithSession(agenttest.CreateSession())); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "System message" {
		t.Fatalf("search input = %q, want system message only", capturedInput)
	}
}

func TestTextSearchProvider_InvokedAsync_DefaultFilter_ExcludesNonExternalMessagesFromStorageAsync(t *testing.T) {
	var capturedInput string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, textsearch.Config{
		RecentMessageMemoryLimit:   10,
		RecentMessageRolesIncluded: []message.Role{message.RoleUser, message.RoleSystem},
	})
	session := agenttest.CreateSession()
	request := []*message.Message{
		message.NewText("External message"),
		textMessage(message.RoleSystem, "From history").WithSource(message.Source{Type: agent.SourceTypeHistoryProvider, ID: "HistorySource"}),
		textMessage(message.RoleSystem, "From context provider").WithSource(message.Source{Type: agent.SourceTypeContextProvider, ID: "ContextSource"}),
	}

	storeMessages(t, provider, session, request, nil)
	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Next")}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "External message\nNext" {
		t.Fatalf("search input = %q, want only external stored message and current request", capturedInput)
	}
}

func TestTextSearchProvider_InvokedAsync_CustomStorageInputFilter_OverridesDefaultAsync(t *testing.T) {
	var capturedInput string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, textsearch.Config{
		RecentMessageMemoryLimit:         10,
		RecentMessageRolesIncluded:       []message.Role{message.RoleUser, message.RoleSystem},
		StorageInputRequestMessageFilter: messagefilter.PassThrough,
	})
	session := agenttest.CreateSession()
	request := []*message.Message{
		message.NewText("External message"),
		textMessage(message.RoleSystem, "From history").WithSource(message.Source{Type: agent.SourceTypeHistoryProvider, ID: "HistorySource"}),
	}

	storeMessages(t, provider, session, request, nil)
	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Next")}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "External message\nFrom history\nNext" {
		t.Fatalf("search input = %q, want all stored messages and current request", capturedInput)
	}
}

func TestTextSearchProvider_InvokingAsync_WithPreviousFailedRequest_ShouldNotIncludeFailedRequestInputInSearchInputAsync(t *testing.T) {
	var capturedInput string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, textsearch.Config{RecentMessageMemoryLimit: 3})
	session := agenttest.CreateSession()
	initial := []*message.Message{
		message.NewText("A"),
		textMessage(message.RoleAssistant, "B"),
		message.NewText("C"),
		textMessage(message.RoleAssistant, "D"),
	}

	if err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: initial,
		Options:         []agent.Option{agent.WithSession(session)},
		Err:             errors.New("Request Failed"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("E")}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "E" {
		t.Fatalf("search input = %q, want current request only", capturedInput)
	}
}

func TestTextSearchProvider_InvokingAsync_WithRecentMessageMemory_ShouldIncludeStoredMessagesInSearchInputAsync(t *testing.T) {
	var capturedInput string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, textsearch.Config{
		RecentMessageMemoryLimit:   3,
		RecentMessageRolesIncluded: []message.Role{message.RoleUser, message.RoleAssistant},
	})
	session := agenttest.CreateSession()
	initial := []*message.Message{
		message.NewText("A"),
		textMessage(message.RoleAssistant, "B"),
		message.NewText("C"),
		textMessage(message.RoleAssistant, "D"),
	}

	storeMessages(t, provider, session, initial, nil)
	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("E")}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "B\nC\nD\nE" {
		t.Fatalf("search input = %q, want retained memory followed by current request", capturedInput)
	}
}

func TestTextSearchProvider_InvokingAsync_WithAccumulatedMemoryAcrossInvocations_ShouldIncludeAllUpToLimitAsync(t *testing.T) {
	var capturedInput string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, textsearch.Config{
		RecentMessageMemoryLimit:   5,
		RecentMessageRolesIncluded: []message.Role{message.RoleUser, message.RoleAssistant},
	})
	session := agenttest.CreateSession()

	storeMessages(t, provider, session, []*message.Message{
		message.NewText("A"),
		textMessage(message.RoleAssistant, "B"),
	}, nil)
	storeMessages(t, provider, session, []*message.Message{
		message.NewText("C"),
		textMessage(message.RoleAssistant, "D"),
		message.NewText("E"),
	}, nil)
	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("F")}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "A\nB\nC\nD\nE\nF" {
		t.Fatalf("search input = %q, want accumulated memory followed by current request", capturedInput)
	}
}

func TestTextSearchProvider_InvokingAsync_WithRecentMessageRolesIncluded_ShouldFilterRolesAsync(t *testing.T) {
	var capturedInput string
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, textsearch.Config{
		RecentMessageMemoryLimit:   4,
		RecentMessageRolesIncluded: []message.Role{message.RoleAssistant},
	})
	session := agenttest.CreateSession()
	initial := []*message.Message{
		message.NewText("U1"),
		textMessage(message.RoleAssistant, "A1"),
		message.NewText("U2"),
		textMessage(message.RoleAssistant, "A2"),
	}

	storeMessages(t, provider, session, initial, nil)
	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("Question?")}, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "A1\nA2\nQuestion?" {
		t.Fatalf("search input = %q, want assistant memory followed by current request", capturedInput)
	}
}

func TestTextSearchProvider_InvokedAsync_ShouldPersistMessagesToSessionStateBagAsync(t *testing.T) {
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, nil
	}, textsearch.Config{
		RecentMessageMemoryLimit:   3,
		RecentMessageRolesIncluded: []message.Role{message.RoleUser, message.RoleAssistant},
	})
	session := agenttest.CreateSession()
	messages := []*message.Message{
		message.NewText("M1"),
		textMessage(message.RoleAssistant, "M2"),
		message.NewText("M3"),
	}

	storeMessages(t, provider, session, messages, nil)

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var serialized struct {
		State map[string]struct {
			RecentMessagesText []string `json:"recentMessagesText"`
		}
	}
	if err := json.Unmarshal(data, &serialized); err != nil {
		t.Fatal(err)
	}
	state, ok := serialized.State["TextSearchProvider"]
	if !ok {
		t.Fatalf("serialized session has no TextSearchProvider state: %s", data)
	}
	if !slices.Equal(state.RecentMessagesText, []string{"M1", "M2", "M3"}) {
		t.Fatalf("recent messages = %v", state.RecentMessagesText)
	}
}

func TestTextSearchProvider_StateBag_RoundtripRestoresMessagesAsync(t *testing.T) {
	config := textsearch.Config{
		RecentMessageMemoryLimit:   4,
		RecentMessageRolesIncluded: []message.Role{message.RoleUser, message.RoleAssistant},
	}
	provider := textsearch.NewContextProvider(func(context.Context, string) ([]textsearch.Result, error) {
		return nil, nil
	}, config)
	session := agenttest.CreateSession()
	storeMessages(t, provider, session, []*message.Message{
		message.NewText("A"),
		textMessage(message.RoleAssistant, "B"),
		message.NewText("C"),
		textMessage(message.RoleAssistant, "D"),
	}, nil)

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	var capturedInput string
	restoredProvider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		capturedInput = input
		return nil, nil
	}, config)
	if _, _, err := invokeProvider(restoredProvider, t.Context(), nil, agent.WithSession(&restored)); err != nil {
		t.Fatal(err)
	}
	if capturedInput != "A\nB\nC\nD" {
		t.Fatalf("restored search input = %q", capturedInput)
	}
}

func TestTextSearchProvider_InvokingAsync_WithEmptyStateBag_ShouldHaveNoMessagesAsync(t *testing.T) {
	session := agenttest.CreateSession()
	var capturedInput string
	called := false
	provider := textsearch.NewContextProvider(func(_ context.Context, input string) ([]textsearch.Result, error) {
		called = true
		capturedInput = input
		return nil, nil
	}, textsearch.Config{RecentMessageMemoryLimit: 3})

	if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	if !called || capturedInput != "" {
		t.Fatalf("search called = %t, input = %q; want one empty search", called, capturedInput)
	}
}

func ExampleNewContextProvider() {
	provider := textsearch.NewContextProvider(func(_ context.Context, query string) ([]textsearch.Result, error) {
		return []textsearch.Result{{SourceName: "Guide", Text: "The answer is 42."}}, nil
	}, textsearch.Config{
		ContextFormatter: func(results []textsearch.Result) (string, error) { return results[0].Text, nil },
	})
	messages, _, err := provider.Invoking(context.Background(), agent.InvokingContext{
		Messages: []*message.Message{message.NewText("What is the answer?")},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(messages[len(messages)-1])
	// Output: The answer is 42.
}
