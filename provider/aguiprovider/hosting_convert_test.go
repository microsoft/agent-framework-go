// Copyright (c) Microsoft. All rights reserved.

package aguiprovider

import (
	"testing"

	aguiTypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/microsoft/agent-framework-go/message"
)

func TestToAgentMessages_CoalescesConsecutiveAssistantToolCalls(t *testing.T) {
	got, err := toAgentMessages([]aguiTypes.Message{
		{
			ID:      "assistant-call-1",
			Role:    aguiTypes.RoleAssistant,
			Content: "looking up weather",
			ToolCalls: []aguiTypes.ToolCall{
				{
					ID:   "call-1",
					Type: aguiTypes.ToolCallTypeFunction,
					Function: aguiTypes.FunctionCall{
						Name:      "get_weather",
						Arguments: `{"city":"London"}`,
					},
				},
			},
		},
		{
			ID:   "assistant-call-2",
			Role: aguiTypes.RoleAssistant,
			ToolCalls: []aguiTypes.ToolCall{
				{
					ID:   "call-2",
					Type: aguiTypes.ToolCallTypeFunction,
					Function: aguiTypes.FunctionCall{
						Name:      "get_time",
						Arguments: `{"city":"London"}`,
					},
				},
			},
		},
		{
			ID:         "tool-result-1",
			Role:       aguiTypes.RoleTool,
			Content:    `{"temperature":"12C"}`,
			ToolCallID: "call-1",
		},
	})
	if err != nil {
		t.Fatalf("toAgentMessages() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(toAgentMessages()) = %d, want 2", len(got))
	}

	assistant := got[0]
	if assistant.ID != "assistant-call-1" {
		t.Fatalf("coalesced assistant ID = %q, want first message ID", assistant.ID)
	}
	if assistant.Role != message.RoleAssistant {
		t.Fatalf("coalesced assistant role = %q, want %q", assistant.Role, message.RoleAssistant)
	}
	if len(assistant.Contents) != 3 {
		t.Fatalf("len(coalesced assistant contents) = %d, want 3", len(assistant.Contents))
	}
	if text, ok := assistant.Contents[0].(*message.TextContent); !ok || text.Text != "looking up weather" {
		t.Fatalf("first coalesced content = %#v, want text content", assistant.Contents[0])
	}
	firstCall, ok := assistant.Contents[1].(*message.FunctionCallContent)
	if !ok {
		t.Fatalf("second coalesced content = %#v, want function call", assistant.Contents[1])
	}
	if firstCall.CallID != "call-1" || firstCall.Name != "get_weather" {
		t.Fatalf("first coalesced call = %#v, want call-1/get_weather", firstCall)
	}
	secondCall, ok := assistant.Contents[2].(*message.FunctionCallContent)
	if !ok {
		t.Fatalf("third coalesced content = %#v, want function call", assistant.Contents[2])
	}
	if secondCall.CallID != "call-2" || secondCall.Name != "get_time" {
		t.Fatalf("second coalesced call = %#v, want call-2/get_time", secondCall)
	}
}
