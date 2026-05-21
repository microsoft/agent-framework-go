// Copyright (c) Microsoft. All rights reserved.

package openaiagent_test

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
	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/messagetest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// continuationToken represents the structure of a continuation token for background responses
type continuationToken struct {
	ResponseID     string `json:"response_id"`
	SequenceNumber int64  `json:"sequence_number"`
}

// Helper functions for responses tests
func newTestResponsesServer(t *testing.T, input string, output string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed reading request body: %v", err)
		}
		responsesBodyEqual(t, string(body), input)
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, output); err != nil {
			t.Fatalf("failed writing response: %v", err)
		}
	}))
}

func newTestResponsesServerStreaming(t *testing.T, input string, output string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed reading request body: %v", err)
		}
		responsesBodyEqual(t, string(body), input)
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, output); err != nil {
			t.Fatalf("failed writing response: %v", err)
		}
	}))
}

func newTestResponsesClient(server *httptest.Server, model string) *agent.Agent {
	return openaiagent.NewResponses(
		openai.NewClient(option.WithBaseURL(server.URL)),
		openaiagent.Config{
			Model:  model,
			Config: agent.Config{DisableFuncAutoCall: true},
		},
	)
}

func TestResponsesConfigInstructions_NonStreaming(t *testing.T) {
	const input = `
						{
								"instructions":"Be concise.",
								"model":"gpt-4o-mini",
								"input": [{
										"type":"message",
										"role":"user",
										"content":[{"type":"input_text","text":"hello"}]
								}]
						}
						`
	const output = `
						{
							"id": "resp_test",
							"object": "response",
							"created_at": 1741891428,
							"status": "completed",
							"error": null,
							"incomplete_details": null,
							"instructions": "Be concise.",
							"model": "gpt-4o-mini",
							"output": [{
								"type": "message",
								"id": "msg_test",
								"status": "completed",
								"role": "assistant",
								"content": [{"type": "output_text", "text": "Hello", "annotations": []}]
							}]
						}
						`
	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := openaiagent.NewResponses(
		openai.NewClient(option.WithBaseURL(server.URL)),
		openaiagent.Config{
			Model:        "gpt-4o-mini",
			Instructions: "Be concise.",
			Config: agent.Config{
				DisableFuncAutoCall: true,
			},
		},
	)

	if _, err := a.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestResponsesBasicRequestResponse_NonStreaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o-mini",
                "input": [{
                    "type":"message",
                    "role":"user",
                    "content":[{"type":"input_text","text":"hello"}]
                }],
                "max_output_tokens":20
            }
            `

	const output = `
            {
              "id": "resp_67d327649b288191aeb46a824e49dc40058a5e08c46a181d",
              "object": "response",
              "created_at": 1741891428,
              "status": "completed",
              "error": null,
              "incomplete_details": null,
              "instructions": null,
              "max_output_tokens": 20,
              "model": "gpt-4o-mini-2024-07-18",
              "output": [
                {
                  "type": "message",
                  "id": "msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d",
                  "status": "completed",
                  "role": "assistant",
                  "content": [
                    {
                      "type": "output_text",
                      "text": "Hello! How can I assist you today?",
                      "annotations": []
                    }
                  ]
                }
              ],
              "parallel_tool_calls": true,
              "previous_response_id": null,
              "reasoning": {
                "effort": null,
                "generate_summary": null
              },
              "store": true,
              "temperature": 0.5,
              "text": {
                "format": {
                  "type": "text"
                }
              },
              "tool_choice": "auto",
              "tools": [],
              "top_p": 1.0,
              "usage": {
                "input_tokens": 26,
                "input_tokens_details": {
                  "cached_tokens": 0
                },
                "output_tokens": 10,
                "output_tokens_details": {
                  "reasoning_tokens": 0
                },
                "total_tokens": 36
              },
              "user": null,
              "metadata": {}
            }
            `

	want := []*message.Message{
		{
			ID:        "msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1741891428, 0),
			Contents: []message.Content{
				&message.TextContent{
					Text: "Hello! How can I assist you today?",
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:  26,
						OutputTokenCount: 10,
						TotalTokenCount:  36,
					},
				},
			},
		},
	}

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "hello",
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}

	// Verify the response is complete (removed ID check as Message.ID is now set to message ID from output, not response ID)
}

func TestResponsesBasicRequestResponse_Streaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"hello"}]
                    }
                ],
                "stream":true,
                "max_output_tokens":20
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_67d329fbc87c81919f8952fe71dafc96029dabe3ee19bb77","object":"response","created_at":1741892091,"status":"in_progress","error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"generate_summary":null},"store":true,"temperature":0.5,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"usage":null,"user":null,"metadata":{}}}

event: response.in_progress
data: {"type":"response.in_progress","response":{"id":"resp_67d329fbc87c81919f8952fe71dafc96029dabe3ee19bb77","object":"response","created_at":1741892091,"status":"in_progress","error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"generate_summary":null},"store":true,"temperature":0.5,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"usage":null,"user":null,"metadata":{}}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"in_progress","role":"assistant","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":"Hello"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":"!"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":" How"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":" can"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":" I"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":" assist"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":" you"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":" today"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":"?"}

event: response.output_text.done
data: {"type":"response.output_text.done","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"text":"Hello! How can I assist you today?"}

event: response.content_part.done
data: {"type":"response.content_part.done","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello! How can I assist you today?","annotations":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello! How can I assist you today?","annotations":[]}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_67d329fbc87c81919f8952fe71dafc96029dabe3ee19bb77","object":"response","created_at":1741892091,"status":"completed","error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello! How can I assist you today?","annotations":[]}]}],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"generate_summary":null},"store":true,"temperature":0.5,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"usage":{"input_tokens":26,"input_tokens_details":{"cached_tokens":0},"output_tokens":10,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":36},"user":null,"metadata":{}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "hello", agent.Stream(true),
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
	) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Verify we got expected number of updates (17 based on C# test)
	if len(updates) != 17 {
		t.Errorf("expected 17 updates, got %d", len(updates))
	}

	// Concatenate all text from updates
	var fullText strings.Builder
	for _, update := range updates {
		for _, content := range update.Contents {
			if tc, ok := content.(*message.TextContent); ok {
				fullText.WriteString(tc.Text)
			}
		}
	}

	if fullText.String() != "Hello! How can I assist you today?" {
		t.Errorf("expected text 'Hello! How can I assist you today?', got %q", fullText.String())
	}

	// Verify all updates have correct metadata
	responseID := "resp_67d329fbc87c81919f8952fe71dafc96029dabe3ee19bb77"
	createdAt := time.Unix(1741892091, 0)
	for i, update := range updates {
		if update.ResponseID != responseID {
			t.Errorf("update %d: expected ResponseId %s, got %s", i, responseID, update.ResponseID)
		}
		if !update.CreatedAt.Equal(createdAt) {
			t.Errorf("update %d: expected CreatedAt %v, got %v", i, createdAt, update.CreatedAt)
		}
	}

	// Verify usage content in last update
	var usageFound bool
	for _, content := range updates[len(updates)-1].Contents {
		if uc, ok := content.(*message.UsageContent); ok {
			usageFound = true
			if uc.Details.InputTokenCount != 26 {
				t.Errorf("expected InputTokenCount 26, got %d", uc.Details.InputTokenCount)
			}
			if uc.Details.OutputTokenCount != 10 {
				t.Errorf("expected OutputTokenCount 10, got %d", uc.Details.OutputTokenCount)
			}
			if uc.Details.TotalTokenCount != 36 {
				t.Errorf("expected TotalTokenCount 36, got %d", uc.Details.TotalTokenCount)
			}
		}
	}
	if !usageFound {
		t.Error("expected usage content in last update")
	}
}

func TestResponsesBasicReasoningResponse_Streaming(t *testing.T) {
	const input = `
            {
              "input":[{
                "type":"message",
                "role":"user",
                "content":[{
                  "type":"input_text",
                  "text":"Calculate the sum of the first 5 positive integers."
                }]
              }],
              "model": "o4-mini",
              "stream": true
            }
            `

	// Compressed down for testing purposes
	const output = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_68b5ebab461881969ed94149372c2a530698ecbf1b9f2704","object":"response","created_at":1756752811,"status":"in_progress","background":false,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"max_tool_calls":null,"model":"o4-mini-2025-04-16","output":[],"parallel_tool_calls":true,"previous_response_id":null,"prompt_cache_key":null,"reasoning":{"effort":"low","summary":"detailed"},"safety_identifier":null,"service_tier":"auto","store":true,"temperature":1.0,"text":{"format":{"type":"text"},"verbosity":"medium"},"tool_choice":"auto","tools":[],"top_logprobs":0,"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{}}}

event: response.in_progress
data: {"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_68b5ebab461881969ed94149372c2a530698ecbf1b9f2704","object":"response","created_at":1756752811,"status":"in_progress","background":false,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"max_tool_calls":null,"model":"o4-mini-2025-04-16","output":[],"parallel_tool_calls":true,"previous_response_id":null,"prompt_cache_key":null,"reasoning":{"effort":"low","summary":"detailed"},"safety_identifier":null,"service_tier":"auto","store":true,"temperature":1.0,"text":{"format":{"type":"text"},"verbosity":"medium"},"tool_choice":"auto","tools":[],"top_logprobs":0,"top_p":1.0,"truncation":"disabled","usage":null,"user":null,"metadata":{}}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","type":"reasoning","summary":[]}}

event: response.reasoning_summary_part.added
data: {"type":"response.reasoning_summary_part.added","sequence_number":3,"item_id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","sequence_number":4,"item_id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","output_index":0,"summary_index":0,"delta":"**Calcul","obfuscation":"sLkbFySM"}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","sequence_number":5,"item_id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","output_index":0,"summary_index":0,"delta":"ating","obfuscation":"dkm1f6DKqUj"}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","sequence_number":6,"item_id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","output_index":0,"summary_index":0,"delta":" a","obfuscation":"X8ahc2lfCf9eA1"}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","sequence_number":7,"item_id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","output_index":0,"summary_index":0,"delta":" simple","obfuscation":"1rLVyIaNl"}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","sequence_number":8,"item_id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","output_index":0,"summary_index":0,"delta":" sum","obfuscation":"jCK7mgNR80Re"}

event: response.reasoning_summary_text.done
data: {"type":"response.reasoning_summary_text.done","sequence_number":9,"item_id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","output_index":0,"summary_index":0,"text":"**Calculating a simple sum**"}

event: response.reasoning_summary_part.done
data: {"type":"response.reasoning_summary_part.done","sequence_number":10,"item_id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":"**Calculating a simple sum**"}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":11,"output_index":0,"item":{"id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","type":"reasoning","summary":[{"type":"summary_text","text":"**Calculating a simple sum**"}]}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":12,"output_index":1,"item":{"id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","type":"message","status":"in_progress","content":[],"role":"assistant"}}

event: response.content_part.added
data: {"type":"response.content_part.added","sequence_number":13,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":""}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":14,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":"The","logprobs":[],"obfuscation":"japg2KaCkjNsp"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":15,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" sum","logprobs":[],"obfuscation":"1BEqjKQ0KU41"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":16,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" of","logprobs":[],"obfuscation":"GUqom1rsdZsnT"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":17,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" the","logprobs":[],"obfuscation":"UmCms91yrTlg"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":18,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" first","logprobs":[],"obfuscation":"AyNbZpfTXo"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":19,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" ","logprobs":[],"obfuscation":"tuyz4HkKODFQRtk"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":20,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":"5","logprobs":[],"obfuscation":"QAwyISolmjXfTlc"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":21,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" positive","obfuscation":"2Euge1H"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":22,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" integers","obfuscation":"ih0Znt8"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":23,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" is","obfuscation":"oQihR5Pw8jRz5"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":24,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":" 15","obfuscation":"7TdJ1FWlZF8lTd"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":25,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"delta":".","obfuscation":"x2VAJKlWI8qjgYq"}

event: response.output_text.done
data: {"type":"response.output_text.done","sequence_number":26,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"text":"The sum of the first 5 positive integers is 15.","logprobs":[]}

event: response.content_part.done
data: {"type":"response.content_part.done","sequence_number":27,"item_id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","output_index":1,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":"The sum of the first 5 positive integers is 15."}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":28,"output_index":1,"item":{"id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":"The sum of the first 5 positive integers is 15."}],"role":"assistant"}}

event: response.completed
data: {"type":"response.completed","sequence_number":29,"response":{"id":"resp_68b5ebab461881969ed94149372c2a530698ecbf1b9f2704","object":"response","created_at":1756752811,"status":"completed","background":false,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"max_tool_calls":null,"model":"o4-mini-2025-04-16","output":[{"id":"rs_68b5ebabc0088196afb9fa86b487732d0698ecbf1b9f2704","type":"reasoning","summary":[{"type":"summary_text","text":"**Calculating a simple sum**"}]},{"id":"msg_68b5ebae5a708196b74b94f22ca8995e0698ecbf1b9f2704","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":"The sum of the first 5 positive integers is 15."}],"role":"assistant"}],"parallel_tool_calls":true,"previous_response_id":null,"prompt_cache_key":null,"reasoning":{"effort":"low","summary":"detailed"},"safety_identifier":null,"service_tier":"default","store":true,"temperature":1.0,"text":{"format":{"type":"text"},"verbosity":"medium"},"tool_choice":"auto","tools":[],"top_logprobs":0,"top_p":1.0,"truncation":"disabled","usage":{"input_tokens":17,"input_tokens_details":{"cached_tokens":0},"output_tokens":122,"output_tokens_details":{"reasoning_tokens":64},"total_tokens":139},"user":null,"metadata":{}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "o4-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Calculate the sum of the first 5 positive integers.", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Verify we got expected number of updates (30 based on C# test)
	if len(updates) != 30 {
		t.Errorf("expected 30 updates, got %d", len(updates))
	}

	// Concatenate all assistant text from updates
	var fullText strings.Builder
	for _, update := range updates {
		if update.Role == message.RoleAssistant {
			for _, content := range update.Contents {
				if tc, ok := content.(*message.TextContent); ok {
					fullText.WriteString(tc.Text)
				}
			}
		}
	}

	expected := "The sum of the first 5 positive integers is 15."
	if fullText.String() != expected {
		t.Errorf("expected text %q, got %q", expected, fullText.String())
	}

	// Verify reasoning content exists in the stream
	var reasoningFound bool
	for _, update := range updates {
		for _, content := range update.Contents {
			if _, ok := content.(*message.TextReasoningContent); ok {
				reasoningFound = true
				break
			}
		}
		if reasoningFound {
			break
		}
	}
	if !reasoningFound {
		t.Error("expected reasoning content in updates")
	}

	// Verify usage content
	var usageFound bool
	for _, content := range updates[len(updates)-1].Contents {
		if uc, ok := content.(*message.UsageContent); ok {
			usageFound = true
			if uc.Details.InputTokenCount != 17 {
				t.Errorf("expected InputTokenCount 17, got %d", uc.Details.InputTokenCount)
			}
			if uc.Details.OutputTokenCount != 122 {
				t.Errorf("expected OutputTokenCount 122, got %d", uc.Details.OutputTokenCount)
			}
			if uc.Details.TotalTokenCount != 139 {
				t.Errorf("expected TotalTokenCount 139, got %d", uc.Details.TotalTokenCount)
			}
		}
	}
	if !usageFound {
		t.Error("expected usage content in last update")
	}
}

func TestResponsesChatOptions_Model_OverridesClientModel_NonStreaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
                "model":"gpt-4o",
                "max_output_tokens":10
            }
            `
	const output = `
            {
              "id": "resp_67d327649b288191aeb46a824e49dc40058a5e08c46a181d",
              "object": "response",
              "created_at": 1727888631,
              "status": "completed",
              "model": "gpt-4o-2024-08-06",
              "output": [
                {
                  "type": "message",
                  "id": "msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d",
                  "status": "completed",
                  "role": "assistant",
                  "content": [
                    {
                      "type": "output_text",
                      "text": "Hello! How can I assist you today?",
                      "annotations": []
                    }
                  ]
                }
              ],
              "usage": {
                "input_tokens": 8,
                "output_tokens": 9,
                "total_tokens": 17
              }
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	// Create client with gpt-4o-mini model
	a := newTestResponsesClient(server, "gpt-4o-mini")

	// Override with gpt-4o in options
	resp, err := a.RunText(t.Context(), "hello",
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			Model:           "gpt-4o",
			MaxOutputTokens: openai.Int(10),
			Temperature:     openai.Float(0.5),
		}),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Verify the response contains the expected content
	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	// (removed ID check as Message.ID is now set to message ID from output, not response ID)
}

func TestResponsesMultipleMessages_NonStreaming(t *testing.T) {
	const input = `
            {
                "temperature": 0.25,
                "input": [
                    {
                        "type": "message",
                        "role": "user",
                        "content": [{"type": "input_text", "text": "hello!"}]
                    },
                    {
                        "type": "message",
                        "role": "assistant",
                        "content": [{"type": "input_text", "text": "hi, how are you?"}]
                    },
                    {
                        "type": "message",
                        "role": "user",
                        "content": [{"type": "input_text", "text": "i'm good. how are you?"}]
                    }
                ],
                "model": "gpt-4o-mini"
            }
            `
	const output = `
            {
              "id": "resp_ADyV17bXeSm5rzUx3n46O7m3M0o3P",
              "object": "response",
              "created_at": 1727894187,
              "status": "completed",
              "model": "gpt-4o-mini-2024-07-18",
              "output": [
                {
                  "type": "message",
                  "id": "msg_ADyV17bXeSm5rzUx3n46O7m3M0o3P",
                  "status": "completed",
                  "role": "assistant",
                  "content": [
                    {
                      "type": "output_text",
                      "text": "I'm doing well, thank you! What's on your mind today?",
                      "annotations": []
                    }
                  ]
                }
              ],
              "usage": {
                "input_tokens": 42,
                "output_tokens": 15,
                "total_tokens": 57
              }
            }
            `
	want := []*message.Message{
		{
			ID:        "msg_ADyV17bXeSm5rzUx3n46O7m3M0o3P",
			Role:      message.RoleAssistant,
			CreatedAt: time.Unix(1727894187, 0),
			Contents: []message.Content{
				&message.TextContent{
					Text: "I'm doing well, thank you! What's on your mind today?",
				},
				&message.UsageContent{
					Details: message.UsageDetails{
						InputTokenCount:  42,
						OutputTokenCount: 15,
						TotalTokenCount:  57,
					},
				},
			},
		},
	}

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hello!"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "hi, how are you?"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "i'm good. how are you?"}}},
	}

	resp, err := a.Run(t.Context(), messages,
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			Temperature: openai.Float(0.25),
		}),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if err := messagetest.MessagesEqual(resp.Messages, want); err != nil {
		t.Error(err)
	}
}

func TestResponsesDataContentMessage_Image_NonStreaming(t *testing.T) {
	// A minimal 1x1 PNG image as a data URI (red pixel)
	_ = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

	input := `
            {
              "input": [
                {
                  "type": "message",
                  "role": "user",
                  "content": [
                    {
                      "type": "input_text",
                      "text": "What does this logo say?"
                    },
                    {
                      "type": "input_image",
                      "image_url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==",
                      "detail": "high"
                    }
                  ]
                }
              ],
              "model": "gpt-4o-mini"
            }
            `
	const output = `
            {
              "id": "resp_BHaQ3nkeSDGhLzLya3mGbB1EXSqve",
              "object": "response",
              "created_at": 1743531271,
              "status": "completed",
              "model": "gpt-4o-mini-2024-07-18",
              "output": [
                {
                  "type": "message",
                  "id": "msg_BHaQ3nkeSDGhLzLya3mGbB1EXSqve",
                  "status": "completed",
                  "role": "assistant",
                  "content": [
                    {
                      "type": "output_text",
                      "text": "The logo says \".NET\", which is a software development framework created by Microsoft.",
                      "annotations": []
                    }
                  ]
                }
              ],
              "usage": {
                "input_tokens": 8513,
                "output_tokens": 56,
                "total_tokens": 8569
              }
            }
            `

	want := []*message.Message{
		{
			ID:        "msg_BHaQ3nkeSDGhLzLya3mGbB1EXSqve",
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
					},
				},
			},
		},
	}

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

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

func TestResponsesReasoningTextDelta_Streaming(t *testing.T) {
	const input = `
            {
              "input":[{
                "type":"message",
                "role":"user",
                "content":[{
                  "type":"input_text",
                  "text":"Solve this problem step by step."
                }]
              }],
              "model": "o4-mini",
              "stream": true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_reasoning123","object":"response","created_at":1756752900,"status":"in_progress","model":"o4-mini-2025-04-16","output":[],"reasoning":{"effort":"medium"}}}

event: response.in_progress
data: {"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_reasoning123","object":"response","created_at":1756752900,"status":"in_progress","model":"o4-mini-2025-04-16","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"rs_reasoning123","type":"reasoning","text":""}}

event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","sequence_number":3,"item_id":"rs_reasoning123","output_index":0,"delta":"First, "}

event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","sequence_number":4,"item_id":"rs_reasoning123","output_index":0,"delta":"let's analyze "}

event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","sequence_number":5,"item_id":"rs_reasoning123","output_index":0,"delta":"the problem."}

event: response.reasoning_text.done
data: {"type":"response.reasoning_text.done","sequence_number":6,"item_id":"rs_reasoning123","output_index":0,"text":"First, let's analyze the problem."}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":7,"output_index":0,"item":{"id":"rs_reasoning123","type":"reasoning","text":"First, let's analyze the problem."}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":8,"output_index":1,"item":{"id":"msg_reasoning123","type":"message","status":"in_progress","content":[],"role":"assistant"}}

event: response.content_part.added
data: {"type":"response.content_part.added","sequence_number":9,"item_id":"msg_reasoning123","output_index":1,"content_index":0,"part":{"type":"output_text","annotations":[],"text":""}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":10,"item_id":"msg_reasoning123","output_index":1,"content_index":0,"delta":"The solution is 42."}

event: response.output_text.done
data: {"type":"response.output_text.done","sequence_number":11,"item_id":"msg_reasoning123","output_index":1,"content_index":0,"text":"The solution is 42."}

event: response.content_part.done
data: {"type":"response.content_part.done","sequence_number":12,"item_id":"msg_reasoning123","output_index":1,"content_index":0,"part":{"type":"output_text","annotations":[],"text":"The solution is 42."}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":13,"output_index":1,"item":{"id":"msg_reasoning123","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"text":"The solution is 42."}],"role":"assistant"}}

