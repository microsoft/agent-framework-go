// Copyright (c) Microsoft. All rights reserved.

package openaiprovider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/messagetest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func bodyEqual(t *testing.T, got string, want string) {
	t.Helper()
	var gotObj any
	if err := json.Unmarshal([]byte(got), &gotObj); err != nil {
		t.Fatalf("failed decoding got JSON: %v\n%s", err, got)
	}
	var wantObj any
	if err := json.Unmarshal([]byte(want), &wantObj); err != nil {
		t.Fatalf("failed decoding want JSON: %v\n%s", err, want)
	}
	if !reflect.DeepEqual(gotObj, wantObj) {
		gotOut, err := json.MarshalIndent(gotObj, "", "    ")
		if err != nil {
			t.Fatalf("failed marshaling gotObj: %v", err)
		}
		wantOut, err := json.MarshalIndent(wantObj, "", "    ")
		if err != nil {
			t.Fatalf("failed marshaling wantObj: %v", err)
		}
		t.Errorf("body\ngot %s\nwant %s", gotOut, wantOut)
	}
}

func newTestServer(t *testing.T, input string, output string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed reading request body: %v", err)
		}
		bodyEqual(t, string(body), input)
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, output); err != nil {
			t.Fatalf("failed writing response: %v", err)
		}
	}))
}

func newTestClient(server *httptest.Server) *agent.Agent {
	return openaiprovider.NewChatCompletionsAgent(
		openai.NewClient(option.WithBaseURL(server.URL)),
		openaiprovider.AgentConfig{
			Model:  "gpt-4o-mini",
			Config: agent.Config{DisableFuncAutoCall: true},
		},
	)
}

func TestChatRequestIncludesAgentFrameworkUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent := r.Header.Get("User-Agent")
		if !strings.HasPrefix(userAgent, "agent-framework-go/") {
			t.Fatalf("User-Agent = %q, want agent-framework-go prefix", userAgent)
		}
		if !strings.Contains(userAgent, "OpenAI/Go") {
			t.Fatalf("User-Agent = %q, want OpenAI SDK token", userAgent)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1727888631,
			"model":"gpt-4o-mini",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]
		}`)
	}))
	defer server.Close()

	a := newTestClient(server)
	if _, err := a.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestChatConfigInstructions_NonStreaming(t *testing.T) {
	const input = `
						{
								"messages":[
										{"role":"system","content":"Be concise.\nAnswer warmly."},
										{"role":"user","content":"hello"}
								],
								"model":"gpt-4o-mini"
						}
						`
	const output = `
						{
							"id": "chatcmpl-test",
							"object": "chat.completion",
							"created": 1727888631,
							"model": "gpt-4o-mini-2024-07-18",
							"choices": [{
								"index": 0,
								"message": {"role": "assistant", "content": "Hello", "refusal": null},
								"finish_reason": "stop"
							}]
						}
						`
	server := newTestServer(t, input, output)
	defer server.Close()

	a := openaiprovider.NewChatCompletionsAgent(
		openai.NewClient(option.WithBaseURL(server.URL)),
		openaiprovider.AgentConfig{
			Model:        "gpt-4o-mini",
			Instructions: "Be concise.",
			Config: agent.Config{
				DisableFuncAutoCall: true,
			},
		},
	)

	if _, err := a.RunText(t.Context(), "hello", agent.WithInstructions("Answer warmly.")).Collect(); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestChatBasicRequestResponse_NonStreaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "messages":[{"role":"user","content":"hello"}],
                "model":"gpt-4o-mini",
                "max_completion_tokens":10
            }
            `
	const output = `
            {
              "id": "chatcmpl-ADx3PvAnCwJg0woha4pYsBTi3ZpOI",
              "object": "chat.completion",
              "created": 1727888631,
              "model": "gpt-4o-mini-2024-07-18",
              "choices": [
                {
                  "index": 0,
                  "message": {
                    "role": "assistant",
                    "content": "Hello! How can I assist you today?",
                    "refusal": null
                  },
                  "logprobs": null,
                  "finish_reason": "stop"
                }
              ],
              "usage": {
                "prompt_tokens": 8,
                "completion_tokens": 9,
                "total_tokens": 17,
                "prompt_tokens_details": {
                  "cached_tokens": 13
                },
                "completion_tokens_details": {
                  "reasoning_tokens": 90
                }
              },
              "system_fingerprint": "fp_f85bea6784"
            }
            `
	want := []*message.Message{
		{
			ID:        "chatcmpl-ADx3PvAnCwJg0woha4pYsBTi3ZpOI",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1727888631, 0),
			Contents: []message.Content{
				&message.TextContent{
					Text: "Hello! How can I assist you today?",
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:       8,
						OutputTokenCount:      9,
						TotalTokenCount:       17,
						CachedInputTokenCount: 13,
						ReasoningTokenCount:   90,
						AdditionalCounts: map[string]int64{
							"PromptTokensDetails.AudioTokens":                  0,
							"CompletionTokensDetails.AudioTokens":              0,
							"CompletionTokensDetails.AcceptedPredictionTokens": 0,
							"CompletionTokensDetails.RejectedPredictionTokens": 0,
						},
					},
				},
			},
		},
	}

	server := newTestServer(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	resp, err := a.RunText(t.Context(), "hello",
		openaiprovider.ChatCompletionNewParams(openai.ChatCompletionNewParams{
			MaxCompletionTokens: openai.Int(10),
			Temperature:         openai.Float(0.5),
		}),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected FinishReason stop, got %q", resp.FinishReason)
	}
}

