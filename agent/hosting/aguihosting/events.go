// Copyright (c) Microsoft. All rights reserved.

package aguihosting

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	aguiEvents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func filterServerToolsFromMixedInvocations(update *agent.ResponseUpdate, clientToolNames map[string]struct{}) (*agent.ResponseUpdate, map[string]struct{}) {
	if update == nil || len(clientToolNames) == 0 || len(update.Contents) == 0 {
		return update, nil
	}

	containsClient := false
	containsServer := false
	for _, c := range update.Contents {
		fcc, ok := c.(*message.FunctionCallContent)
		if !ok {
			continue
		}
		_, isClient := clientToolNames[fcc.Name]
		if isClient {
			containsClient = true
		} else {
			containsServer = true
		}
		if containsClient && containsServer {
			break
		}
	}

	if !containsClient || !containsServer {
		return update, nil
	}

	filtered := make(message.Contents, 0, len(update.Contents))
	suppressed := map[string]struct{}{}
	for _, c := range update.Contents {
		fcc, ok := c.(*message.FunctionCallContent)
		if !ok {
			filtered = append(filtered, c)
			continue
		}
		if _, isClient := clientToolNames[fcc.Name]; isClient {
			filtered = append(filtered, c)
		} else if fcc.CallID != "" {
			suppressed[fcc.CallID] = struct{}{}
		}
	}
	clone := *update
	clone.Contents = filtered
	return &clone, suppressed
}

func updatesToAGUIEvents(
	ctx context.Context,
	updates iter.Seq2[*agent.ResponseUpdate, error],
	threadID string,
	runID string,
	clientToolNames map[string]struct{},
) iter.Seq2[aguiEvents.Event, error] {
	return func(yield func(aguiEvents.Event, error) bool) {
		if !yield(aguiEvents.NewRunStartedEvent(threadID, runID), nil) {
			return
		}

		var currentMessageID string
		var currentReasoningMsgID string
		suppressedCallIDs := map[string]struct{}{}
		for update, err := range updates {
			if err != nil {
				if !yield(aguiEvents.NewRunErrorEvent(err.Error(), aguiEvents.WithRunID(runID)), err) {
					return
				}
				return
			}
			if ctx.Err() != nil {
				if !yield(aguiEvents.NewRunErrorEvent(ctx.Err().Error(), aguiEvents.WithRunID(runID)), ctx.Err()) {
					return
				}
				return
			}

			var suppressed map[string]struct{}
			update, suppressed = filterServerToolsFromMixedInvocations(update, clientToolNames)
			for callID := range suppressed {
				suppressedCallIDs[callID] = struct{}{}
			}
			if update == nil {
				continue
			}

			msgID := update.MessageID
			if msgID == "" {
				msgID = aguiEvents.GenerateMessageID()
			}

			if currentReasoningMsgID != "" && currentReasoningMsgID != msgID {
				if !yield(aguiEvents.NewReasoningMessageEndEvent(currentReasoningMsgID), nil) {
					return
				}
				currentReasoningMsgID = ""
			}

			hasText := hasTextLikeContent(update.Contents)
			if hasText && currentMessageID != msgID {
				if currentMessageID != "" {
					if !yield(aguiEvents.NewTextMessageEndEvent(currentMessageID), nil) {
						return
					}
				}
				role := string(update.Role)
				if role == "" {
					role = string(message.RoleAssistant)
				}
				if !yield(aguiEvents.NewTextMessageStartEvent(msgID, aguiEvents.WithRole(role)), nil) {
					return
				}
				currentMessageID = msgID
			}

			hasReasoning := hasReasoningContent(update.Contents)
			if hasReasoning && currentReasoningMsgID != msgID {
				if currentReasoningMsgID != "" {
					if !yield(aguiEvents.NewReasoningMessageEndEvent(currentReasoningMsgID), nil) {
						return
					}
				}
				role := string(update.Role)
				if role == "" {
					role = string(message.RoleAssistant)
				}
				if !yield(aguiEvents.NewReasoningMessageStartEvent(msgID, role), nil) {
					return
				}
				currentReasoningMsgID = msgID
			}

			for _, c := range update.Contents {
				if frc, ok := c.(*message.FunctionResultContent); ok {
					if _, isSuppressed := suppressedCallIDs[frc.CallID]; isSuppressed {
						continue
					}
				}
				if rc, ok := c.(*message.TextReasoningContent); ok {
					if rc.Text != "" {
						if !yield(aguiEvents.NewReasoningMessageContentEvent(currentReasoningMsgID, rc.Text), nil) {
							return
						}
					}
					if rc.ProtectedData != "" {
						if !yield(aguiEvents.NewReasoningEncryptedValueEvent(aguiEvents.ReasoningEncryptedValueSubtypeMessage, currentReasoningMsgID, rc.ProtectedData), nil) {
							return
						}
					}
					continue
				}
				events, convErr := contentToEvents(c, msgID)
				if convErr != nil {
					if !yield(aguiEvents.NewRunErrorEvent(convErr.Error(), aguiEvents.WithRunID(runID)), convErr) {
						return
					}
					return
				}
				for _, e := range events {
					if !yield(e, nil) {
						return
					}
				}
			}
		}

		if currentReasoningMsgID != "" {
			if !yield(aguiEvents.NewReasoningMessageEndEvent(currentReasoningMsgID), nil) {
				return
			}
		}
		if currentMessageID != "" {
			if !yield(aguiEvents.NewTextMessageEndEvent(currentMessageID), nil) {
				return
			}
		}
		yield(aguiEvents.NewRunFinishedEvent(threadID, runID), nil)
	}
}