event: response.completed
data: {"type":"response.completed","sequence_number":14,"response":{"id":"resp_reasoning123","object":"response","created_at":1756752900,"status":"completed","model":"o4-mini-2025-04-16","output":[{"id":"rs_reasoning123","type":"reasoning","text":"First, let's analyze the problem."},{"id":"msg_reasoning123","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"text":"The solution is 42."}],"role":"assistant"}],"usage":{"input_tokens":10,"output_tokens":25,"total_tokens":35}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "o4-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Solve this problem step by step.", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Verify we got expected number of updates (15 based on C# test)
	if len(updates) != 15 {
		t.Errorf("expected 15 updates, got %d", len(updates))
	}

	// Concatenate all assistant text from updates
	var assistantText strings.Builder
	for _, update := range updates {
		if update.Role == message.RoleAssistant {
			for _, content := range update.Contents {
				if tc, ok := content.(*message.TextContent); ok {
					assistantText.WriteString(tc.Text)
				}
			}
		}
	}

	if assistantText.String() != "The solution is 42." {
		t.Errorf("expected assistant text 'The solution is 42.', got %q", assistantText.String())
	}

	// Verify all updates have correct metadata
	responseID := "resp_reasoning123"
	createdAt := time.Unix(1756752900, 0)
	for i, update := range updates {
		if update.ResponseID != responseID {
			t.Errorf("update %d: expected ResponseId %s, got %s", i, responseID, update.ResponseID)
		}
		if !update.CreatedAt.Equal(createdAt) {
			t.Errorf("update %d: expected CreatedAt %v, got %v", i, createdAt, update.CreatedAt)
		}
	}

	// Verify reasoning text delta updates (indices 3-5)
	var reasoningText strings.Builder
	for i := 3; i <= 5; i++ {
		found := false
		for _, content := range updates[i].Contents {
			if tc, ok := content.(*message.TextReasoningContent); ok {
				reasoningText.WriteString(tc.Text)
				found = true
			}
		}
		if !found {
			t.Errorf("update %d: expected TextReasoningContent", i)
		}
	}

	if reasoningText.String() != "First, let's analyze the problem." {
		t.Errorf("expected reasoning text 'First, let's analyze the problem.', got %q", reasoningText.String())
	}

	// Verify usage content in last update
	var usageFound bool
	for _, content := range updates[len(updates)-1].Contents {
		if uc, ok := content.(*message.UsageContent); ok {
			usageFound = true
			if uc.Details.InputTokenCount != 10 {
				t.Errorf("expected InputTokenCount 10, got %d", uc.Details.InputTokenCount)
			}
			if uc.Details.OutputTokenCount != 25 {
				t.Errorf("expected OutputTokenCount 25, got %d", uc.Details.OutputTokenCount)
			}
			if uc.Details.TotalTokenCount != 35 {
				t.Errorf("expected TotalTokenCount 35, got %d", uc.Details.TotalTokenCount)
			}
		}
	}
	if !usageFound {
		t.Error("expected usage content in last update")
	}
}

func TestResponsesChatOptions_Model_OverridesClientModel_Streaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
                "stream":true,
                "max_output_tokens":20
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_streaming123","object":"response","created_at":1741891428,"status":"in_progress","model":"gpt-4o-2024-08-06","output":[]}}

event: response.in_progress
data: {"type":"response.in_progress","response":{"id":"resp_streaming123","object":"response","created_at":1741891428,"status":"in_progress","model":"gpt-4o-2024-08-06","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_streaming123","status":"in_progress","role":"assistant","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","item_id":"msg_streaming123","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_streaming123","output_index":0,"content_index":0,"delta":"Hello!"}

event: response.output_text.done
data: {"type":"response.output_text.done","item_id":"msg_streaming123","output_index":0,"content_index":0,"text":"Hello!"}

event: response.content_part.done
data: {"type":"response.content_part.done","item_id":"msg_streaming123","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello!","annotations":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_streaming123","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_streaming123","object":"response","created_at":1741891428,"status":"completed","model":"gpt-4o-2024-08-06","output":[{"type":"message","id":"msg_streaming123","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}]}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	// Create client with gpt-4o-mini model
	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	// Override with gpt-4o in options
	for update, err := range a.RunText(t.Context(), "hello", agent.Stream(true),
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			Model:           "gpt-4o",
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
	) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Verify we got some updates
	if len(updates) == 0 {
		t.Fatal("expected updates, got none")
	}

	// Verify the response ID
	if updates[0].ResponseID != "resp_streaming123" {
		t.Errorf("expected ResponseId resp_streaming123, got %s", updates[0].ResponseID)
	}

	// Concatenate all text
	var fullText strings.Builder
	for _, update := range updates {
		for _, content := range update.Contents {
			if tc, ok := content.(*message.TextContent); ok {
				fullText.WriteString(tc.Text)
			}
		}
	}

	if fullText.String() != "Hello!" {
		t.Errorf("expected text 'Hello!', got %q", fullText.String())
	}
}

func TestResponsesMultipleOutputItems_NonStreaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o-mini",
                "input": [{
                    "type":"message",
                    "role":"user",
                    "content":[{"type":"input_text","text":"hello"}]
                }],
                "max_output_tokens":20
            }
            `

	const output = `
            {
              "id": "resp_67d327649b288191aeb46a824e49dc40058a5e08c46a181d",
              "object": "response",
              "created_at": 1741891428,
              "status": "completed",
              "error": null,
              "incomplete_details": null,
              "instructions": null,
              "max_output_tokens": 20,
              "model": "gpt-4o-mini-2024-07-18",
              "output": [
                {
                  "type": "message",
                  "id": "msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d",
                  "status": "completed",
                  "role": "assistant",
                  "content": [
                    {
                      "type": "output_text",
                      "text": "Hello!",
                      "annotations": []
                    }
                  ]
                },
                {
                  "type": "message",
                  "id": "msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a182e",
                  "status": "completed",
                  "role": "assistant",
                  "content": [
                    {
                      "type": "output_text",
                      "text": " How can I assist you today?",
                      "annotations": []
                    }
                  ]
                }
              ],
              "parallel_tool_calls": true,
              "previous_response_id": null,
              "reasoning": {
                "effort": null,
                "generate_summary": null
              },
              "store": true,
              "temperature": 0.5,
              "text": {
                "format": {
                  "type": "text"
                }
              },
              "tool_choice": "auto",
              "tools": [],
              "top_p": 1.0,
              "usage": {
                "input_tokens": 26,
                "input_tokens_details": {
                  "cached_tokens": 0
                },
                "output_tokens": 10,
                "output_tokens_details": {
                  "reasoning_tokens": 0
                },
                "total_tokens": 36
              },
              "user": null,
              "metadata": {}
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "hello",
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Verify we got 2 messages (multiple output items)
	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}

	// Verify first message
	if resp.Messages[0].ID != "msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d" {
		t.Errorf("expected first message ID msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d, got %s", resp.Messages[0].ID)
	}
	if resp.Messages[0].Role != message.RoleAssistant {
		t.Errorf("expected first message role assistant, got %s", resp.Messages[0].Role)
	}
	var text1 string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			text1 = tc.Text
		}
	}
	if text1 != "Hello!" {
		t.Errorf("expected first message text 'Hello!', got %q", text1)
	}

	// Verify second message
	if resp.Messages[1].Role != message.RoleAssistant {
		t.Errorf("expected second message role assistant, got %s", resp.Messages[1].Role)
	}
	var text2 string
	for _, content := range resp.Messages[1].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			text2 = tc.Text
		}
	}
	if text2 != " How can I assist you today?" {
		t.Errorf("expected second message text ' How can I assist you today?', got %q", text2)
	}

	// Verify usage (should be in last message)
	var usageFound bool
	for _, content := range resp.Messages[len(resp.Messages)-1].Contents {
		if uc, ok := content.(*message.UsageContent); ok {
			usageFound = true
			if uc.Details.InputTokenCount != 26 {
				t.Errorf("expected InputTokenCount 26, got %d", uc.Details.InputTokenCount)
			}
			if uc.Details.OutputTokenCount != 10 {
				t.Errorf("expected OutputTokenCount 10, got %d", uc.Details.OutputTokenCount)
			}
			if uc.Details.TotalTokenCount != 36 {
				t.Errorf("expected TotalTokenCount 36, got %d", uc.Details.TotalTokenCount)
			}
		}
	}
	if !usageFound {
		t.Error("expected usage content in last message")
	}
}

