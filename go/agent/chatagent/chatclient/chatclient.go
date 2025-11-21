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

type Client interface {
	Response(context.Context, *ChatOptions, ...*message.Message) (*ChatResponse, error)
	StreamingResponse(context.Context, *ChatOptions, ...*message.Message) iter.Seq2[*ChatResponseUpdate, error]
}

type StructuredResponseClient interface {
	Client

	StructuredResponse(any, context.Context, *ChatOptions, ...*message.Message) (*ChatResponse, error)
}

type ChatResponse struct {
	AdditionalProperties map[string]any
	ConversationID       string
	FinishReason         string
	ModelID              string
	ID                   string
	Role                 message.Role
	CreatedAt            time.Time
	RawRepresentation    any
	Usage                *message.UsageDetails
	Messages             []*message.Message
}

// String returns the concatenated text contents of the response messages.
func (r *ChatResponse) String() string {
	var sb strings.Builder
	for _, msg := range r.Messages {
		for _, c := range msg.Contents {
			if textContent, ok := c.(*message.TextContent); ok {
				sb.WriteString(textContent.Text)
			}
		}
	}
	return sb.String()
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

func NewChatResponseFromUpdates(updates []*ChatResponseUpdate) *ChatResponse {
	r := &ChatResponse{}
	for _, update := range updates {
		processUpdate(r, update)
	}
	finalizeResponse(r)
	return r
}

func finalizeResponse(r *ChatResponse) {
	for _, msg := range r.Messages {
		msg.Contents = message.CoalesceContents(msg.Contents)
	}
}

// processUpdate updates the ChatResponse r with the information from the ChatResponseUpdate update.
func processUpdate(r *ChatResponse, update *ChatResponseUpdate) {
	// If there is no message created yet, or if the last update we saw had a different
	// identifying parts, create a new message.
	isNewMessage := true
	if len(r.Messages) > 0 {
		lastMsg := r.Messages[len(r.Messages)-1]
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
		r.Messages = append(r.Messages, msg)
	} else {
		msg = r.Messages[len(r.Messages)-1]
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
	for _, content := range update.Contents {
		switch c := content.(type) {
		case *message.UsageContent:
			// Usage content is treated specially and propagated to the response's Usage.
			if r.Usage == nil {
				r.Usage = new(message.UsageDetails)
			}
			r.Usage.Add(&c.Details)
		default:
			msg.Contents = append(msg.Contents, content)
		}
	}
	// Other members on a ChatResponseUpdate map to members of the ChatResponse.
	// Update the response object with those, preferring the values from later updates.
	r.ID = cmp.Or(update.ResponseID, r.ID)
	r.ConversationID = cmp.Or(update.ConversationID, r.ConversationID)
	r.FinishReason = cmp.Or(update.FinishReason, r.FinishReason)
	r.Role = cmp.Or(update.Role, r.Role)
	r.ModelID = cmp.Or(update.ModelID, r.ModelID)
	if r.CreatedAt.IsZero() || (!update.CreatedAt.IsZero() && update.CreatedAt.After(r.CreatedAt)) {
		r.CreatedAt = update.CreatedAt
	}
	if update.AdditionalProperties != nil {
		if r.AdditionalProperties == nil {
			r.AdditionalProperties = make(map[string]any)
		}
		maps.Copy(r.AdditionalProperties, update.AdditionalProperties)
	}
}

// notEmptyNorEqual returns true if both strings are not empty and not the same as each other.
func notEmptyNorEqual(s1, s2 string) bool {
	return s1 != "" && s2 != "" && s1 != s2
}
