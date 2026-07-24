// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"cmp"
	"iter"
	"maps"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/message"
)

// ResponseStream represents an execution of the agent.
type ResponseStream iter.Seq2[*ResponseUpdate, error]

// Collect gathers all response updates into a single Response object.
func (r ResponseStream) Collect() (*Response, error) {
	var resp Response
	for update, err := range r {
		if err != nil {
			return nil, err
		}
		resp.Update(update)
	}
	resp.Coalesce()
	return &resp, nil
}

// Response represents the complete result of an [Agent] run.
//
// A Response is the non-streaming counterpart to [ResponseUpdate]. Streaming
// runs can be collected into a Response with [ResponseStream.Collect], which
// merges updates that belong to the same logical message and coalesces adjacent
// content items where possible.
type Response struct {
	// AdditionalProperties contains provider-specific metadata associated with
	// the response that does not fit the standard response schema.
	AdditionalProperties map[string]any `json:",omitzero"`

	// AgentID identifies the agent that generated this response.
	AgentID string `json:",omitzero"`

	// ID identifies this response.
	ID string `json:",omitzero"`

	// CreatedAt is the timestamp for the response. It is zero when the provider
	// did not supply a creation time.
	CreatedAt time.Time `json:",omitzero"`

	// ContinuationToken is used to continue a background response. When present,
	// pass it to a later run with [WithContinuationToken] to poll for completion
	// or resume streaming, depending on the provider.
	ContinuationToken string `json:",omitzero"`

	// FinishReason describes why the agent stopped generating. Common values are
	// "stop", "length", and "tool_calls". It is empty when the provider did not
	// supply a finish reason.
	FinishReason string `json:",omitzero"`

	// Messages contains the messages produced by the agent run.
	Messages []*message.Message
}

func (resp *Response) String() string {
	if resp == nil {
		return ""
	}
	var sb strings.Builder
	for _, msg := range resp.Messages {
		for _, c := range msg.Contents {
			if textContent, ok := c.(*message.TextContent); ok {
				sb.WriteString(textContent.Text)
			}
		}
	}
	return sb.String()
}

// Contents returns a sequence of all the contents in the response, across all messages.
// The contents are returned in the order they were added to the response.
func (resp *Response) Contents() iter.Seq[message.Content] {
	return func(yield func(message.Content) bool) {
		if resp == nil {
			return
		}
		for _, msg := range resp.Messages {
			for _, c := range msg.Contents {
				if !yield(c) {
					return
				}
			}
		}
	}
}

func (resp *Response) Usage() message.UsageDetails {
	var usage message.UsageDetails
	if resp == nil {
		return usage
	}
	for _, msg := range resp.Messages {
		usage.Add(msg.Usage())
	}
	return usage
}

// Coalesce merges adjacent compatible content items within each message.
func (resp *Response) Coalesce() {
	if resp == nil {
		return
	}
	for _, msg := range resp.Messages {
		msg.Contents = message.CoalesceContents(msg.Contents)
	}
}

// ToUpdates converts this response into response updates suitable for streaming
// scenarios.
//
// Each message in the response becomes a separate update. Response-level usage,
// additional properties, and a non-empty continuation token are included as an
// additional metadata-only update when present.
func (resp *Response) ToUpdates() []*ResponseUpdate {
	if resp == nil {
		return nil
	}
	usage := resp.Usage()
	hasUsage := !isZeroUsage(usage)
	hasAdditionalProperties := resp.AdditionalProperties != nil

	updates := make([]*ResponseUpdate, 0, len(resp.Messages)+1)
	for _, msg := range resp.Messages {
		createdAt := msg.CreatedAt
		if createdAt.IsZero() {
			createdAt = resp.CreatedAt
		}
		updates = append(updates, &ResponseUpdate{
			RawRepresentation:    msg.RawRepresentation,
			AdditionalProperties: msg.AdditionalProperties,
			AgentID:              resp.AgentID,
			MessageID:            msg.ID,
			ResponseID:           resp.ID,
			FinishReason:         resp.FinishReason,
			AuthorName:           msg.AuthorName,
			Role:                 msg.Role,
			CreatedAt:            createdAt,
			Contents:             msg.Contents,
		})
	}

	if hasUsage || hasAdditionalProperties || resp.ContinuationToken != "" {
		extra := &ResponseUpdate{
			AdditionalProperties: resp.AdditionalProperties,
			AgentID:              resp.AgentID,
			ResponseID:           resp.ID,
			ContinuationToken:    resp.ContinuationToken,
			CreatedAt:            resp.CreatedAt,
		}
		if hasUsage {
			extra.Contents = message.Contents{&message.UsageContent{Details: usage}}
		}
		updates = append(updates, extra)
	}

	return updates
}

func isZeroUsage(usage message.UsageDetails) bool {
	return usage.InputTokenCount == 0 &&
		usage.OutputTokenCount == 0 &&
		usage.TotalTokenCount == 0 &&
		usage.CachedInputTokenCount == 0 &&
		usage.ReasoningTokenCount == 0 &&
		len(usage.AdditionalCounts) == 0
}