func TestResponsesFunctionCallWithResult_NonStreaming(t *testing.T) {
	// First request - function call
	const input1 = `
            {
                "input": [{
                    "type": "message",
                    "role": "user",
                    "content": [{"type": "input_text", "text": "What's the weather in Seattle?"}]
                }],
                "model": "gpt-4o-mini",
                "tools": [{
                    "type": "function",
                    "name": "get_weather",
                    "description": "Get the current weather",
                    "parameters": {
                        "type": "object",
                        "required": ["location"],
                        "properties": {
                            "location": {
                                "description": "The city and state",
                                "type": "string"
                            }
                        },
                        "additionalProperties": false
                    }
                }]
            }
            `

	const output1 = `
            {
              "id": "resp_call123",
              "object": "response",
              "created_at": 1727894702,
              "status": "completed",
              "model": "gpt-4o-mini-2024-07-18",
              "output": [{
                  "type": "function_call",
                  "id": "call_abc123",
                  "name": "get_weather",
                  "arguments": "{\"location\":\"Seattle, WA\"}"
              }],
              "usage": {
                "input_tokens": 50,
                "output_tokens": 15,
                "total_tokens": 65
              }
            }
            `

	// Second request - with function result
	const input2 = `
            {
                "input": [
                    {
                        "type": "message",
                        "role": "user",
                        "content": [{"type": "input_text", "text": "What's the weather in Seattle?"}]
                    },
                    {
                        "type": "function_call_output",
                        "call_id": "call_abc123",
                        "output": "{\"temperature\":72,\"condition\":\"sunny\"}"
                    }
                ],
                "model": "gpt-4o-mini",
                "tools": [{
                    "type": "function",
                    "name": "get_weather",
                    "description": "Get the current weather",
                    "parameters": {
                        "type": "object",
                        "required": ["location"],
                        "properties": {
                            "location": {
                                "description": "The city and state",
                                "type": "string"
                            }
                        },
                        "additionalProperties": false
                    }
                }]
            }
            `

	const output2 = `
            {
              "id": "resp_result123",
              "object": "response",
              "created_at": 1727894750,
              "status": "completed",
              "model": "gpt-4o-mini-2024-07-18",
              "output": [{
                  "type": "message",
                  "id": "msg_result123",
                  "status": "completed",
                  "role": "assistant",
                  "content": [{
                      "type": "output_text",
                      "text": "The weather in Seattle is sunny with a temperature of 72°F.",
                      "annotations": []
                  }]
              }],
              "usage": {
                "input_tokens": 80,
                "output_tokens": 20,
                "total_tokens": 100
              }
            }
            `

	// Test first call - get function call
	server1 := newTestResponsesServer(t, input1, output1)
	defer server1.Close()

	a1 := newTestResponsesClient(server1, "gpt-4o-mini")

	type GetWeatherInput struct {
		Location string `json:"location" jsonschema:"The city and state"`
	}
	getWeather := func(ctx context.Context, input GetWeatherInput) (map[string]any, error) {
		return map[string]any{"temperature": 72, "condition": "sunny"}, nil
	}
	tool := functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Get the current weather",
	}, getWeather)

	resp1, err := a1.RunText(t.Context(), "What's the weather in Seattle?",
		agent.WithTool(tool),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Verify function call
	if len(resp1.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp1.Messages))
	}
	var funcCall *message.FunctionCallContent
	for _, content := range resp1.Messages[0].Contents {
		if fc, ok := content.(*message.FunctionCallContent); ok {
			funcCall = fc
			break
		}
	}
	if funcCall == nil {
		t.Fatal("expected function call content")
	}
	if funcCall.CallID != "call_abc123" {
		t.Errorf("expected call ID call_abc123, got %s", funcCall.CallID)
	}
	if funcCall.Name != "get_weather" {
		t.Errorf("expected function name get_weather, got %s", funcCall.Name)
	}

	// Test second call - with function result
	server2 := newTestResponsesServer(t, input2, output2)
	defer server2.Close()

	a2 := newTestResponsesClient(server2, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "What's the weather in Seattle?"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{
				CallID:    "call_abc123",
				Name:      "get_weather",
				Arguments: `{"location":"Seattle, WA"}`,
			},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_abc123",
				Result: `{"temperature":72,"condition":"sunny"}`,
			},
		}},
	}

	resp2, err := a2.Run(t.Context(), messages,
		agent.WithTool(tool),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Verify response
	if len(resp2.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp2.Messages))
	}
	var responseText string
	for _, content := range resp2.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "The weather in Seattle is sunny with a temperature of 72°F." {
		t.Errorf("expected weather response, got %q", responseText)
	}
}