func newTestServerStreaming(t *testing.T, input string, output string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed reading request body: %v", err)
		}
		bodyEqual(t, string(body), input)
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, output); err != nil {
			t.Fatalf("failed writing response: %v", err)
		}
	}))
}

func TestChatBasicRequestResponse_Streaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "messages":[{"role":"user","content":"hello"}],
                "model":"gpt-4o-mini",
                "stream":true,
                "max_completion_tokens":20
            }
            `
	const output = `data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":" How"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":" can"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":" I"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":" assist"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":" you"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":" today"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":"?"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":9,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":5,"audio_tokens":123},"completion_tokens_details":{"reasoning_tokens":90,"audio_tokens":456}}}

data: [DONE]

`

	msgID := "chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK"
	createdAt := time.Unix(1727889370, 0)
	want := []*agent.ResponseUpdate{
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: "Hello"}}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: "!"}}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: " How"}}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: " can"}}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: " I"}}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: " assist"}}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: " you"}}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: " today"}}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.TextContent{Text: "?"}}},
		{MessageID: msgID, ResponseID: msgID, FinishReason: "stop", Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{&message.UsageContent{Details: message.UsageDetails{
			InputTokenCount:       8,
			OutputTokenCount:      9,
			TotalTokenCount:       17,
			CachedInputTokenCount: 5,
			ReasoningTokenCount:   90,
			AdditionalCounts: map[string]int64{
				"PromptTokensDetails.AudioTokens":                  123,
				"CompletionTokensDetails.AudioTokens":              456,
				"CompletionTokensDetails.AcceptedPredictionTokens": 0,
				"CompletionTokensDetails.RejectedPredictionTokens": 0,
			},
		}}}},
	}

	server := newTestServerStreaming(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "hello", openaiprovider.ChatCompletionNewParams(openai.ChatCompletionNewParams{
		MaxCompletionTokens: openai.Int(20),
		Temperature:         openai.Float(0.5),
	}), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}
	if err := agenttest.ResponseUpdatesEqual(updates, want); err != nil {
		t.Error(err)
	}
}

func TestChatMultipleMessages_NonStreaming(t *testing.T) {
	const input = `
            {
                "frequency_penalty": 0.75,
                "presence_penalty": 0.5,
                "temperature": 0.25,
                "messages": [
                    {
                        "role": "system",
                        "content": "You are a really nice friend."
                    },
                    {
                        "role": "user",
                        "content": "hello!"
                    },
                    {
                        "role": "assistant",
                        "content": "hi, how are you?"
                    },
                    {
                        "role": "user",
                        "content": "i'm good. how are you?"
                    }
                ],
                "model": "gpt-4o-mini",
                "stop": ["great"],
                "seed": 42
            }
            `
	const output = `
            {
              "id": "chatcmpl-ADyV17bXeSm5rzUx3n46O7m3M0o3P",
              "object": "chat.completion",
              "created": 1727894187,
              "model": "gpt-4o-mini-2024-07-18",
              "choices": [
                {
                  "index": 0,
                  "message": {
                    "role": "assistant",
                    "content": "I'm doing well, thank you! What's on your mind today?",
                    "refusal": null
                  },
                  "logprobs": null,
                  "finish_reason": "stop"
                }
              ],
              "usage": {
                "prompt_tokens": 42,
                "completion_tokens": 15,
                "total_tokens": 57,
                "prompt_tokens_details": {
                  "cached_tokens": 13,
                  "audio_tokens": 123
                },
                "completion_tokens_details": {
                  "reasoning_tokens": 90,
                  "audio_tokens": 456
                }
              },
              "system_fingerprint": "fp_f85bea6784"
            }
            `
	want := []*message.Message{
		{
			ID:        "chatcmpl-ADyV17bXeSm5rzUx3n46O7m3M0o3P",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1727894187, 0),
			Contents: []message.Content{
				&message.TextContent{
					Text: "I'm doing well, thank you! What's on your mind today?",
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:       42,
						OutputTokenCount:      15,
						TotalTokenCount:       57,
						CachedInputTokenCount: 13,
						ReasoningTokenCount:   90,
						AdditionalCounts: map[string]int64{
							"PromptTokensDetails.AudioTokens":                  123,
							"CompletionTokensDetails.AudioTokens":              456,
							"CompletionTokensDetails.AcceptedPredictionTokens": 0,
							"CompletionTokensDetails.RejectedPredictionTokens": 0,
						},
					},
				},
			},
		},
	}

	server := newTestServer(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	messages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "You are a really nice friend."}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hello!"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "hi, how are you?"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "i'm good. how are you?"}}},
	}

	resp, err := a.Run(t.Context(), messages,
		openaiprovider.ChatCompletionNewParams(openai.ChatCompletionNewParams{
			Temperature:      openai.Float(0.25),
			FrequencyPenalty: openai.Float(0.75),
			PresencePenalty:  openai.Float(0.5),
			Stop: openai.ChatCompletionNewParamsStopUnion{
				OfStringArray: []string{"great"},
			},
			Seed: openai.Int(42),
		}),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
}

func TestChatMultiPartSystemMessage_NonStreaming(t *testing.T) {
	const input = `
            {
                "messages": [
                    {
                        "role": "system",
                        "content": [
                            {
                                "type": "text",
                                "text": "You are a really nice friend."
                            },
                            {
                                "type": "text",
                                "text": "Really nice."
                            }
                        ]
                    },
                    {
                        "role": "user",
                        "content": "hello!"
                    }
                ],
                "model": "gpt-4o-mini"
            }
            `
	const output = `
            {
              "id": "chatcmpl-ADyV17bXeSm5rzUx3n46O7m3M0o3P",
              "object": "chat.completion",
              "created": 1727894187,
              "model": "gpt-4o-mini-2024-07-18",
              "choices": [
                {
                  "index": 0,
                  "message": {
                    "role": "assistant",
                    "content": "Hi! It's so good to hear from you!",
                    "refusal": null
                  },
                  "logprobs": null,
                  "finish_reason": "stop"
                }
              ],
              "usage": {
                "prompt_tokens": 42,
                "completion_tokens": 15,
                "total_tokens": 57,
                "prompt_tokens_details": {
                  "cached_tokens": 13
                },
                "completion_tokens_details": {
                  "reasoning_tokens": 90
                }
              },
              "system_fingerprint": "fp_f85bea6784"
            }
            `
	want := []*message.Message{
		{
			ID:        "chatcmpl-ADyV17bXeSm5rzUx3n46O7m3M0o3P",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1727894187, 0),
			Contents: []message.Content{
				&message.TextContent{
					Text: "Hi! It's so good to hear from you!",
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:       42,
						OutputTokenCount:      15,
						TotalTokenCount:       57,
						CachedInputTokenCount: 13,
						ReasoningTokenCount:   90,
						AdditionalCounts: map[string]int64{
							"PromptTokensDetails.AudioTokens":                  0,
							"CompletionTokensDetails.AudioTokens":              0,
							"CompletionTokensDetails.AcceptedPredictionTokens": 0,
							"CompletionTokensDetails.RejectedPredictionTokens": 0,
						},
					},
				},
			},
		},
	}

	server := newTestServer(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	messages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{
			&message.TextContent{Text: "You are a really nice friend."},
			&message.TextContent{Text: "Really nice."},
		}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hello!"}}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
}

func TestChatEmptyAssistantMessage_NonStreaming(t *testing.T) {
	const input = `
            {
                "messages": [
                    {
                        "role": "system",
                        "content": "You are a really nice friend."
                    },
                    {
                        "role": "user",
                        "content": "hello!"
                    },
                    {
                        "role": "assistant",
                        "content": ""
                    },
                    {
                        "role": "user",
                        "content": "i'm good. how are you?"
                    }
                ],
                "model": "gpt-4o-mini"
            }
            `
	const output = `
            {
              "id": "chatcmpl-ADyV17bXeSm5rzUx3n46O7m3M0o3P",
              "object": "chat.completion",
              "created": 1727894187,
              "model": "gpt-4o-mini-2024-07-18",
              "choices": [
                {
                  "index": 0,
                  "message": {
                    "role": "assistant",
                    "content": "I'm doing well, thank you! What's on your mind today?",
                    "refusal": null
                  },
                  "logprobs": null,
                  "finish_reason": "stop"
                }
              ],
              "usage": {
                "prompt_tokens": 42,
                "completion_tokens": 15,
                "total_tokens": 57,
                "prompt_tokens_details": {
                  "cached_tokens": 13
                },
                "completion_tokens_details": {
                  "reasoning_tokens": 90
                }
              },
              "system_fingerprint": "fp_f85bea6784"
            }
            `
	want := []*message.Message{
		{
			ID:        "chatcmpl-ADyV17bXeSm5rzUx3n46O7m3M0o3P",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1727894187, 0),
			Contents: []message.Content{
				&message.TextContent{
					Text: "I'm doing well, thank you! What's on your mind today?",
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:       42,
						OutputTokenCount:      15,
						TotalTokenCount:       57,
						CachedInputTokenCount: 13,
						ReasoningTokenCount:   90,
						AdditionalCounts: map[string]int64{
							"PromptTokensDetails.AudioTokens":                  0,
							"CompletionTokensDetails.AudioTokens":              0,
							"CompletionTokensDetails.AcceptedPredictionTokens": 0,
							"CompletionTokensDetails.RejectedPredictionTokens": 0,
						},
					},
				},
			},
		},
	}

	server := newTestServer(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	messages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "You are a really nice friend."}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hello!"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: ""}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "i'm good. how are you?"}}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
}

func TestChatFunctionCallContent_NonStreaming(t *testing.T) {
	const input = `
            {
                "tools": [
                    {
                        "function": {
                            "description": "Gets the age of the specified person.",
                            "name": "GetPersonAge",
                            "parameters": {
                                "type": "object",
                                "required": [
                                    "personName"
                                ],
                                "properties": {
                                    "personName": {
                                    	"description": "The person whose age is being requested",
                                        "type": "string"
                                    }
                                },
                                "additionalProperties": false
                            }
                        },
                        "type": "function"
                    }
                ],
                "messages": [
                    {
                        "role": "user",
                        "content": "How old is Alice?"
                    }
                ],
                "model": "gpt-4o-mini"
            }
            `
	const output = `
            {
              "id": "chatcmpl-ADydKhrSKEBWJ8gy0KCIU74rN3Hmk",
              "object": "chat.completion",
              "created": 1727894702,
              "model": "gpt-4o-mini-2024-07-18",
              "choices": [
                {
                  "index": 0,
                  "message": {
                    "role": "assistant",
                    "content": null,
                    "tool_calls": [
                      {
                        "id": "call_8qbINM045wlmKZt9bVJgwAym",
                        "type": "function",
                        "function": {
                          "name": "GetPersonAge",
                          "arguments": "{\"personName\":\"Alice\"}"
                        }
                      }
                    ],
                    "refusal": null
                  },
                  "logprobs": null,
                  "finish_reason": "tool_calls"
                }
              ],
              "usage": {
                "prompt_tokens": 61,
                "completion_tokens": 16,
                "total_tokens": 77,
                "prompt_tokens_details": {
                  "cached_tokens": 13
                },
                "completion_tokens_details": {
                  "reasoning_tokens": 90
                }
              },
              "system_fingerprint": "fp_f85bea6784"
            }
            `
	want := []*message.Message{
		{
			ID:        "chatcmpl-ADydKhrSKEBWJ8gy0KCIU74rN3Hmk",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1727894702, 0),
			Contents: []message.Content{
				&message.FunctionCallContent{
					CallID:    "call_8qbINM045wlmKZt9bVJgwAym",
					Name:      "GetPersonAge",
					Arguments: `{"personName":"Alice"}`,
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:       61,
						OutputTokenCount:      16,
						TotalTokenCount:       77,
						CachedInputTokenCount: 13,
						ReasoningTokenCount:   90,
						AdditionalCounts: map[string]int64{
							"PromptTokensDetails.AudioTokens":                  0,
							"CompletionTokensDetails.AudioTokens":              0,
							"CompletionTokensDetails.AcceptedPredictionTokens": 0,
							"CompletionTokensDetails.RejectedPredictionTokens": 0,
						},
					},
				},
			},
		},
	}

	server := newTestServer(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	type GetPersonAgeInput struct {
		PersonName string `json:"personName" jsonschema:"The person whose age is being requested"`
	}
	getPersonAge := func(ctx context.Context, input GetPersonAgeInput) (int, error) {
		return 42, nil
	}
	tool := functool.MustNew(functool.Config{
		Name:        "GetPersonAge",
		Description: "Gets the age of the specified person.",
	}, getPersonAge)

	resp, err := a.RunText(t.Context(), "How old is Alice?",
		agent.WithTool(tool),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
}

func TestChatFunctionCallContent_Streaming(t *testing.T) {
	const input = `
            {
                "tools": [
                    {
                        "function": {
                            "description": "Gets the age of the specified person.",
                            "name": "GetPersonAge",
                            "parameters": {
                                "type": "object",
                                "required": [
                                    "personName"
                                ],
                                "properties": {
                                    "personName": {
                                        "type": "string"
                                    }
                                },
                                "additionalProperties": false
                            }
                        },
                        "type": "function"
                    }
                ],
                "messages": [
                    {
                        "role": "user",
                        "content": "How old is Alice?"
                    }
                ],
                "model": "gpt-4o-mini",
                "stream": true
            }
            `
	const output = `data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_F9ZaqPWo69u0urxAhVt8meDW","type":"function","function":{"name":"GetPersonAge","arguments":""}}],"refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\""}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"person"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"Name"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\":\""}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"Alice"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"}"}}]},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"tool_calls"}],"usage":null}