func (resp *Response) Update(update *ResponseUpdate) {
	if update == nil {
		return
	}
	msg := resp.targetMessage(update)
	// Some members on ResponseUpdate map to members of Message.
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
	if update.AdditionalProperties != nil {
		if msg.AdditionalProperties == nil {
			msg.AdditionalProperties = make(map[string]any)
		}
		maps.Copy(msg.AdditionalProperties, update.AdditionalProperties)
	}
	// A nil RawRepresentation carries no provider data, so treat it as a no-op.
	// This keeps metadata-only updates (e.g. response-level usage or a
	// continuation token emitted by ToUpdates) from mutating the message's raw
	// data during a ToUpdates/Collect round-trip.
	if update.RawRepresentation != nil {
		if msg.RawRepresentation == nil {
			msg.RawRepresentation = update.RawRepresentation
		} else if s, ok := msg.RawRepresentation.([]any); ok {
			msg.RawRepresentation = append(s, update.RawRepresentation)
		} else {
			msg.RawRepresentation = []any{msg.RawRepresentation, update.RawRepresentation}
		}
	}

	// Other members on a ResponseUpdate map to members of the response.
	// Update the response object with those, preferring the values from later updates.
	resp.AgentID = cmp.Or(update.AgentID, resp.AgentID)
	resp.ID = cmp.Or(update.ResponseID, resp.ID)
	resp.FinishReason = cmp.Or(update.FinishReason, resp.FinishReason)
	if update.ContinuationToken == "" {
		resp.ContinuationToken = ""
	} else {
		resp.ContinuationToken = update.ContinuationToken
	}
	if resp.CreatedAt.IsZero() || (!update.CreatedAt.IsZero() && update.CreatedAt.After(resp.CreatedAt)) {
		resp.CreatedAt = update.CreatedAt
	}
	if update.AdditionalProperties != nil {
		if resp.AdditionalProperties == nil {
			resp.AdditionalProperties = make(map[string]any)
		}
		maps.Copy(resp.AdditionalProperties, update.AdditionalProperties)
	}
}

func (resp *Response) targetMessage(update *ResponseUpdate) *message.Message {
	if len(resp.Messages) > 0 {
		lastMsg := resp.Messages[len(resp.Messages)-1]
		if !isDifferentMessage(update, lastMsg) {
			return lastMsg
		}
	}

	msg := &message.Message{
		Role: message.RoleAssistant,
	}
	resp.Messages = append(resp.Messages, msg)
	return msg
}

func isDifferentMessage(update *ResponseUpdate, msg *message.Message) bool {
	return notEmptyNorEqual(update.AuthorName, msg.AuthorName) ||
		notEmptyNorEqual(update.MessageID, msg.ID) ||
		notEmptyNorEqual(string(update.Role), string(msg.Role))
}

// notEmptyNorEqual returns true if both strings are not empty and not the same as each other.
func notEmptyNorEqual(s1, s2 string) bool {
	return s1 != "" && s2 != "" && s1 != s2
}

// ResponseUpdate represents a single streaming response chunk from an [Agent].
//
// Updates layer on each other to form a [Response]. A single update may contain
// content for a message, metadata about the response, a continuation token for
// background work, or any combination of those values. To get the text for the
// update, call [ResponseUpdate.String].
type ResponseUpdate struct {
	// RawRepresentation stores the provider-specific object that produced this
	// update. It is useful for debugging or for consumers that need to access the
	// underlying provider model.
	RawRepresentation any `json:"-"`

	// AdditionalProperties contains provider-specific metadata associated with
	// the update that does not fit the standard response-update schema.
	AdditionalProperties map[string]any `json:",omitzero"`

	// AgentID identifies the agent that produced this update.
	AgentID string

	// MessageID identifies the message of which this update is a part. Updates
	// with the same non-empty MessageID are merged into the same message when
	// collected into a [Response].
	MessageID string

	// ResponseID identifies the response of which this update is a part.
	ResponseID string

	// FinishReason is the reason the generation ended. It is typically set only
	// on the final update of a stream. Common values are "stop", "length", and
	// "tool_calls".
	FinishReason string `json:",omitzero"`

	// AuthorName is the display name of the author or agent that produced this update.
	AuthorName string `json:",omitzero"`

	// Role is the role of the update author.
	Role message.Role `json:",omitzero"`

	// ContinuationToken is used to continue the streamed response. When present,
	// pass the latest token to a later run with [WithContinuationToken] to resume
	// or poll for the same background response, depending on the provider.
	ContinuationToken string `json:",omitzero"`

	// CreatedAt is the timestamp for this update. It is zero when the provider
	// did not supply a creation time.
	CreatedAt time.Time `json:",omitzero"`

	// Contents contains the content items carried by this update.
	Contents message.Contents `json:",omitzero"`
}

// String returns the concatenated text contents of the response messages.
func (r *ResponseUpdate) String() string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range r.Contents {
		if textContent, ok := c.(*message.TextContent); ok {
			sb.WriteString(textContent.Text)
		}
	}
	return sb.String()
}

func (m *ResponseUpdate) Usage() message.UsageDetails {
	if m == nil || m.Contents == nil {
		return message.UsageDetails{}
	}
	return m.Contents.Usage()
}