func TestResponsesFunctionCall_UsesCallIDWhenDifferentFromID(t *testing.T) {
	const input1 = `
			{
				"input": [{
					"type": "message",
					"role": "user",
					"content": [{"type": "input_text", "text": "What's the weather in Amsterdam?"}]
				}],
				"model": "gpt-4o-mini",
				"tools": [{
					"type": "function",
					"name": "get_weather",
					"description": "Get the current weather",
					"parameters": {
						"type": "object",
						"required": ["location"],
						"properties": {
							"location": {
								"description": "The city and country",
								"type": "string"
							}
						},
						"additionalProperties": false
					}
				}]
			}
			`

	const output1 = `
			{
			  "id": "resp_fc_001",
			  "object": "response",
			  "created_at": 1727894702,
			  "status": "completed",
			  "model": "gpt-4o-mini-2024-07-18",
			  "output": [{
				  "type": "function_call",
				  "id": "fc_001",
				  "call_id": "call_weather_001",
				  "name": "get_weather",
				  "arguments": "{\"location\":\"Amsterdam\"}"
			  }]
			}
			`

	const input2 = `
			{
				"input": [
					{
						"type": "message",
						"role": "user",
						"content": [{"type": "input_text", "text": "What's the weather in Amsterdam?"}]
					},
					{
						"type": "function_call_output",
						"call_id": "call_weather_001",
						"output": "Cloudy, 15°C"
					}
				],
				"model": "gpt-4o-mini",
				"tools": [{
					"type": "function",
					"name": "get_weather",
					"description": "Get the current weather",
					"parameters": {
						"type": "object",
						"required": ["location"],
						"properties": {
							"location": {
								"description": "The city and country",
								"type": "string"
							}
						},
						"additionalProperties": false
					}
				}]
			}
			`

	const output2 = `
			{
			  "id": "resp_fc_002",
			  "object": "response",
			  "created_at": 1727894750,
			  "status": "completed",
			  "model": "gpt-4o-mini-2024-07-18",
			  "output": [{
				  "type": "message",
				  "id": "msg_fc_002",
				  "status": "completed",
				  "role": "assistant",
				  "content": [{
					  "type": "output_text",
					  "text": "It is cloudy in Amsterdam with a high of 15°C.",
					  "annotations": []
				  }]
			  }]
			}
			`

	server1 := newTestResponsesServer(t, input1, output1)
	defer server1.Close()

	a1 := newTestResponsesClient(server1, "gpt-4o-mini")

	type GetWeatherInput struct {
		Location string `json:"location" jsonschema:"The city and country"`
	}
	getWeather := func(ctx context.Context, input GetWeatherInput) (string, error) {
		return "Cloudy, 15°C", nil
	}
	weatherTool := functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Get the current weather",
	}, getWeather)

	resp1, err := a1.RunText(t.Context(), "What's the weather in Amsterdam?",
		agent.WithTool(weatherTool),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp1.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp1.Messages))
	}

	var funcCall *message.FunctionCallContent
	for _, content := range resp1.Messages[0].Contents {
		if fc, ok := content.(*message.FunctionCallContent); ok {
			funcCall = fc
			break
		}
	}
	if funcCall == nil {
		t.Fatal("expected function call content")
	}
	if funcCall.CallID != "call_weather_001" {
		t.Fatalf("expected CallID to use call_id (call_weather_001), got %q", funcCall.CallID)
	}

	server2 := newTestResponsesServer(t, input2, output2)
	defer server2.Close()

	a2 := newTestResponsesClient(server2, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "What's the weather in Amsterdam?"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{
				CallID:    funcCall.CallID,
				Name:      funcCall.Name,
				Arguments: funcCall.Arguments,
			},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: funcCall.CallID,
				Result: "Cloudy, 15°C",
			},
		}},
	}

	resp2, err := a2.Run(t.Context(), messages,
		agent.WithTool(weatherTool),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp2.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp2.Messages))
	}
}

func TestResponsesToolCallResult_SingleTextContent_SerializesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_123",
                        "output":"Result text"
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_001",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Done","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_123",
				Result: "Result text",
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Done" {
		t.Errorf("expected response text 'Done', got %q", responseText)
	}
}

func TestResponsesToolCallResult_MultipleTextContents_SerializesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_456",
                        "output":"First part. Second part."
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_002",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_002",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Processed","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	// Note: Currently, the Go implementation serializes all content as a single concatenated text
	// This differs from the C# test which uses a list of AIContent with separate items
	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_456",
				Result: "First part. Second part.",
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Processed" {
		t.Errorf("expected response text 'Processed', got %q", responseText)
	}
}

func TestResponsesNonStreamingResponseWithIncompleteReason_MapsFinishReason(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"incomplete",
              "model":"gpt-4o-mini",
              "output":[{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Partial","annotations":[]}]}],
              "incomplete_details":{"reason":"max_output_tokens"}
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "test").Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Partial" {
		t.Errorf("expected response text 'Partial', got %q", responseText)
	}
	if resp.FinishReason != "max_output_tokens" {
		t.Errorf("expected FinishReason max_output_tokens, got %q", resp.FinishReason)
	}
}

func TestResponsesResponseFormatSchemaConvertsJSONSchema(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	format, err := jsonformat.For[payload]()
	if err != nil {
		t.Fatal(err)
	}

	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
                "text":{"format":{"type":"json_schema","name":"payload","schema":{"properties":{"name":{"type":"string"}},"type":"object","required":["name"],"additionalProperties":false},"strict":true}}
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"output_text","text":"{\"name\":\"Ada\"}","annotations":[]}]}]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	if _, err := a.RunText(t.Context(), "hello", agent.WithResponseFormat(format)).Collect(); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestResponsesStreamingResponseWithQueuedUpdate_HandlesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: response.queued
data: {"type":"response.queued","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"queued","model":"gpt-4o-mini","output":[]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"completed","model":"gpt-4o-mini","output":[{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Done","annotations":[]}]}]}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) < 3 {
		t.Errorf("expected at least 3 updates, got %d", len(updates))
	}

	// Verify all updates have the same response ID
	for i, update := range updates {
		if update.ResponseID != "resp_001" {
			t.Errorf("update %d: expected ResponseID resp_001, got %s", i, update.ResponseID)
		}
	}
}

func TestResponsesStreamingResponseWithFailedUpdate_HandlesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: response.failed
data: {"type":"response.failed","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"failed","model":"gpt-4o-mini","output":[],"error":{"code":"internal_error","message":"Internal error"}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) < 2 {
		t.Errorf("expected at least 2 updates, got %d", len(updates))
	}

	// Verify all updates have the same response ID
	for i, update := range updates {
		if update.ResponseID != "resp_001" {
			t.Errorf("update %d: expected ResponseID resp_001, got %s", i, update.ResponseID)
		}
	}
}

func TestResponsesStreamingResponseWithInProgressUpdate_HandlesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: response.in_progress
data: {"type":"response.in_progress","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"completed","model":"gpt-4o-mini","output":[{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Done","annotations":[]}]}]}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) < 3 {
		t.Errorf("expected at least 3 updates, got %d", len(updates))
	}

	// Verify all updates have the same response ID
	for i, update := range updates {
		if update.ResponseID != "resp_001" {
			t.Errorf("update %d: expected ResponseID resp_001, got %s", i, update.ResponseID)
		}
	}
}

func TestResponsesStreamingResponseWithRefusalUpdate_HandlesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"harmful request"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"message","id":"msg_001","role":"assistant","status":"in_progress","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","content_part":{"type":"refusal","refusal":"I cannot provide that information"},"item_id":"msg_001"}

event: response.refusal.done
data: {"type":"response.refusal.done","refusal":"I cannot provide that information","item_id":"msg_001"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"completed","model":"gpt-4o-mini","output":[{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"I cannot provide that information"}]}]}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	var errorMessages []string
	for update, err := range a.RunText(t.Context(), "harmful request", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
		// Collect error content
		for _, content := range update.Contents {
			if ec, ok := content.(*message.ErrorContent); ok {
				errorMessages = append(errorMessages, ec.Message)
			}
		}
	}

	if len(updates) < 2 {
		t.Errorf("expected at least 2 updates, got %d", len(updates))
	}

	// Verify we got a refusal message
	found := false
	for _, msg := range errorMessages {
		if strings.Contains(msg, "I cannot provide that information") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected refusal message containing 'I cannot provide that information', got messages: %v", errorMessages)
	}
}

func TestResponsesCodeInterpreterTool_NonStreaming(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{
                    "type":"message",
                    "role":"user",
                    "content":[{"type":"input_text","text":"Calculate the sum of numbers from 1 to 5"}]
                }],
                "tools":[{
                    "type":"code_interpreter",
                    "container":{"type":"auto"}
                }]
            }
            `

	const output = `
            {
              "id":"resp_0e599e83cc6642210068fb7475165481a08efc750483c7048f",
              "object":"response",
              "created_at":1761309813,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "id":"ci_0e599e83cc6642210068fb7477fb9881a0811e8b0dc054b2fa",
                  "type":"code_interpreter_call",
                  "status":"completed",
                  "code":"# Calculating the sum of numbers from 1 to 5\nresult = sum(range(1, 6))\nresult",
                  "container_id":"cntr_68fb7476c384819186524b78cdc3180000a9a0fdd06b3cd4",
                  "outputs":null
                },
                {
                  "id":"msg_0e599e83cc6642210068fb747e118081a08c3ed46daa9d9dcb",
                  "type":"message",
                  "status":"completed",
                  "content":[{
                    "type":"output_text",
                    "annotations":[],
                    "text":"15"
                  }],
                  "role":"assistant"
                }
              ],
              "usage":{
                "input_tokens":225,
                "output_tokens":34,
                "total_tokens":259
              }
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "Calculate the sum of numbers from 1 to 5",
		agent.WithTool(&hostedtool.CodeInterpreter{}),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Expected: 1 message with 3 contents (like C#)
	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	msg := resp.Messages[0]
	if msg.Role != message.RoleAssistant {
		t.Errorf("expected role assistant, got %s", msg.Role)
	}
	if len(msg.Contents) != 4 {
		t.Fatalf("expected 4 contents (CodeInterpreterToolCallContent, CodeInterpreterToolResultContent, TextContent, UsageContent), got %d", len(msg.Contents))
	}

	// Check for CodeInterpreterToolCallContent
	codeCall, ok := msg.Contents[0].(*message.CodeInterpreterToolCallContent)
	if !ok {
		t.Fatalf("expected first content to be CodeInterpreterToolCallContent, got %T", msg.Contents[0])
	}
	if codeCall.CallID == "" {
		t.Error("expected CallID to be set")
	}
	if len(codeCall.Inputs) != 1 {
		t.Errorf("expected 1 input in code call, got %d", len(codeCall.Inputs))
	}
	if dataContent, ok := codeCall.Inputs[0].(*message.DataContent); ok {
		if dataContent.MediaType != "text/x-python" {
			t.Errorf("expected MediaType text/x-python, got %s", dataContent.MediaType)
		}
	}

	// Check for CodeInterpreterToolResultContent
	codeResult, ok := msg.Contents[1].(*message.CodeInterpreterToolResultContent)
	if !ok {
		t.Fatalf("expected second content to be CodeInterpreterToolResultContent, got %T", msg.Contents[1])
	}
	if codeResult.CallID != codeCall.CallID {
		t.Errorf("expected result CallID to match call CallID, got %s vs %s", codeResult.CallID, codeCall.CallID)
	}

	// Check for TextContent
	textContent, ok := msg.Contents[2].(*message.TextContent)
	if !ok {
		t.Fatalf("expected third content to be TextContent, got %T", msg.Contents[2])
	}
	if textContent.Text != "15" {
		t.Errorf("expected text '15', got %q", textContent.Text)
	}

	// Check for UsageContent
	usageContent, ok := msg.Contents[3].(*message.UsageContent)
	if !ok {
		t.Fatalf("expected fourth content to be UsageContent, got %T", msg.Contents[3])
	}
	if usageContent.Details.InputTokenCount != 225 {
		t.Errorf("expected input tokens 225, got %d", usageContent.Details.InputTokenCount)
	}
	if usageContent.Details.OutputTokenCount != 34 {
		t.Errorf("expected output tokens 34, got %d", usageContent.Details.OutputTokenCount)
	}
}

func TestResponsesCodeInterpreterTool_Streaming(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Calculate 3+3"}]}],
                "tools":[{"type":"code_interpreter","container":{"type":"auto"}}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_002","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"code_interpreter_call","id":"call_code_002","code":"","container_id":"container_002","status":"in_progress","outputs":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"code_interpreter_call","id":"call_code_002","code":"print(3+3)","container_id":"container_002","status":"completed","outputs":[{"type":"logs","logs":"6\n"}]}}