data: {"id":"chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl","object":"chat.completion.chunk","created":1727895263,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_f85bea6784","choices":[],"usage":{"prompt_tokens":61,"completion_tokens":16,"total_tokens":77,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":90}}}

data: [DONE]

`

	msgID := "chatcmpl-ADymNiWWeqCJqHNFXiI1QtRcLuXcl"
	callID := "call_F9ZaqPWo69u0urxAhVt8meDW"
	createdAt := time.Unix(1727895263, 0)
	want := []*agent.ResponseUpdate{
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt},
		{MessageID: msgID, ResponseID: msgID, FinishReason: "tool_calls", Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{
			&message.FunctionCallContent{CallID: callID, Name: "GetPersonAge", Arguments: `{"personName":"Alice"}`},
		}},
		{MessageID: msgID, ResponseID: msgID, Role: message.RoleAssistant, CreatedAt: createdAt, Contents: []message.Content{
			&message.UsageContent{Details: message.UsageDetails{
				InputTokenCount:       61,
				OutputTokenCount:      16,
				TotalTokenCount:       77,
				CachedInputTokenCount: 0,
				ReasoningTokenCount:   90,
				AdditionalCounts: map[string]int64{
					"PromptTokensDetails.AudioTokens":                  0,
					"CompletionTokensDetails.AudioTokens":              0,
					"CompletionTokensDetails.AcceptedPredictionTokens": 0,
					"CompletionTokensDetails.RejectedPredictionTokens": 0,
				},
			}},
		}},
	}

	server := newTestServerStreaming(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	type GetPersonAgeInput struct {
		PersonName string `json:"personName"`
	}
	getPersonAge := func(ctx context.Context, input GetPersonAgeInput) (int, error) {
		return 42, nil
	}
	tool := functool.MustNew(functool.Config{
		Name:        "GetPersonAge",
		Description: "Gets the age of the specified person.",
	}, getPersonAge)

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "How old is Alice?", agent.WithTool(tool), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}
	if err := agenttest.ResponseUpdatesEqual(updates, want); err != nil {
		t.Error(err)
	}
}

func TestChatAssistantMessageWithBothToolsAndContent_NonStreaming(t *testing.T) {
	const input = `
            {
                "messages": [
                    {
                        "role": "system",
                        "content": "You are a really nice friend."
                    },
                    {
                        "role": "user",
                        "content": "hello!"
                    },
                    {
                        "role": "assistant",
                        "content": "hi, how are you?",
                        "tool_calls": [
                            {
                                "id": "12345",
                                "type": "function",
                                "function": {
                                    "name": "SayHello",
                                    "arguments": "null"
                                }
                            },
                            {
                                "id": "12346",
                                "type": "function",
                                "function": {
                                    "name": "SayHi",
                                    "arguments": "null"
                                }
                            }
                        ]
                    },
                    {
                        "role": "tool",
                        "tool_call_id": "12345",
                        "content": "{ \"$type\": \"text\", \"text\": \"Said hello\" }"
                    },
                    {
                        "role":"tool",
                        "tool_call_id":"12346",
                        "content":"Said hi"
                    },
                    {
                        "role":"assistant",
                        "content":"You are great."
                    },
                    {
                        "role":"user",
                        "content":"Thanks!"
                    }
                ],
                "model":"gpt-4o-mini"
            }
            `
	const output = `
            {
              "id": "chatcmpl-ADyV17bXeSm5rzUx3n46O7m3M0o3P",
              "object": "chat.completion",
              "created": 1727894187,
              "model": "gpt-4o-mini-2024-07-18",
              "choices": [
                {
                  "index": 0,
                  "message": {
                    "role": "assistant",
                    "content": "I'm doing well, thank you! What's on your mind today?",
                    "refusal": null
                  },
                  "logprobs": null,
                  "finish_reason": "stop"
                }
              ],
              "usage": {
                "prompt_tokens": 42,
                "completion_tokens": 15,
                "total_tokens": 57,
                "prompt_tokens_details": {
                  "cached_tokens": 20
                },
                "completion_tokens_details": {
                  "reasoning_tokens": 90
                }
              },
              "system_fingerprint": "fp_f85bea6784"
            }
            `
	want := []*message.Message{
		{
			ID:        "chatcmpl-ADyV17bXeSm5rzUx3n46O7m3M0o3P",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1727894187, 0),
			Contents: []message.Content{
				&message.TextContent{
					Text: "I'm doing well, thank you! What's on your mind today?",
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:       42,
						OutputTokenCount:      15,
						TotalTokenCount:       57,
						CachedInputTokenCount: 20,
						ReasoningTokenCount:   90,
						AdditionalCounts: map[string]int64{
							"PromptTokensDetails.AudioTokens":                  0,
							"CompletionTokensDetails.AudioTokens":              0,
							"CompletionTokensDetails.AcceptedPredictionTokens": 0,
							"CompletionTokensDetails.RejectedPredictionTokens": 0,
						},
					},
				},
			},
		},
	}

	server := newTestServer(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	messages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "You are a really nice friend."}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hello!"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "hi, how are you?"},
			&message.FunctionCallContent{CallID: "12345", Name: "SayHello", Arguments: "null"},
			&message.FunctionCallContent{CallID: "12346", Name: "SayHi", Arguments: "null"},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "12345", Result: `{ "$type": "text", "text": "Said hello" }`},
			&message.FunctionResultContent{CallID: "12346", Result: "Said hi"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "You are great."}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Thanks!"}}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
}

func TestChatOptions_Model_OverridesClientModel_NonStreaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "messages":[{"role":"user","content":"hello"}],
                "model":"gpt-4o",
                "max_completion_tokens":10
            }
            `
	const output = `
            {
              "id": "chatcmpl-ADx3PvAnCwJg0woha4pYsBTi3ZpOI",
              "object": "chat.completion",
              "created": 1727888631,
              "model": "gpt-4o-2024-08-06",
              "choices": [
                {
                  "index": 0,
                  "message": {
                    "role": "assistant",
                    "content": "Hello! How can I assist you today?",
                    "refusal": null
                  },
                  "logprobs": null,
                  "finish_reason": "stop"
                }
              ],
              "usage": {
                "prompt_tokens": 8,
                "completion_tokens": 9,
                "total_tokens": 17
              }
            }
            `

	server := newTestServer(t, input, output)
	defer server.Close()

	// Create client with gpt-4o-mini model
	a := newTestClient(server)

	// Override with gpt-4o in options
	resp, err := a.RunText(t.Context(), "hello",
		openaiprovider.ChatCompletionNewParams(openai.ChatCompletionNewParams{
			Model:               "gpt-4o",
			MaxCompletionTokens: openai.Int(10),
			Temperature:         openai.Float(0.5),
		}),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Verify the response contains the expected content
	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	if resp.Messages[0].ID != "chatcmpl-ADx3PvAnCwJg0woha4pYsBTi3ZpOI" {
		t.Errorf("expected ID chatcmpl-ADx3PvAnCwJg0woha4pYsBTi3ZpOI, got %s", resp.Messages[0].ID)
	}
}

