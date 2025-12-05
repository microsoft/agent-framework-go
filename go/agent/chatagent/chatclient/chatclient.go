// Copyright (c) Microsoft. All rights reserved.

package chatclient

import (
	"cmp"
	"context"
	"iter"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/microsoft/agent-framework/go/format"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/param"
	"github.com/microsoft/agent-framework/go/tool"
)

type Capabilities struct {
	Streaming        bool
	StructuredOutput format.Formatter // nil if structured output is not supported
}

type Client interface {
	Capabilities() Capabilities
	Response(context.Context, ChatOptions, ...*message.Message) iter.Seq2[*ChatResponseUpdate, error]
}

type ChatResponseUpdate struct {
	AdditionalProperties map[string]any
	AuthorName           string
	ConversationID       string
	FinishReason         string
	MessageID            string
	ModelID              string
	ResponseID           string
	Role                 message.Role
	CreatedAt            time.Time
	RawRepresentation    any
	Contents             []message.Content
}

// String returns the concatenated text contents of the response messages.
func (r *ChatResponseUpdate) String() string {
	var sb strings.Builder
	for _, c := range r.Contents {
		if textContent, ok := c.(*message.TextContent); ok {
			sb.WriteString(textContent.Text)
		}
	}
	return sb.String()
}

type ChatOptions struct {
	Streaming                param.Opt[bool]
	AllowBackgroundResponses param.Opt[bool]

	ContinuationToken any

	// ResponseFormat represents the desired response format for agent execution.
	// It is up to the client implementation if or how to honor the request.
	// If the client implementation doesn't recognize the specific kind, it can be ignored.
	// If nil, the client default is used.
	ResponseFormat format.Format

	// Tools to make available to the agent.
	Tools []tool.Tool

	// ToolMode specifies how tools should be used.
	ToolMode tool.ToolMode

	// MaxTurns limits the number of agent turns.
	MaxTurns int

	ConversationID string

	Instructions string

	// Temperature controls randomness in generation.
	Temperature param.Opt[float64]

	// TopP controls nucleus sampling.
	TopP param.Opt[float64]

	// MaxTokens limits the response length.
	MaxTokens param.Opt[int]

	// AdditionalProperties for provider-specific options.
	AdditionalProperties map[string]any
}

func (o *ChatOptions) Clone() *ChatOptions {
	if o == nil {
		return nil
	}
	clone := *o
	clone.Tools = slices.Clone(o.Tools)
	clone.AdditionalProperties = maps.Clone(o.AdditionalProperties)
	return &clone
}

// Copy copies other into o, prioritizing the o values when both are set.
func (o *ChatOptions) Copy(other *ChatOptions) {
	if other == nil {
		return
	}
	o.Temperature = o.Temperature.OrOpt(other.Temperature)
	o.TopP = o.TopP.OrOpt(other.TopP)
	o.MaxTokens = o.MaxTokens.OrOpt(other.MaxTokens)
	o.AllowBackgroundResponses = o.AllowBackgroundResponses.OrOpt(other.AllowBackgroundResponses)
	o.Instructions = cmp.Or(o.Instructions, other.Instructions)
	o.ConversationID = cmp.Or(o.ConversationID, other.ConversationID)
	o.ResponseFormat = cmp.Or(o.ResponseFormat, other.ResponseFormat)
	o.ToolMode = cmp.Or(o.ToolMode, other.ToolMode)
	o.MaxTurns = cmp.Or(o.MaxTurns, other.MaxTurns)
	o.ContinuationToken = cmp.Or(o.ContinuationToken, other.ContinuationToken)
	o.Tools = append(o.Tools, other.Tools...)
	if o.AdditionalProperties != nil && other.AdditionalProperties != nil {
		// Merge only the additional properties from the agent if they are not already set in the request options.
		for k, v := range other.AdditionalProperties {
			if _, exists := o.AdditionalProperties[k]; !exists {
				o.AdditionalProperties[k] = v
			}
		}
	} else if other.AdditionalProperties != nil {
		o.AdditionalProperties = maps.Clone(other.AdditionalProperties)
	}
}

func NewMessageFromUpdates(updates []*ChatResponseUpdate) []*message.Message {
	var msgs []*message.Message
	for _, update := range updates {
		isNewMessage := true
		if len(msgs) > 0 {
			lastMsg := msgs[len(msgs)-1]
			isNewMessage = notEmptyNorEqual(update.AuthorName, lastMsg.AuthorName) ||
				notEmptyNorEqual(update.MessageID, lastMsg.ID) ||
				notEmptyNorEqual(string(update.Role), string(lastMsg.Role))
		}
		// Get the message to target, either a new one or the last ones.
		var msg *message.Message
		if isNewMessage {
			msg = &message.Message{
				Role: message.RoleAssistant,
			}
			msgs = append(msgs, msg)
		} else {
			msg = msgs[len(msgs)-1]
		}
		// Some members on ChatResponseUpdate map to members of ChatMessage.
		// Incorporate those into the latest message; in cases where the message
		// stores a single value, prefer the latest update's value over anything
		// stored in the message.
		msg.AuthorName = cmp.Or(update.AuthorName, msg.AuthorName)
		msg.Role = cmp.Or(update.Role, msg.Role)
		msg.ID = cmp.Or(update.MessageID, msg.ID)
		if msg.CreatedAt.IsZero() || (!update.CreatedAt.IsZero() && update.CreatedAt.After(msg.CreatedAt)) {
			msg.CreatedAt = update.CreatedAt
		}
		msg.Contents = append(msg.Contents, update.Contents...)
	}
	for _, msg := range msgs {
		msg.Contents = message.CoalesceContents(msg.Contents)
	}
	return msgs
}

// notEmptyNorEqual returns true if both strings are not empty and not the same as each other.
func notEmptyNorEqual(s1, s2 string) bool {
	return s1 != "" && s2 != "" && s1 != s2
}