event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"message","id":"msg_002","role":"assistant","status":"in_progress","content":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_002","content_index":0,"delta":"6"}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"message","id":"msg_002","status":"completed","role":"assistant","content":[{"type":"output_text","text":"6","annotations":[]}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_002","object":"response","created_at":1741892091,"status":"completed","model":"gpt-4o-mini","output":[{"type":"code_interpreter_call","id":"call_code_002","code":"print(3+3)","container_id":"container_002","status":"completed","outputs":[{"type":"logs","logs":"6\n"}]},{"type":"message","id":"msg_002","status":"completed","role":"assistant","content":[{"type":"output_text","text":"6","annotations":[]}]}]}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	var allText strings.Builder
	for update, err := range a.RunText(t.Context(), "Calculate 3+3", agent.Stream(true),
		agent.WithTool(&hostedtool.CodeInterpreter{}),
	) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
		for _, content := range update.Contents {
			if tc, ok := content.(*message.TextContent); ok {
				allText.WriteString(tc.Text)
			}
		}
	}

	if len(updates) < 3 {
		t.Errorf("expected at least 3 updates, got %d", len(updates))
	}

	// Verify we got both code interpreter content and text result
	responseText := allText.String()
	if !strings.Contains(responseText, "Code Interpreter") {
		t.Errorf("expected response to contain 'Code Interpreter', got %q", responseText)
	}
	if !strings.Contains(responseText, "print(3+3)") {
		t.Errorf("expected response to contain code 'print(3+3)', got %q", responseText)
	}
	if !strings.Contains(responseText, "6") {
		t.Errorf("expected response to contain output '6', got %q", responseText)
	}
}

func TestResponsesStreamingResponseWithIncompleteUpdate_HandlesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: response.incomplete
data: {"type":"response.incomplete","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"incomplete","model":"gpt-4o-mini","output":[],"incomplete_details":{"reason":"max_output_tokens"}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	if len(updates) < 2 {
		t.Errorf("expected at least 2 updates, got %d", len(updates))
	}

	// Verify all updates have the same response ID
	for i, update := range updates {
		if update.ResponseID != "resp_001" {
			t.Errorf("update %d: expected ResponseID resp_001, got %s", i, update.ResponseID)
		}
	}
}

func TestResponsesResponseWithUsageDetails_ParsesTokenCounts(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_001",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Done","annotations":[]}]
                }
              ],
              "usage":{
                "input_tokens":50,
                "output_tokens":25,
                "total_tokens":75,
                "input_tokens_details":{"cached_tokens":10},
                "output_tokens_details":{"reasoning_tokens":5}
              }
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "test").Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Find usage content
	usage := resp.Usage()
	if usage.InputTokenCount != 50 {
		t.Errorf("expected input tokens 50, got %d", usage.InputTokenCount)
	}
	if usage.OutputTokenCount != 25 {
		t.Errorf("expected output tokens 25, got %d", usage.OutputTokenCount)
	}
	if usage.TotalTokenCount != 75 {
		t.Errorf("expected total tokens 75, got %d", usage.TotalTokenCount)
	}
	if usage.CachedInputTokenCount != 10 {
		t.Errorf("expected cached_input_tokens 10, got %v", usage.CachedInputTokenCount)
	}
	if usage.ReasoningTokenCount != 5 {
		t.Errorf("expected reasoning_tokens 5, got %v", usage.ReasoningTokenCount)
	}
}

func TestResponsesStreamingErrorUpdate_DocumentedFormat_ParsesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: error
data: {"type":"error","sequence_number":1,"message":"Rate limit exceeded","code":"rate_limit_exceeded","param":"requests"}

event: response.failed
data: {"type":"response.failed","sequence_number":2,"response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"failed","model":"gpt-4o-mini","output":[],"error":{"code":"rate_limit_exceeded","message":"Rate limit exceeded"}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Find error update
	var errorUpdate *agent.ResponseUpdate
	for _, update := range updates {
		for _, content := range update.Contents {
			if _, ok := content.(*message.ErrorContent); ok {
				errorUpdate = update
				break
			}
		}
		if errorUpdate != nil {
			break
		}
	}

	if errorUpdate == nil {
		t.Fatal("expected to find an update with ErrorContent")
	}

	// Extract and verify error content
	var errorContent *message.ErrorContent
	for _, content := range errorUpdate.Contents {
		if ec, ok := content.(*message.ErrorContent); ok {
			errorContent = ec
			break
		}
	}

	if errorContent.Message != "Rate limit exceeded" {
		t.Errorf("expected error message 'Rate limit exceeded', got %q", errorContent.Message)
	}
	if errorContent.ErrorCode != "rate_limit_exceeded" {
		t.Errorf("expected error code 'rate_limit_exceeded', got %q", errorContent.ErrorCode)
	}
	if errorContent.Details != "requests" {
		t.Errorf("expected error details 'requests', got %q", errorContent.Details)
	}
}

func TestResponsesUserMessageWithEmptyText_CreatesEmptyInputPart(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":""}]
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_001",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Done","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: ""}}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Logf("Messages received: %d", len(resp.Messages))
		for i, msg := range resp.Messages {
			t.Logf("  Message %d: ID=%s, Role=%s, Contents=%d", i, msg.ID, msg.Role, len(msg.Contents))
			for j, c := range msg.Contents {
				t.Logf("    Content %d: %T", j, c)
			}
		}
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Done" {
		t.Errorf("expected response text 'Done', got %q", responseText)
	}
}

func TestResponsesResponseWithRefusalContent_ParsesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"harmful request"}]}]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_001",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"refusal","refusal":"I cannot provide that information"}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "harmful request").Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	// Find error content (refusal is mapped to ErrorContent)
	var errorContent *message.ErrorContent
	for _, content := range resp.Messages[0].Contents {
		if ec, ok := content.(*message.ErrorContent); ok {
			errorContent = ec
			break
		}
	}

	if errorContent == nil {
		t.Fatal("expected ErrorContent in message")
	}

	if errorContent.Message != "I cannot provide that information" {
		t.Errorf("expected error message 'I cannot provide that information', got %q", errorContent.Message)
	}
	if errorContent.ErrorCode != "Refusal" {
		t.Errorf("expected error code 'Refusal', got %q", errorContent.ErrorCode)
	}
}

func TestResponsesStreamingResponseWithAnnotations_HandlesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","response_id":"resp_001","output_index":0,"item":{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Annotated text","annotations":[{"type":"file_citation","file_id":"file_123","start_index":0,"end_index":14}]}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_001","object":"response","created_at":1741892091,"status":"completed","model":"gpt-4o-mini","output":[{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Annotated text","annotations":[{"type":"file_citation","file_id":"file_123","start_index":0,"end_index":14}]}]}]}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Find an update with annotations
	var annotatedUpdate *agent.ResponseUpdate
	for _, update := range updates {
		for _, content := range update.Contents {
			if tc, ok := content.(*message.TextContent); ok {
				if len(tc.Annotations) > 0 {
					annotatedUpdate = update
					break
				}
			}
		}
		if annotatedUpdate != nil {
			break
		}
	}

	if annotatedUpdate == nil {
		t.Fatal("expected to find an update with annotations")
	}

	// Verify first content has annotations
	if len(annotatedUpdate.Contents) == 0 {
		t.Fatal("expected annotated update to have contents")
	}

	var firstAnnotations []message.Annotation
	if tc, ok := annotatedUpdate.Contents[0].(*message.TextContent); ok {
		firstAnnotations = tc.Annotations
	}

	if len(firstAnnotations) == 0 {
		t.Error("expected first content to have annotations")
	}
}

func TestResponsesResponseWithInputImageHttpUrl_ParsesAsUriContent(t *testing.T) {
	t.Skip("Skipping: input_image in output messages not yet supported by SDK")
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"What is in this image?"}]}]
            }
            `

	// The output includes a message with input_image content that has an image_url property with HTTP URL.
	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_001",
                  "status":"completed",
                  "role":"user",
                  "content":[
                    {"type":"input_image","image_url":"https://example.com/image.png"}
                  ]
                },
                {
                  "type":"message",
                  "id":"msg_002",
                  "status":"completed",
                  "role":"assistant",
                  "content":[
                    {"type":"output_text","text":"This is a cat.","annotations":[]}
                  ]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "What is in this image?").Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}

	// First message should be user with URI content
	userMsg := resp.Messages[0]
	if userMsg.Role != message.RoleUser {
		t.Errorf("expected first message to be user, got %s", userMsg.Role)
	}

	// HTTP URL should be returned as URIContent
	var imageContent *message.URIContent
	for _, content := range userMsg.Contents {
		if uc, ok := content.(*message.URIContent); ok {
			imageContent = uc
			break
		}
	}
	if imageContent == nil {
		t.Fatal("expected URIContent in user message")
	}
	if imageContent.URI != "https://example.com/image.png" {
		t.Errorf("expected URI https://example.com/image.png, got %s", imageContent.URI)
	}
	if imageContent.MediaType != "image/*" {
		t.Errorf("expected MediaType image/*, got %s", imageContent.MediaType)
	}

	// Second message should be assistant with text
	assistantMsg := resp.Messages[1]
	if assistantMsg.Role != message.RoleAssistant {
		t.Errorf("expected second message to be assistant, got %s", assistantMsg.Role)
	}

	var responseText string
	for _, content := range assistantMsg.Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "This is a cat." {
		t.Errorf("expected response text 'This is a cat.', got %q", responseText)
	}
}

func TestResponsesResponseWithInputImageDataUri_ParsesAsDataContent(t *testing.T) {
	t.Skip("Skipping: input_image in output messages not yet supported by SDK")
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"What is in this image?"}]}]
            }
            `

	// The output includes a message with input_image content that has an image_url property with a data URI.
	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_001",
                  "status":"completed",
                  "role":"user",
                  "content":[
                    {"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}
                  ]
                },
                {
                  "type":"message",
                  "id":"msg_002",
                  "status":"completed",
                  "role":"assistant",
                  "content":[
                    {"type":"output_text","text":"This is a red pixel.","annotations":[]}
                  ]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "What is in this image?").Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}

	// First message should be user with Data content
	userMsg := resp.Messages[0]
	if userMsg.Role != message.RoleUser {
		t.Errorf("expected first message to be user, got %s", userMsg.Role)
	}

	// Data URI should be returned as DataContent
	var imageContent *message.DataContent
	for _, content := range userMsg.Contents {
		if dc, ok := content.(*message.DataContent); ok {
			imageContent = dc
			break
		}
	}
	if imageContent == nil {
		t.Fatal("expected DataContent in user message")
	}
	if imageContent.MediaType != "image/png" {
		t.Errorf("expected MediaType image/png, got %s", imageContent.MediaType)
	}
	if len(imageContent.Data) == 0 {
		t.Error("expected Data to have content")
	}

	// Second message should be assistant with text
	assistantMsg := resp.Messages[1]
	if assistantMsg.Role != message.RoleAssistant {
		t.Errorf("expected second message to be assistant, got %s", assistantMsg.Role)
	}

	var responseText string
	for _, content := range assistantMsg.Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "This is a red pixel." {
		t.Errorf("expected response text 'This is a red pixel.', got %q", responseText)
	}
}

func TestResponsesResponseWithEndUserId_IncludesInAdditionalProperties(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Done","annotations":[]}]}],
              "user":"user_123"
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "test").Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Verify response has EndUserId in AdditionalProperties
	if resp.AdditionalProperties == nil {
		t.Fatal("expected AdditionalProperties to be set")
	}
	if endUserID, ok := resp.AdditionalProperties["EndUserId"]; !ok {
		t.Error("expected EndUserId in AdditionalProperties")
	} else if endUserID != "user_123" {
		t.Errorf("expected EndUserId 'user_123', got %v", endUserID)
	}
}

