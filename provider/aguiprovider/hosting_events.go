// Copyright (c) Microsoft. All rights reserved.

package aguiprovider

import (
	"context"
	"encoding/json"
	"iter"
	"strings"

	aguiEvents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguiTypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
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
		var currentMessageSourceID string
		var currentReasoningMsgID string
		var currentReasoningSourceID string
		usedMessageIDs := map[string]struct{}{}
		allocateMessageID := func(preferred string) string {
			if preferred != "" {
				if _, used := usedMessageIDs[preferred]; !used {
					usedMessageIDs[preferred] = struct{}{}
					return preferred
				}
			}
			for {
				generated := aguiEvents.GenerateMessageID()
				if _, used := usedMessageIDs[generated]; used {
					continue
				}
				usedMessageIDs[generated] = struct{}{}
				return generated
			}
		}
		closeReasoning := func() bool {
			if currentReasoningMsgID == "" {
				return true
			}
			if !yield(aguiEvents.NewReasoningMessageEndEvent(currentReasoningMsgID), nil) {
				return false
			}
			if !yield(aguiEvents.NewReasoningEndEvent(currentReasoningMsgID), nil) {
				return false
			}
			currentReasoningMsgID = ""
			currentReasoningSourceID = ""
			return true
		}
		closeText := func() bool {
			if currentMessageID == "" {
				return true
			}
			if !yield(aguiEvents.NewTextMessageEndEvent(currentMessageID), nil) {
				return false
			}
			currentMessageID = ""
			currentMessageSourceID = ""
			return true
		}
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

			mixedReasoningAndText := hasReasoningContent(update.Contents) && hasTextLikeContent(update.Contents)

			for _, c := range update.Contents {
				if frc, ok := c.(*message.FunctionResultContent); ok {
					if _, isSuppressed := suppressedCallIDs[frc.CallID]; isSuppressed {
						continue
					}
				}
				if rc, ok := c.(*message.TextReasoningContent); ok {
					if rc.Text == "" && rc.ProtectedData == "" {
						continue
					}
					sourceIDChanged := update.MessageID != "" && currentReasoningSourceID != update.MessageID
					if currentReasoningMsgID == "" || sourceIDChanged {
						if !closeReasoning() {
							return
						}
						preferredID := update.MessageID
						if mixedReasoningAndText {
							preferredID = ""
						}
						currentReasoningMsgID = allocateMessageID(preferredID)
						currentReasoningSourceID = update.MessageID
						if !yield(aguiEvents.NewReasoningStartEvent(currentReasoningMsgID), nil) {
							return
						}
						if !yield(aguiEvents.NewReasoningMessageStartEvent(currentReasoningMsgID, string(aguiTypes.RoleReasoning)), nil) {
							return
						}
					}
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

				if !closeReasoning() {
					return
				}

				eventMessageID := update.MessageID
				if isTextLikeContent(c) {
					sourceIDChanged := update.MessageID != "" && currentMessageSourceID != update.MessageID
					if currentMessageID == "" || sourceIDChanged {
						if !closeText() {
							return
						}
						currentMessageID = allocateMessageID(update.MessageID)
						currentMessageSourceID = update.MessageID
						role := string(update.Role)
						if role == "" {
							role = string(message.RoleAssistant)
						}
						if !yield(aguiEvents.NewTextMessageStartEvent(currentMessageID, aguiEvents.WithRole(role)), nil) {
							return
						}
					}
					eventMessageID = currentMessageID
				}

				events, convErr := contentToEvents(c, eventMessageID)
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

		if !closeReasoning() {
			return
		}
		if !closeText() {
			return
		}
		yield(aguiEvents.NewRunFinishedEvent(threadID, runID), nil)
	}
}

func isTextLikeContent(content message.Content) bool {
	switch c := content.(type) {
	case *message.TextContent:
		return c.Text != ""
	case *message.DataContent:
		return shouldEmitDataContentAsText(c)
	case *message.URIContent:
		return c.URI != ""
	default:
		return false
	}
}

func hasTextLikeContent(contents message.Contents) bool {
	for _, c := range contents {
		if isTextLikeContent(c) {
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
	case *message.URIContent:
		// AG-UI has no native reference/attachment event; surface the URI as
		// text so it is not silently dropped (mirrors the DataContent fallback
		// and the A2A hosting path, which never drop unknown content). Gemini,
		// for example, emits URIContent for generated files.
		if c.URI == "" {
			return nil, nil
		}
		return []aguiEvents.Event{aguiEvents.NewTextMessageContentEvent(messageID, c.URI)}, nil
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