func TestChatOptions_Model_OverridesClientModel_Streaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "messages":[{"role":"user","content":"hello"}],
                "model":"gpt-4o",
                "stream":true,
                "max_completion_tokens":20
            }
            `
	const output = `data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f85bea6784","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":null}

data: {"id":"chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK","object":"chat.completion.chunk","created":1727889370,"model":"gpt-4o-2024-08-06","system_fingerprint":"fp_f85bea6784","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":9,"total_tokens":17}}

data: [DONE]

`

	server := newTestServerStreaming(t, input, output)
	defer server.Close()

	// Create client with gpt-4o-mini model
	a := newTestClient(server)

	var updates []*agent.ResponseUpdate
	// Override with gpt-4o in options
	for update, err := range a.RunText(t.Context(), "hello", agent.Stream(true),
		openaiprovider.ChatCompletionNewParams(openai.ChatCompletionNewParams{
			Model:               "gpt-4o",
			MaxCompletionTokens: openai.Int(20),
			Temperature:         openai.Float(0.5),
		}),
	) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Verify we got the expected text
	var text string
	for _, update := range updates {
		for _, content := range update.Contents {
			if tc, ok := content.(*message.TextContent); ok {
				text += tc.Text
			}
		}
	}
	if text != "Hello!" {
		t.Errorf("expected text 'Hello!', got %q", text)
	}

	// Verify all updates have the correct message ID
	for _, update := range updates {
		if update.MessageID != "chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK" {
			t.Errorf("expected message ID chatcmpl-ADxFKtX6xIwdWRN42QvBj2u1RZpCK, got %s", update.MessageID)
			break
		}
	}
}

func TestChatDataContentMessage_Image_NonStreaming(t *testing.T) {
	// A minimal 1x1 PNG image as a data URI (red pixel)
	imageDataURI := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

	input := `
            {
              "messages": [
                {
                  "role": "user",
                  "content": [
                    {
                      "type": "text",
                      "text": "What does this logo say?"
                    },
                    {
                      "type": "image_url",
                      "image_url": {
                        "detail": "high",
                        "url": "` + imageDataURI + `"
                      }
                    }
                  ]
                }
              ],
              "model": "gpt-4o-mini"
            }
            `
	const output = `
            {
              "choices": [
                {
                  "finish_reason": "stop",
                  "index": 0,
                  "logprobs": null,
                  "message": {
                    "content": "The logo says \".NET\", which is a software development framework created by Microsoft.",
                    "refusal": null,
                    "role": "assistant"
                  }
                }
              ],
              "created": 1743531271,
              "id": "chatcmpl-BHaQ3nkeSDGhLzLya3mGbB1EXSqve",
              "model": "gpt-4o-mini-2024-07-18",
              "object": "chat.completion",
              "system_fingerprint": "fp_b705f0c291",
              "usage": {
                "completion_tokens": 56,
                "completion_tokens_details": {
                  "accepted_prediction_tokens": 0,
                  "audio_tokens": 0,
                  "reasoning_tokens": 0,
                  "rejected_prediction_tokens": 0
                },
                "prompt_tokens": 8513,
                "prompt_tokens_details": {
                  "audio_tokens": 0,
                  "cached_tokens": 0
                },
                "total_tokens": 8569
              }
            }
            `

	want := []*message.Message{
		{
			ID:        "chatcmpl-BHaQ3nkeSDGhLzLya3mGbB1EXSqve",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1743531271, 0),
			Contents: []message.Content{
				&message.TextContent{
					Text: "The logo says \".NET\", which is a software development framework created by Microsoft.",
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:  8513,
						OutputTokenCount: 56,
						TotalTokenCount:  8569,
						AdditionalCounts: map[string]int64{
							"PromptTokensDetails.AudioTokens":                  0,
							"CompletionTokensDetails.AudioTokens":              0,
							"CompletionTokensDetails.AcceptedPredictionTokens": 0,
							"CompletionTokensDetails.RejectedPredictionTokens": 0,
						},
					},
				},
			},
		},
	}

	server := newTestServer(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	// Create DataContent from the data URI
	dataContent := &message.DataContent{
		Data:      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==",
		MediaType: "image/png",
	}
	// Add "detail" to AdditionalProperties
	dataContent.AdditionalProperties = map[string]any{
		"detail": "high",
	}

	messages := []*message.Message{
		{
			Role: message.RoleUser,
			Contents: []message.Content{
				&message.TextContent{Text: "What does this logo say?"},
				dataContent,
			},
		},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
}

func TestChatMultipleRequiredFunctions(t *testing.T) {
	const input = `
            {
                "tools": [
                    {
                        "function": {
                            "description": "Get the current weather for a location",
                            "name": "GetWeather",
                            "parameters": {
                                "type": "object",
                                "required": [
                                    "location"
                                ],
                                "properties": {
                                    "location": {
                                        "description": "The city and state, e.g. San Francisco, CA",
                                        "type": "string"
                                    }
                                },
                                "additionalProperties": false
                            }
                        },
                        "type": "function"
                    },
                    {
                        "function": {
                            "description": "Get the current time for a location",
                            "name": "GetTime",
                            "parameters": {
                                "type": "object",
                                "required": [
                                    "location"
                                ],
                                "properties": {
                                    "location": {
                                        "description": "The city and state, e.g. San Francisco, CA",
                                        "type": "string"
                                    }
                                },
                                "additionalProperties": false
                            }
                        },
                        "type": "function"
                    }
                ],
                "tool_choice": {
                    "type": "allowed_tools",
                    "allowed_tools": {
                        "mode": "required",
                        "tools": [
                            {
                                "type": "function",
                                "function": {
                                    "name": "GetWeather"
                                }
                            },
                            {
                                "type": "function",
                                "function": {
                                    "name": "GetTime"
                                }
                            }
                        ]
                    }
                },
                "messages": [
                    {
                        "role": "user",
                        "content": "What's the weather and time in Seattle?"
                    }
                ],
                "model": "gpt-4o-mini"
            }
            `
	const output = `
            {
              "id": "chatcmpl-TestMultiRequired123",
              "object": "chat.completion",
              "created": 1727900000,
              "model": "gpt-4o-mini-2024-07-18",
              "choices": [
                {
                  "index": 0,
                  "message": {
                    "role": "assistant",
                    "content": null,
                    "tool_calls": [
                      {
                        "id": "call_weather_123",
                        "type": "function",
                        "function": {
                          "name": "GetWeather",
                          "arguments": "{\"location\":\"Seattle, WA\"}"
                        }
                      },
                      {
                        "id": "call_time_456",
                        "type": "function",
                        "function": {
                          "name": "GetTime",
                          "arguments": "{\"location\":\"Seattle, WA\"}"
                        }
                      }
                    ],
                    "refusal": null
                  },
                  "logprobs": null,
                  "finish_reason": "tool_calls"
                }
              ],
              "system_fingerprint": "fp_test123"
            }
            `

	want := []*message.Message{
		{
			ID:        "chatcmpl-TestMultiRequired123",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1727900000, 0),
			Contents: []message.Content{
				&message.FunctionCallContent{
					CallID:    "call_weather_123",
					Name:      "GetWeather",
					Arguments: `{"location":"Seattle, WA"}`,
				},
				&message.FunctionCallContent{
					CallID:    "call_time_456",
					Name:      "GetTime",
					Arguments: `{"location":"Seattle, WA"}`,
				},
			},
		},
	}

	server := newTestServer(t, input, output)
	defer server.Close()

	a := newTestClient(server)

	type LocationInput struct {
		Location string `json:"location" jsonschema:"The city and state, e.g. San Francisco, CA"`
	}

	getWeather := func(ctx context.Context, input LocationInput) (string, error) {
		return "Sunny, 72°F", nil
	}

	getTime := func(ctx context.Context, input LocationInput) (string, error) {
		return "3:45 PM", nil
	}

	weatherTool := functool.MustNew(functool.Config{
		Name:        "GetWeather",
		Description: "Get the current weather for a location",
	}, getWeather)

	timeTool := functool.MustNew(functool.Config{
		Name:        "GetTime",
		Description: "Get the current time for a location",
	}, getTime)

	resp, err := a.RunText(t.Context(), "What's the weather and time in Seattle?",
		agent.WithTool(weatherTool),
		agent.WithTool(timeTool),
		agent.WithToolMode(tool.RequireTools("GetWeather", "GetTime")),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
}