func TestResponsesResponseWithError_IncludesInAdditionalPropertiesAndMessage(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"failed",
              "model":"gpt-4o-mini",
              "output":[{"type":"message","id":"msg_001","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Processing","annotations":[]}]}],
              "error":{"code":"rate_limit_exceeded","message":"Rate limit exceeded"}
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	resp, err := a.RunText(t.Context(), "test").Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Verify response has Error in AdditionalProperties
	if resp.AdditionalProperties == nil {
		t.Fatal("expected AdditionalProperties to be set")
	}
	if _, ok := resp.AdditionalProperties["Error"]; !ok {
		t.Error("expected Error in AdditionalProperties")
	}

	// Verify last message contains error content
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	lastMessage := resp.Messages[len(resp.Messages)-1]
	var errorContent *message.ErrorContent
	for _, content := range lastMessage.Contents {
		if ec, ok := content.(*message.ErrorContent); ok {
			errorContent = ec
			break
		}
	}
	if errorContent == nil {
		t.Fatal("expected ErrorContent in last message")
	}
	if errorContent.Message != "Rate limit exceeded" {
		t.Errorf("expected error message 'Rate limit exceeded', got %q", errorContent.Message)
	}
	if errorContent.ErrorCode != "rate_limit_exceeded" {
		t.Errorf("expected error code 'rate_limit_exceeded', got %q", errorContent.ErrorCode)
	}
}

func TestResponsesStreamingErrorUpdate_ActualErroneousFormat_ParsesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_002","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: error
data: {"type":"error","sequence_number":1,"error":{"message":"Content filter triggered","code":"content_filter","param":"safety"}}

event: response.failed
data: {"type":"response.failed","sequence_number":2,"response":{"id":"resp_002","object":"response","created_at":1741892091,"status":"failed","model":"gpt-4o-mini","output":[],"error":{"code":"content_filter","message":"Content filter triggered"}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	var streamErr error
	for update, err := range a.RunText(t.Context(), "test", agent.Stream(true)) {
		if err != nil {
			// When there's an error event in the stream, the SDK returns it as a Go error
			streamErr = err
			break
		}
		updates = append(updates, update)
	}

	// The SDK returns error events as Go errors, not as ErrorContent in updates
	// Verify we got an error
	if streamErr == nil {
		t.Fatal("expected error from stream")
	}

	// Verify the error message contains the expected information
	errMsg := streamErr.Error()
	if !strings.Contains(errMsg, "Content filter triggered") {
		t.Errorf("expected error message to contain 'Content filter triggered', got %q", errMsg)
	}
	if !strings.Contains(errMsg, "content_filter") {
		t.Errorf("expected error message to contain 'content_filter', got %q", errMsg)
	}
}

func TestResponsesStreamingErrorUpdate_NoErrorInformation_HandlesGracefully(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}],
                "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_003","object":"response","created_at":1741892091,"status":"in_progress","model":"gpt-4o-mini","output":[]}}

event: error
data: {"type":"error","sequence_number":1}

event: response.failed
data: {"type":"response.failed","sequence_number":2,"response":{"id":"resp_003","object":"response","created_at":1741892091,"status":"failed","model":"gpt-4o-mini","output":[]}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "test", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Find error update with empty error information
	var errorUpdate *agent.ResponseUpdate
	for _, update := range updates {
		for _, content := range update.Contents {
			if _, ok := content.(*message.ErrorContent); ok {
				errorUpdate = update
				break
			}
		}
		if errorUpdate != nil {
			break
		}
	}

	if errorUpdate == nil {
		t.Fatal("expected to find an update with ErrorContent")
	}

	// Verify error content has empty fields
	var errorContent *message.ErrorContent
	for _, content := range errorUpdate.Contents {
		if ec, ok := content.(*message.ErrorContent); ok {
			errorContent = ec
			break
		}
	}

	if errorContent == nil {
		t.Fatal("expected ErrorContent in error update")
	}

	// Verify all fields are empty (like C#)
	if errorContent.Message != "" {
		t.Errorf("expected empty Message, got %q", errorContent.Message)
	}
	if errorContent.ErrorCode != "" {
		t.Errorf("expected empty ErrorCode, got %q", errorContent.ErrorCode)
	}
	if errorContent.Details != "" {
		t.Errorf("expected empty Details, got %q", errorContent.Details)
	}
}

func TestResponsesUserMessageWithVariousContentTypes_ConvertsCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[
                            {"type":"input_text","text":"Here is some text"},
                            {"type":"input_image","image_url":"https://example.com/image.jpg","detail":"high"}
                        ]
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_001",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Processed","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	// Create a message with text and image
	messages := []*message.Message{
		{
			Role: message.RoleUser,
			Contents: []message.Content{
				&message.TextContent{Text: "Here is some text"},
				&message.URIContent{
					URI:       "https://example.com/image.jpg",
					MediaType: "image/jpeg",
					ContentHeader: message.ContentHeader{
						AdditionalProperties: map[string]any{"detail": "high"},
					},
				},
			},
		},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Processed" {
		t.Errorf("expected response text 'Processed', got %q", responseText)
	}
}

func TestResponsesToolCallResult_DataContent_SerializesAsInputImage(t *testing.T) {
	// A minimal 1x1 PNG image as base64
	const imageBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_123",
                        "output":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}]
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_001",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_001",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Image received","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_123",
				Result: []message.Content{
					&message.DataContent{
						Data:      imageBase64,
						MediaType: "image/png",
					},
				},
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Image received" {
		t.Errorf("expected response text 'Image received', got %q", responseText)
	}
}

func TestResponsesToolCallResult_UriContent_SerializesAsInputImage(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_456",
                        "output":[{"type":"input_image","image_url":"https://example.com/photo.jpg"}]
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_002",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_002",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Image URL received","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_456",
				Result: []message.Content{
					&message.URIContent{
						URI:       "https://example.com/photo.jpg",
						MediaType: "image/jpeg",
					},
				},
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Image URL received" {
		t.Errorf("expected response text 'Image URL received', got %q", responseText)
	}
}

func TestResponsesToolCallResult_MixedContent_SerializesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_789",
                        "output":[
                            {"type":"input_text","text":"Here is the result"},
                            {"type":"input_image","image_url":"https://example.com/chart.png"}
                        ]
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_003",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_003",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Mixed content received","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_789",
				Result: []message.Content{
					&message.TextContent{Text: "Here is the result"},
					&message.URIContent{
						URI:       "https://example.com/chart.png",
						MediaType: "image/png",
					},
				},
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Mixed content received" {
		t.Errorf("expected response text 'Mixed content received', got %q", responseText)
	}
}

func TestResponsesToolCallResult_HostedFileContent_SerializesCorrectly(t *testing.T) {
	const input = `{
		"model":"gpt-4o-mini",
		"input":[
			{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"test"}]
			},
			{
				"type":"function_call_output",
				"call_id":"call_file",
				"output":[
					{"type":"input_image","file_id":"file-abc123"}
				]
			}
		]
	}`

	const output = `{
		"id":"resp_005",
		"object":"response",
		"created_at":1741892091,
		"status":"completed",
		"model":"gpt-4o-mini",
		"output":[
			{
				"type":"message",
				"id":"msg_005",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"File processed","annotations":[]}]
			}
		]
	}`

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{
			Role: message.RoleUser,
			Contents: []message.Content{
				&message.TextContent{Text: "test"},
			},
		},
		{
			Role: message.RoleTool,
			Contents: []message.Content{
				&message.FunctionResultContent{
					CallID: "call_file",
					Result: &message.HostedFileContent{
						FileID:    "file-abc123",
						Name:      "result.png",
						MediaType: "image/png",
					},
				},
			},
		},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "File processed" {
		t.Errorf("expected response text 'File processed', got %q", responseText)
	}
}

func TestResponsesToolCallResult_DataContentPDF_SerializesAsInputFile(t *testing.T) {
	const input = `{
		"model":"gpt-4o-mini",
		"input":[
			{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"test"}]
			},
			{
				"type":"function_call_output",
				"call_id":"call_pdf",
				"output":[
					{"type":"input_file","file_data":"data:application/pdf;base64,cGRmZGF0YQ==","filename":"report.pdf"}
				]
			}
		]
	}`

	const output = `{
		"id":"resp_007",
		"object":"response",
		"created_at":1741892091,
		"status":"completed",
		"model":"gpt-4o-mini",
		"output":[
			{
				"type":"message",
				"id":"msg_007",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"PDF processed","annotations":[]}]
			}
		]
	}`

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{
			Role: message.RoleUser,
			Contents: []message.Content{
				&message.TextContent{Text: "test"},
			},
		},
		{
			Role: message.RoleTool,
			Contents: []message.Content{
				&message.FunctionResultContent{
					CallID: "call_pdf",
					Result: &message.DataContent{
						Data:      "cGRmZGF0YQ==",
						MediaType: "application/pdf",
						Name:      "report.pdf",
					},
				},
			},
		},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "PDF processed" {
		t.Errorf("expected response text 'PDF processed', got %q", responseText)
	}
}

func TestResponsesToolCallResult_UriContentNonImage_SerializesAsInputFile(t *testing.T) {
	const input = `{
		"model":"gpt-4o-mini",
		"input":[
			{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"test"}]
			},
			{
				"type":"function_call_output",
				"call_id":"call_file_uri",
				"output":[
					{"type":"input_file","file_url":"https://example.com/document.pdf"}
				]
			}
		]
	}`

	const output = `{
		"id":"resp_009",
		"object":"response",
		"created_at":1741892091,
		"status":"completed",
		"model":"gpt-4o-mini",
		"output":[
			{
				"type":"message",
				"id":"msg_009",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"File URI processed","annotations":[]}]
			}
		]
	}`

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{
			Role: message.RoleUser,
			Contents: []message.Content{
				&message.TextContent{Text: "test"},
			},
		},
		{
			Role: message.RoleTool,
			Contents: []message.Content{
				&message.FunctionResultContent{
					CallID: "call_file_uri",
					Result: &message.URIContent{
						URI:       "https://example.com/document.pdf",
						MediaType: "application/pdf",
					},
				},
			},
		},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "File URI processed" {
		t.Errorf("expected response text 'File URI processed', got %q", responseText)
	}
}

func TestResponsesToolCallResult_HostedFileContentNonImage_SerializesAsInputFile(t *testing.T) {
	const input = `{
		"model":"gpt-4o-mini",
		"input":[
			{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"test"}]
			},
			{
				"type":"function_call_output",
				"call_id":"call_hosted_file",
				"output":[
					{"type":"input_file","file_id":"file-xyz789","filename":"document.txt"}
				]
			}
		]
	}`

	const output = `{
		"id":"resp_010",
		"object":"response",
		"created_at":1741892091,
		"status":"completed",
		"model":"gpt-4o-mini",
		"output":[
			{
				"type":"message",
				"id":"msg_010",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"Hosted file processed","annotations":[]}]
			}
		]
	}`

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{
			Role: message.RoleUser,
			Contents: []message.Content{
				&message.TextContent{Text: "test"},
			},
		},
		{
			Role: message.RoleTool,
			Contents: []message.Content{
				&message.FunctionResultContent{
					CallID: "call_hosted_file",
					Result: &message.HostedFileContent{
						FileID:    "file-xyz789",
						Name:      "document.txt",
						MediaType: "text/plain",
					},
				},
			},
		},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Hosted file processed" {
		t.Errorf("expected response text 'Hosted file processed', got %q", responseText)
	}
}

func TestResponsesToolCallResult_ObjectSerialization_SerializesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_obj",
                        "output":"{\"name\":\"John\",\"age\":30}"
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_obj",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_obj",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Object processed","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_obj",
				Result: json.RawMessage(`{"name":"John","age":30}`),
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Object processed" {
		t.Errorf("expected response text 'Object processed', got %q", responseText)
	}
}

func TestResponsesToolCallResult_StringFallback_SerializesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_string",
                        "output":"Simple string result"
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_008",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_008",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"String processed","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_string",
				Result: "Simple string result",
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "String processed" {
		t.Errorf("expected response text 'String processed', got %q", responseText)
	}
}

func TestResponsesToolCallResult_TextContentObject_SerializesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_content",
                        "output":[{"type":"input_text","text":"Content object result"}]
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_content",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_content",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Content processed","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_content",
				Result: &message.TextContent{Text: "Content object result"},
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	var responseText string
	for _, content := range resp.Messages[0].Contents {
		if tc, ok := content.(*message.TextContent); ok {
			responseText = tc.Text
		}
	}
	if responseText != "Content processed" {
		t.Errorf("expected response text 'Content processed', got %q", responseText)
	}
}