func hasTextLikeContent(contents message.Contents) bool {
	for _, c := range contents {
		text, ok := c.(*message.TextContent)
		if ok && text.Text != "" {
			return true
		}
		dc, ok := c.(*message.DataContent)
		if ok && shouldEmitDataContentAsText(dc) {
			return true
		}
	}
	return false
}

func hasReasoningContent(contents message.Contents) bool {
	for _, c := range contents {
		rc, ok := c.(*message.TextReasoningContent)
		if ok && (rc.Text != "" || rc.ProtectedData != "") {
			return true
		}
	}
	return false
}

func contentToEvents(content message.Content, messageID string) ([]aguiEvents.Event, error) {
	switch c := content.(type) {
	case *message.TextContent:
		if c.Text == "" {
			return nil, nil
		}
		return []aguiEvents.Event{aguiEvents.NewTextMessageContentEvent(messageID, c.Text)}, nil
	case *message.FunctionCallContent:
		callID := c.CallID
		if callID == "" {
			callID = aguiEvents.GenerateToolCallID()
		}
		args := strings.TrimSpace(c.Arguments)
		if args == "" {
			args = "{}"
		}
		return []aguiEvents.Event{
			aguiEvents.NewToolCallStartEvent(callID, c.Name),
			aguiEvents.NewToolCallArgsEvent(callID, args),
			aguiEvents.NewToolCallEndEvent(callID),
		}, nil
	case *message.FunctionResultContent:
		callID := c.CallID
		if callID == "" {
			callID = aguiEvents.GenerateToolCallID()
		}
		contentStr, err := serializeToolResult(c.Result)
		if err != nil {
			return nil, err
		}
		// Use result-{callID} so each tool result has a deterministically unique
		// message ID. MEAI batches all results under a shared MessageId, which
		// collapses them in FE reconciliation when the same id is reused.
		return []aguiEvents.Event{aguiEvents.NewToolCallResultEvent("result-"+callID, callID, contentStr)}, nil
	case *message.DataContent:
		return dataContentToEvents(c, messageID)
	default:
		return nil, nil
	}
}

func dataContentToEvents(content *message.DataContent, messageID string) ([]aguiEvents.Event, error) {
	b, err := content.Bytes()
	if err != nil {
		return nil, err
	}
	mediaType := strings.ToLower(strings.TrimSpace(content.MediaType))
	switch mediaType {
	case "application/json":
		var snapshot any
		if err := json.Unmarshal(b, &snapshot); err != nil {
			return nil, err
		}
		return []aguiEvents.Event{aguiEvents.NewStateSnapshotEvent(snapshot)}, nil
	case "application/json-patch+json":
		var delta []aguiEvents.JSONPatchOperation
		if err := json.Unmarshal(b, &delta); err != nil {
			return nil, err
		}
		return []aguiEvents.Event{aguiEvents.NewStateDeltaEvent(delta)}, nil
	default:
		return []aguiEvents.Event{aguiEvents.NewTextMessageContentEvent(messageID, string(b))}, nil
	}
}

func shouldEmitDataContentAsText(content *message.DataContent) bool {
	if content == nil {
		return false
	}
	mediaType := strings.ToLower(strings.TrimSpace(content.MediaType))
	return mediaType != "application/json" && mediaType != "application/json-patch+json"
}

func serializeToolResult(result any) (string, error) {
	if result == nil {
		return "", nil
	}
	if s, ok := result.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to serialize tool result: %w", err)
	}
	return string(b), nil
}
