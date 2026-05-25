// Copyright (c) Microsoft. All rights reserved.

package aguihosting

import (
	"context"
	"encoding/json"
	"fmt"

	aguiTypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func toAgentMessages(agMessages []aguiTypes.Message) ([]*message.Message, error) {
	result := make([]*message.Message, 0, len(agMessages))

	// Coalesce consecutive assistant messages that carry tool calls into one.
	// The AG-UI client creates a separate assistant message per tool call when
	// ToolCallStartEvent.parentMessageId is empty, but OpenAI requires every
	// assistant message with tool_calls to be immediately followed by tool
	// responses for each of its tool_call_ids. Two consecutive single-tool-call
	// assistant messages without intervening tool results trigger HTTP 400.
	var pendingMsg *message.Message

	flush := func() {
		if pendingMsg != nil {
			result = append(result, pendingMsg)
			pendingMsg = nil
		}
	}

	for _, m := range agMessages {
		isAssistantWithToolCalls := m.Role == aguiTypes.RoleAssistant && len(m.ToolCalls) > 0

		if !isAssistantWithToolCalls {
			flush()
		}

		converted, err := toAgentMessage(m)
		if err != nil {
			return nil, err
		}

		if isAssistantWithToolCalls {
			if pendingMsg == nil {
				pendingMsg = converted
			} else {
				pendingMsg.Contents = append(pendingMsg.Contents, converted.Contents...)
			}
		} else {
			result = append(result, converted)
		}
	}
	flush()

	return result, nil
}

func toAgentMessage(m aguiTypes.Message) (*message.Message, error) {
	out := &message.Message{ID: m.ID}

	switch m.Role {
	case aguiTypes.RoleSystem:
		out.Role = message.RoleSystem
		out.Contents = message.Contents{&message.TextContent{Text: contentAsString(m.Content)}}
	case aguiTypes.RoleUser:
		out.Role = message.RoleUser
		out.Contents = message.Contents{&message.TextContent{Text: contentAsString(m.Content)}}
	case aguiTypes.RoleAssistant:
		out.Role = message.RoleAssistant
		contents := make(message.Contents, 0, 1+len(m.ToolCalls))
		if content := contentAsString(m.Content); content != "" {
			contents = append(contents, &message.TextContent{Text: content})
		}
		for _, call := range m.ToolCalls {
			contents = append(contents, &message.FunctionCallContent{
				CallID:    call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			})
		}
		out.Contents = contents
	case aguiTypes.RoleTool:
		out.Role = message.RoleTool
		result := contentAsMaybeJSON(m.Content)
		out.Contents = message.Contents{&message.FunctionResultContent{CallID: m.ToolCallID, Result: result}}
	default:
		return nil, fmt.Errorf("unsupported AG-UI role %q", m.Role)
	}

	return out, nil
}

func toClientToolNames(tools []aguiTypes.Tool) map[string]struct{} {
	if len(tools) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		if t.Name != "" {
			names[t.Name] = struct{}{}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func toDeclarationTools(tools []aguiTypes.Tool) []tool.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			continue
		}
		out = append(out, declarationTool{name: t.Name, description: t.Description, schema: t.Parameters})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type declarationTool struct {
	name        string
	description string
	schema      any
}

func (t declarationTool) Name() string        { return t.name }
func (t declarationTool) Description() string { return t.description }
func (t declarationTool) Schema() any         { return t.schema }
func (t declarationTool) ReturnSchema() any   { return nil }

func (t declarationTool) Call(ctx context.Context, args string) (any, error) {
	return nil, fmt.Errorf("client tool %q cannot be invoked on server", t.name)
}

func contentAsString(content any) string {
	switch value := content.(type) {
	case nil:
		return ""
	case string:
		return value
	default:
		b, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(b)
	}
}

func contentAsMaybeJSON(content any) any {
	str := contentAsString(content)
	if str == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(str), &v); err != nil {
		return str
	}
	return v
}