func TestResponsesToolCallResult_MultipleContentObjects_SerializesCorrectly(t *testing.T) {
	const input = `
            {
                "model":"gpt-4o-mini",
                "input":[
                    {
                        "type":"message",
                        "role":"user",
                        "content":[{"type":"input_text","text":"test"}]
                    },
                    {
                        "type":"function_call_output",
                        "call_id":"call_multi",
                        "output":[
                            {"type":"input_text","text":"First part"},
                            {"type":"input_text","text":"Second part"}
                        ]
                    }
                ]
            }
            `

	const output = `
            {
              "id":"resp_multi_content",
              "object":"response",
              "created_at":1741892091,
              "status":"completed",
              "model":"gpt-4o-mini",
              "output":[
                {
                  "type":"message",
                  "id":"msg_multi_content",
                  "status":"completed",
                  "role":"assistant",
                  "content":[{"type":"output_text","text":"Multiple contents processed","annotations":[]}]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "test"}}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_multi",
				Result: []message.Content{
					&message.TextContent{Text: "First part"},
					&message.TextContent{Text: "Second part"},
				},
			},
		}},
	}

	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}

	responseText := resp.String()
	if responseText != "Multiple contents processed" {
		t.Errorf("expected response text 'Multiple contents processed', got %q", responseText)
	}
}

// newTestResponsesServerGET creates a test server for GET requests (polling)
func newTestResponsesServerGET(t *testing.T, responseID string, output string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For polling, expect GET request to /v1/responses/{response_id}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET method, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, responseID) {
			t.Errorf("expected path to contain %s, got %s", responseID, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, output); err != nil {
			t.Fatalf("failed writing response: %v", err)
		}
	}))
}

func TestResponsesConversationId_AsResponseId_NonStreaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o-mini",
                "previous_response_id":"resp_12345",
                "input":[{
                    "type":"message",
                    "role":"user",
                    "content":[{"type":"input_text","text":"hello"}]
                }],
                "max_output_tokens":20
            }
            `

	const output = `
            {
              "id": "resp_67890",
              "object": "response",
              "created_at": 1741891428,
              "status": "completed",
              "model": "gpt-4o-mini-2024-07-18",
              "output": [
                {
                  "type": "message",
                  "id": "msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d",
                  "status": "completed",
                  "role": "assistant",
                  "content": [
                    {
                      "type": "output_text",
                      "text": "Hello! How can I assist you today?",
                      "annotations": []
                    }
                  ]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("resp_12345"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "hello",
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
		agent.WithSession(session),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// After the call, session conversation id should be updated to the new response ID
	if got := session.ServiceID(); got != "resp_67890" {
		t.Errorf("expected ConversationId resp_67890, got %s", got)
	}
}

func TestResponsesConversationId_AsConversationId_NonStreaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o-mini",
                "conversation":"conv_12345",
                "input":[{
                    "type":"message",
                    "role":"user",
                    "content":[{"type":"input_text","text":"hello"}]
                }],
                "max_output_tokens":20
            }
            `

	const output = `
            {
              "id": "resp_67890",
              "object": "response",
              "created_at": 1741891428,
              "status": "completed",
              "model": "gpt-4o-mini-2024-07-18",
              "output": [
                {
                  "type": "message",
                  "id": "msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d",
                  "status": "completed",
                  "role": "assistant",
                  "content": [
                    {
                      "type": "output_text",
                      "text": "Hello! How can I assist you today?",
                      "annotations": []
                    }
                  ]
                }
              ]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("conv_12345"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "hello",
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
		agent.WithSession(session),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// When using a conversation ID, it should remain unchanged
	if got := session.ServiceID(); got != "conv_12345" {
		t.Errorf("expected ConversationId conv_12345, got %s", got)
	}
}

func TestResponsesConversationId_AsResponseId_Streaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o-mini",
                "previous_response_id":"resp_12345",
                "input":[{
                    "type":"message",
                    "role":"user",
                    "content":[{"type":"input_text","text":"hello"}]
                }],
                "stream":true,
                "max_output_tokens":20
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_67890","object":"response","created_at":1741892091,"status":"in_progress","max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[],"store":true,"temperature":0.5}}

event: response.in_progress
data: {"type":"response.in_progress","response":{"id":"resp_67890","object":"response","created_at":1741892091,"status":"in_progress","max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[],"store":true,"temperature":0.5}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"in_progress","role":"assistant","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":"Hello!"}

event: response.output_text.done
data: {"type":"response.output_text.done","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"text":"Hello!"}

event: response.content_part.done
data: {"type":"response.content_part.done","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello!","annotations":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_67890","object":"response","created_at":1741892091,"status":"completed","max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}],"store":true,"temperature":0.5,"usage":{"input_tokens":26,"output_tokens":10,"total_tokens":36}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("resp_12345"))
	if err != nil {
		t.Fatal(err)
	}

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "hello", agent.Stream(true),
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
		agent.WithSession(session),
	) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	for i, update := range updates {
		if update.ResponseID != "resp_67890" {
			t.Errorf("update %d: expected ResponseID resp_67890, got %s", i, update.ResponseID)
		}
	}

	if got := session.ServiceID(); got != "resp_67890" {
		t.Errorf("expected ConversationId resp_67890, got %s", got)
	}
}

func TestResponsesConversationId_AsConversationId_Streaming(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o-mini",
                "conversation":"conv_12345",
                "input":[{
                    "type":"message",
                    "role":"user",
                    "content":[{"type":"input_text","text":"hello"}]
                }],
                "stream":true,
                "max_output_tokens":20
            }
            `

	const output = `event: response.created
data: {"type":"response.created","response":{"id":"resp_67890","object":"response","created_at":1741892091,"status":"in_progress","max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[],"store":true,"temperature":0.5}}

event: response.in_progress
data: {"type":"response.in_progress","response":{"id":"resp_67890","object":"response","created_at":1741892091,"status":"in_progress","max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[],"store":true,"temperature":0.5}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"in_progress","role":"assistant","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"delta":"Hello!"}

event: response.output_text.done
data: {"type":"response.output_text.done","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"text":"Hello!"}

event: response.content_part.done
data: {"type":"response.content_part.done","item_id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello!","annotations":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_67890","object":"response","created_at":1741892091,"status":"completed","max_output_tokens":20,"model":"gpt-4o-mini-2024-07-18","output":[{"type":"message","id":"msg_67d329fc0c0081919696b8ab36713a41029dabe3ee19bb77","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}],"store":true,"temperature":0.5,"usage":{"input_tokens":26,"output_tokens":10,"total_tokens":36}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("conv_12345"))
	if err != nil {
		t.Fatal(err)
	}

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "hello", agent.Stream(true),
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
		agent.WithSession(session),
	) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	for i, update := range updates {
		if update.ResponseID != "resp_67890" {
			t.Errorf("update %d: expected ResponseID resp_67890, got %s", i, update.ResponseID)
		}
	}

	if got := session.ServiceID(); got != "conv_12345" {
		t.Errorf("expected ConversationId conv_12345, got %s", got)
	}
}

func TestResponsesBackgroundResponses_FirstCall(t *testing.T) {
	const input = `
            {
                "temperature":0.5,
                "model":"gpt-4o-mini",
                "background":true,
                "input":[{
                    "type":"message",
                    "role":"user",
                    "content":[{"type":"input_text","text":"hello"}]
                }],
                "max_output_tokens":20
            }
            `

	const output = `
            {
              "id":"resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed7",
              "object":"response",
              "created_at":1758712522,
              "status":"queued",
              "background":true,
              "model":"gpt-4o-mini-2024-07-18",
              "output":[]
            }
            `

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	resp, err := a.RunText(t.Context(), "hello",
		openaiagent.ResponsesNewParams(responses.ResponseNewParams{
			MaxOutputTokens: openai.Int(20),
			Temperature:     openai.Float(0.5),
		}),
		agent.AllowBackgroundResponses(true),
		agent.WithSession(session),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Errorf("expected 1 message (for continuation token), got %d", len(resp.Messages))
	}

	if resp.ContinuationToken == "" {
		t.Fatal("expected ContinuationToken to be set")
	}
	if got := session.ServiceID(); got != "resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed7" {
		t.Errorf("expected session ServiceID resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed7, got %s", got)
	}
}

func TestResponsesBackgroundResponses_PollingCall_Queued(t *testing.T) {
	testResponsesBackgroundPolling(t, "queued")
}

func TestResponsesBackgroundResponses_PollingCall_InProgress(t *testing.T) {
	testResponsesBackgroundPolling(t, "in_progress")
}

func TestResponsesBackgroundResponses_PollingCall_Completed(t *testing.T) {
	testResponsesBackgroundPolling(t, "completed")
}

func testResponsesBackgroundPolling(t *testing.T, status string) {
	outputContent := `[]`
	if status == "completed" {
		outputContent = `[{
			"type":"message",
			"id":"msg_67d32764fcdc8191bcf2e444d4088804058a5e08c46a181d",
			"status":"completed",
			"role":"assistant",
			"content":[
				{
					"type":"output_text",
					"text":"The background response result.",
					"annotations":[]
				}
			]
		}]`
	}

	output := `
            {
              "id":"resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed8",
              "object":"response",
              "created_at":1758712522,
              "status":"` + status + `",
              "background":true,
              "model":"gpt-4o-mini-2024-07-18",
              "output":` + outputContent + `
            }
            `

	server := newTestResponsesServerGET(t, "resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed8", output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	// Create session with ConversationID to simulate a previous call (polling scenario)
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed8"))
	if err != nil {
		t.Fatal(err)
	}

	// Create continuation token
	ct := continuationToken{
		ResponseID:     "resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed8",
		SequenceNumber: 0,
	}
	ctJSON, _ := json.Marshal(ct)

	resp, err := a.Run(t.Context(), nil,
		agent.WithContinuationToken(agenttest.NewContinuationToken(t, string(ctJSON))),
		agent.AllowBackgroundResponses(true),
		agent.WithSession(session),
	).Collect()
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	switch status {
	case "queued", "in_progress":
		if resp.ContinuationToken == "" {
			t.Error("expected ContinuationToken to be set for queued/in_progress status")
		}

		if len(resp.Messages) != 1 {
			t.Errorf("expected 1 message for %s status, got %d", status, len(resp.Messages))
		}

	case "completed":
		if resp.ContinuationToken != "" {
			t.Error("expected ContinuationToken to be empty for completed status")
		}

		responseText := resp.String()
		if responseText != "The background response result." {
			t.Errorf("expected response text 'The background response result.', got %q", responseText)
		}
		if len(resp.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(resp.Messages))
		}
	}
	if got := session.ServiceID(); got != "resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed8" {
		t.Errorf("expected session ServiceID resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed8, got %s", got)
	}
}

func TestResponsesBackgroundResponses_Streaming(t *testing.T) {
	const input = `
            {
              "model":"gpt-4o-2024-08-06",
              "background":true,
              "input":[{
                "type":"message",
                "role":"user",
                "content":[{
                  "type":"input_text",
                  "text":"hello"
                }]
              }],
              "stream":true
            }
            `

	const output = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_68d401a7b36c81a288600e95a5a119d4073420ed59d5f559","object":"response","created_at":1758724519,"status":"queued","background":true,"model":"gpt-4o-2024-08-06","output":[]}}

event: response.queued
data: {"type":"response.queued","sequence_number":1,"response":{"id":"resp_68d401a7b36c81a288600e95a5a119d4073420ed59d5f559","object":"response","created_at":1758724519,"status":"queued","background":true,"model":"gpt-4o-2024-08-06","output":[]}}

event: response.in_progress
data: {"type":"response.in_progress","sequence_number":2,"response":{"id":"resp_68d401a7b36c81a288600e95a5a119d4073420ed59d5f559","object":"response","created_at":1758724519,"status":"in_progress","background":true,"model":"gpt-4o-2024-08-06","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":3,"item":{"id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content":[],"role":"assistant","status":"in_progress","type":"message"},"output_index":0}

event: response.content_part.added
data: {"type":"response.content_part.added","sequence_number":4,"item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"part":{"text":"","type":"output_text","annotations":[]},"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":5,"delta":"Hello","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":6,"delta":"!","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":7,"delta":" How","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":8,"delta":" can","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":9,"delta":" I","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":10,"delta":" assist","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":11,"delta":" you","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":12,"delta":" today","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":13,"delta":"?","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.output_text.done
data: {"type":"response.output_text.done","sequence_number":14,"text":"Hello! How can I assist you today?","item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"output_index":0}

event: response.content_part.done
data: {"type":"response.content_part.done","sequence_number":15,"item_id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content_index":0,"part":{"text":"Hello! How can I assist you today?","type":"output_text","annotations":[]},"output_index":0}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":16,"item":{"id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content":[{"text":"Hello! How can I assist you today?","type":"output_text","annotations":[]}],"role":"assistant","status":"completed","type":"message"},"output_index":0}

event: response.completed
data: {"type":"response.completed","sequence_number":17,"response":{"id":"resp_68d401a7b36c81a288600e95a5a119d4073420ed59d5f559","object":"response","created_at":1758724519,"status":"completed","background":true,"model":"gpt-4o-2024-08-06","output":[{"id":"msg_68d401aa78d481a2ab30776a79c691a6073420ed59d5f559","content":[{"text":"Hello! How can I assist you today?","type":"output_text","annotations":[]}],"role":"assistant","status":"completed","type":"message"}],"usage":{"total_tokens":18,"input_tokens":8,"output_tokens":10}}}

`

	server := newTestResponsesServerStreaming(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-2024-08-06")
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var updates []*agent.ResponseUpdate
	var allText strings.Builder
	for update, err := range a.RunText(t.Context(), "hello", agent.Stream(true),
		agent.AllowBackgroundResponses(true),
		agent.WithSession(session),
	) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
		for _, content := range update.Contents {
			if tc, ok := content.(*message.TextContent); ok {
				allText.WriteString(tc.Text)
			}
		}
	}

	if allText.String() != "Hello! How can I assist you today?" {
		t.Errorf("expected text 'Hello! How can I assist you today?', got %q", allText.String())
	}

	if len(updates) != 18 {
		t.Errorf("expected 18 updates, got %d", len(updates))
	}
	if got := session.ServiceID(); got != "resp_68d401a7b36c81a288600e95a5a119d4073420ed59d5f559" {
		t.Errorf("expected session ServiceID resp_68d401a7b36c81a288600e95a5a119d4073420ed59d5f559, got %s", got)
	}

	// Verify continuation tokens
	for i := 0; i < len(updates); i++ {
		if updates[i].ResponseID != "resp_68d401a7b36c81a288600e95a5a119d4073420ed59d5f559" {
			t.Errorf("update %d: expected ResponseID resp_68d401a7b36c81a288600e95a5a119d4073420ed59d5f559, got %s", i, updates[i].ResponseID)
		}

		if i < len(updates)-1 {
			// All updates except the last should have continuation token
			if updates[i].ContinuationToken == "" {
				t.Errorf("update %d: expected ContinuationToken to be set", i)
			}
		} else {
			// Last update should not have continuation token
			if updates[i].ContinuationToken != "" {
				t.Errorf("update %d: expected ContinuationToken to be empty for last update", i)
			}
		}
	}
}

