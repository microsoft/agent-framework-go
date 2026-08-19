// Copyright (c) Microsoft. All rights reserved.

package openaiprovider_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool/functool"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func TestChatConfigAllowConcurrentToolInvocations_EnablesParallelAutoCall(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch requestCount.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{
				"id":"chatcmpl-first",
				"object":"chat.completion",
				"created":1727888631,
				"model":"gpt-4o-mini",
				"choices":[{
					"index":0,
					"message":{
						"role":"assistant",
						"tool_calls":[
							{"id":"call_first","type":"function","function":{"name":"FirstTool","arguments":"{}"}},
							{"id":"call_second","type":"function","function":{"name":"SecondTool","arguments":"{}"}}
						]
					},
					"finish_reason":"tool_calls"
				}]
			}`)
		default:
			_, _ = io.WriteString(w, `{
				"id":"chatcmpl-second",
				"object":"chat.completion",
				"created":1727888632,
				"model":"gpt-4o-mini",
				"choices":[{
					"index":0,
					"message":{"role":"assistant","content":"done"},
					"finish_reason":"stop"
				}]
			}`)
		}
	}))
	defer server.Close()

	a := openaiprovider.NewChatCompletionsAgent(
		openai.NewClient(option.WithBaseURL(server.URL)),
		openaiprovider.AgentConfig{
			Model: "gpt-4o-mini",
			Config: agent.Config{
				AllowConcurrentToolInvocations: true,
			},
		},
	)

	assertConcurrentAutoCall(t, a)
}

func TestResponsesConfigAllowConcurrentToolInvocations_EnablesParallelAutoCall(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch requestCount.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{
				"id":"resp_first",
				"object":"response",
				"created_at":1741891428,
				"status":"completed",
				"model":"gpt-4o-mini",
				"output":[
					{"type":"function_call","id":"call_first","name":"FirstTool","arguments":"{}"},
					{"type":"function_call","id":"call_second","name":"SecondTool","arguments":"{}"}
				]
			}`)
		default:
			_, _ = io.WriteString(w, `{
				"id":"resp_second",
				"object":"response",
				"created_at":1741891429,
				"status":"completed",
				"model":"gpt-4o-mini",
				"output":[
					{"type":"message","id":"msg_test","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done","annotations":[]}]}
				]
			}`)
		}
	}))
	defer server.Close()

	a := openaiprovider.NewResponsesAgent(
		openai.NewClient(option.WithBaseURL(server.URL)),
		openaiprovider.AgentConfig{
			Model: "gpt-4o-mini",
			Config: agent.Config{
				AllowConcurrentToolInvocations: true,
			},
		},
	)

	assertConcurrentAutoCall(t, a)
}

func assertConcurrentAutoCall(t *testing.T, a *agent.Agent) {
	t.Helper()

	type emptyInput struct{}

	var started atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once

	firstTool := functool.MustNew(functool.Config{
		Name:        "FirstTool",
		Description: "Waits until both tools have started.",
	}, func(ctx context.Context, _ emptyInput) (string, error) {
		if started.Add(1) == 2 {
			releaseOnce.Do(func() { close(release) })
		}

		select {
		case <-release:
			return "first", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})

	secondTool := functool.MustNew(functool.Config{
		Name:        "SecondTool",
		Description: "Waits until both tools have started.",
	}, func(context.Context, emptyInput) (string, error) {
		if started.Add(1) == 2 {
			releaseOnce.Do(func() { close(release) })
		}
		<-release
		return "second", nil
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if _, err := a.RunText(ctx, "run tools", agent.WithTool(firstTool), agent.WithTool(secondTool)).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
	if got := started.Load(); got != 2 {
		t.Fatalf("started tool invocations = %d, want 2", got)
	}
}