func TestResponsesBackgroundResponses_StreamResumption(t *testing.T) {
	const output = `event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":10,"delta":" assist","logprobs":[],"item_id":"msg_68d40dcb2d34819c88f5d6a8ca7b0308029e611c3cc4a34b","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":11,"delta":" you","logprobs":[],"item_id":"msg_68d40dcb2d34819c88f5d6a8ca7b0308029e611c3cc4a34b","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":12,"delta":" today","logprobs":[],"item_id":"msg_68d40dcb2d34819c88f5d6a8ca7b0308029e611c3cc4a34b","content_index":0,"output_index":0}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":13,"delta":"?","logprobs":[],"item_id":"msg_68d40dcb2d34819c88f5d6a8ca7b0308029e611c3cc4a34b","content_index":0,"output_index":0}

event: response.output_text.done
data: {"type":"response.output_text.done","sequence_number":14,"text":"Hello! How can I assist you today?","logprobs":[],"item_id":"msg_68d40dcb2d34819c88f5d6a8ca7b0308029e611c3cc4a34b","content_index":0,"output_index":0}

event: response.content_part.done
data: {"type":"response.content_part.done","sequence_number":15,"item_id":"msg_68d40dcb2d34819c88f5d6a8ca7b0308029e611c3cc4a34b","content_index":0,"part":{"text":"Hello! How can I assist you today?","logprobs":[],"type":"output_text","annotations":[]},"output_index":0}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":16,"item":{"id":"msg_68d40dcb2d34819c88f5d6a8ca7b0308029e611c3cc4a34b","content":[{"text":"Hello! How can I assist you today?","logprobs":[],"type":"output_text","annotations":[]}],"role":"assistant","status":"completed","type":"message"},"output_index":0}

event: response.completed
data: {"type":"response.completed","sequence_number":17,"response":{"truncation":"disabled","id":"resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b","tool_choice":"auto","temperature":1.0,"top_p":1.0,"status":"completed","top_logprobs":0,"usage":{"total_tokens":18,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0},"output_tokens":10,"input_tokens":8},"object":"response","created_at":1758727622,"prompt_cache_key":null,"text":{"format":{"type":"text"},"verbosity":"medium"},"incomplete_details":null,"model":"gpt-4o-2024-08-06","previous_response_id":null,"safety_identifier":null,"metadata":{},"store":true,"output":[{"id":"msg_68d40dcb2d34819c88f5d6a8ca7b0308029e611c3cc4a34b","content":[{"text":"Hello! How can I assist you today?","logprobs":[],"type":"output_text","annotations":[]}],"role":"assistant","status":"completed","type":"message"}],"parallel_tool_calls":true,"error":null,"background":true,"instructions":null,"service_tier":"default","max_tool_calls":null,"max_output_tokens":null,"tools":[],"user":null,"reasoning":{"effort":null,"summary":null}}}

`

	// Create server that expects GET request to resume stream
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET request, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b") {
			t.Errorf("expected response ID in path, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("starting_after") != "9" {
			t.Errorf("expected starting_after=9, got %s", r.URL.Query().Get("starting_after"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, output); err != nil {
			t.Fatalf("failed writing response: %v", err)
		}
	}))
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-2024-08-06")

	// Emulating resumption of the stream after receiving the first 9 updates that provided the text "Hello! How can I"
	token := agenttest.NewContinuationToken(t, `{"response_id":"resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b","sequence_number":9}`)

	// Create session with ConversationID to allow continuation
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b"))
	if err != nil {
		t.Fatal(err)
	}

	var updates []*agent.ResponseUpdate
	for update, err := range a.Run(t.Context(), []*message.Message{}, agent.Stream(true),
		agent.AllowBackgroundResponses(true),
		agent.WithContinuationToken(token),
		agent.WithSession(session),
	) {
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		updates = append(updates, update)
	}

	// Verify we received 8 updates (remaining updates to complete the response)
	if len(updates) != 8 {
		t.Errorf("expected 8 updates, got %d", len(updates))
	}

	// Concatenate text from updates to verify we got " assist you today?"
	var fullText strings.Builder
	for _, update := range updates {
		for _, content := range update.Contents {
			if tc, ok := content.(*message.TextContent); ok {
				fullText.WriteString(tc.Text)
			}
		}
	}

	if fullText.String() != " assist you today?" {
		t.Errorf("expected text ' assist you today?', got %q", fullText.String())
	}
	if got := session.ServiceID(); got != "resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b" {
		t.Errorf("expected session ServiceID resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b, got %s", got)
	}

	// Verify all updates have correct ResponseID
	for i, update := range updates {
		if update.ResponseID != "resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b" {
			t.Errorf("update %d: expected ResponseID resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b, got %s", i, update.ResponseID)
		}

		// Verify continuation tokens for all but last update
		if i < len(updates)-1 {
			if update.ContinuationToken == "" {
				t.Errorf("update %d: expected ContinuationToken to be set", i)
			}
		} else {
			// Last update should not have continuation token
			if update.ContinuationToken != "" {
				t.Errorf("update %d: expected ContinuationToken to be empty for last update", i)
			}
		}
	}
}

func TestResponsesGetContinuationToken_WithMessages_ThrowsException(t *testing.T) {
	server := newTestResponsesServer(t, "", "")
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	token := agenttest.NewContinuationToken(t, `{"response_id":"resp_123","sequence_number":0}`)

	// Attempt to use continuation token with messages should error
	_, err := a.RunText(t.Context(), "test",
		agent.WithContinuationToken(token),
	).Collect()

	if err == nil {
		t.Fatal("expected error when using continuation token with messages")
	}

	if err.Error() != "messages are not allowed when continuing a background response using a continuation token" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResponsesBackgroundResponses_PollingCall_WithMessages(t *testing.T) {
	server := newTestResponsesServer(t, "", "")
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	token := agenttest.NewContinuationToken(t, `{"response_id":"resp_68d3d2c9ef7c8195863e4e2b2ec226a205007262ecbbfed8","sequence_number":0}`)

	// A try to update a background response with new messages should fail
	_, err = a.RunText(t.Context(), "Please book hotel as well",
		agent.WithSession(session),
		agent.WithContinuationToken(token),
		agent.AllowBackgroundResponses(true),
	).Collect()

	if err == nil {
		t.Fatal("expected error when using continuation token with messages")
	}

	if err.Error() != "messages are not allowed when continuing a background response using a continuation token" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResponsesBackgroundResponses_StreamResumption_WithMessages(t *testing.T) {
	server := newTestResponsesServer(t, "", "")
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	token := agenttest.NewContinuationToken(t, `{"response_id":"resp_68d40dc671a0819cb0ee920078333451029e611c3cc4a34b","sequence_number":9}`)

	// Attempt to resume stream with messages should fail
	for _, err := range a.RunText(t.Context(), "Please book a hotel for me",
		agent.WithSession(session),
		agent.AllowBackgroundResponses(true),
		agent.WithContinuationToken(token),
		agent.Stream(true)) {
		if err == nil {
			t.Fatal("expected error when using continuation token with messages in streaming")
		}
		if err.Error() != "messages are not allowed when continuing a background response using a continuation token" {
			t.Errorf("unexpected error: %v", err)
		}
		return
	}
	t.Fatal("expected error but iteration completed without error")
}

func TestResponsesMultipleRequiredFunctions(t *testing.T) {
	const input = `
            {
                "tools": [
                    {
                        "name": "GetWeather",
                        "description": "Get the current weather for a location",
                        "type": "function",
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
                    {
                        "name": "GetTime",
                        "description": "Get the current time for a location",
                        "type": "function",
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
                    }
                ],
                "tool_choice": {
                    "type": "allowed_tools",
                    "mode": "required",
                    "tools": [
                        {
                            "type": "function",
                            "name": "GetWeather"
                        },
                        {
                            "type": "function",
                            "name": "GetTime"
                        }
                    ]
                },
                "model": "gpt-4o-mini",
                "input": [{
                    "type": "message",
                    "role": "user",
                    "content": [{"type": "input_text", "text": "What's the weather and time in Seattle?"}]
                }]
            }
            `
	const output = `
            {
              "id": "resp_TestMultiRequired123",
              "object": "response",
              "created_at": 1727900000,
              "status": "completed",
              "model": "gpt-4o-mini-2024-07-18",
              "output": [
                {
                  "type": "function_call",
                  "id": "call_weather_123",
                  "name": "GetWeather",
                  "arguments": "{\"location\":\"Seattle, WA\"}"
                },
                {
                  "type": "function_call",
                  "id": "call_time_456",
                  "name": "GetTime",
                  "arguments": "{\"location\":\"Seattle, WA\"}"
                }
              ]
            }
            `

	want := []*message.Message{
		{
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

	server := newTestResponsesServer(t, input, output)
	defer server.Close()

	a := newTestResponsesClient(server, "gpt-4o-mini")

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

func responsesBodyEqual(t *testing.T, got string, want string) {
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
