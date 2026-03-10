// Copyright (c) Microsoft. All rights reserved.

package message

import (
	"cmp"
	"iter"
	"maps"
	"strings"
	"time"
)

// Role represents the role of a message sender in a conversation.
type Role string

const (
	// RoleUser represents a message from the user.
	RoleUser Role = "user"
	// RoleAssistant represents a message from the assistant.
	RoleAssistant Role = "assistant"
	// RoleSystem represents a system message.
	RoleSystem Role = "system"
	// RoleTool represents a message from a tool execution.
	RoleTool Role = "tool"
)

// Message represents a message in a conversation.
type Message struct {
	AdditionalProperties map[string]any `json:",omitzero"`
	Contents             Contents
	Role                 Role
	ID                   string
	AuthorID             string    `json:",omitzero"`
	AuthorName           string    `json:",omitzero"`
	SourceID             string    `json:",omitzero"`
	CreatedAt            time.Time `json:",omitzero"`
	RawRepresentation    any       `json:"-"`
}

// New creates a new [Message] with the given role and contents.
func New(contents ...Content) *Message {
	return &Message{
		Role:     RoleUser,
		Contents: contents,
	}
}

// NewText creates a new [Message] with text content.
func NewText(text string) *Message {
	return New(&TextContent{Text: text})
}

func (m *Message) String() string {
	return m.Contents.Text()
}

func (m *Message) Usage() UsageDetails {
	return m.Contents.Usage()
}

// Clone creates a shallow copy of the message.
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	v := *m
	v.AdditionalProperties = maps.Clone(m.AdditionalProperties)
	return &v
}

type Response struct {
	AdditionalProperties map[string]any `json:",omitzero"`
	CreatedAt            time.Time      `json:",omitzero"`
	ContinuationToken    string         `json:",omitzero"`
	Messages             []*Message
}

func (resp *Response) String() string {
	var sb strings.Builder
	for _, msg := range resp.Messages {
		for _, c := range msg.Contents {
			if textContent, ok := c.(*TextContent); ok {
				sb.WriteString(textContent.Text)
			}
		}
	}
	return sb.String()
}

// Contents returns a sequence of all the contents in the response, across all messages.
// The contents are returned in the order they were added to the response.
func (resp *Response) Contents() iter.Seq[Content] {
	return func(yield func(Content) bool) {
		for _, msg := range resp.Messages {
			for _, c := range msg.Contents {
				if !yield(c) {
					return
				}
			}
		}
	}
}

func (resp *Response) Usage() UsageDetails {
	var usage UsageDetails
	for _, msg := range resp.Messages {
		usage.Add(msg.Usage())
	}
	return usage
}

func (resp *Response) Coalesce() {
	for _, msg := range resp.Messages {
		msg.Contents = CoalesceContents(msg.Contents)
	}
}

func (resp *Response) Update(update *ResponseUpdate) {
	if update == nil {
		return
	}
	// If there is no message created yet, or if the last update we saw had a different
	// identifying parts, create a new message.
	isNewMessage := true
	if len(resp.Messages) > 0 {
		lastMsg := resp.Messages[len(resp.Messages)-1]
		isNewMessage = notEmptyNorEqual(update.AuthorName, lastMsg.AuthorName) ||
			notEmptyNorEqual(update.MessageID, lastMsg.ID) ||
			notEmptyNorEqual(string(update.Role), string(lastMsg.Role))
	}
	// Get the message to target, either a new one or the last ones.
	var msg *Message
	if isNewMessage {
		msg = &Message{
			Role: RoleAssistant,
		}
		resp.Messages = append(resp.Messages, msg)
	} else {
		msg = resp.Messages[len(resp.Messages)-1]
	}
	// Some members on RunResponseUpdate map to members of Message.
	// Incorporate those into the latest message; in cases where the message
	// stores a single value, prefer the latest update's value over anything
	// stored in the message.
	msg.AuthorID = cmp.Or(update.AuthorID, msg.AuthorID)
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
	if msg.RawRepresentation == nil {
		msg.RawRepresentation = update.RawRepresentation
	} else if s, ok := msg.RawRepresentation.([]any); ok {
		msg.RawRepresentation = append(s, update.RawRepresentation)
	} else {
		msg.RawRepresentation = []any{msg.RawRepresentation, update.RawRepresentation}
	}

	// Other members on a RunResponseUpdate map to members of the response.
	// Update the response object with those, preferring the values from later updates.
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

// notEmptyNorEqual returns true if both strings are not empty and not the same as each other.
func notEmptyNorEqual(s1, s2 string) bool {
	return s1 != "" && s2 != "" && s1 != s2
}

type ResponseUpdate struct {
	RawRepresentation    any            `json:"-"`
	AdditionalProperties map[string]any `json:",omitzero"`
	AuthorID             string
	MessageID            string
	ResponseID           string
	AuthorName           string    `json:",omitzero"`
	Role                 Role      `json:",omitzero"`
	ContinuationToken    string    `json:",omitzero"`
	CreatedAt            time.Time `json:",omitzero"`
	Contents             Contents  `json:",omitzero"`
}

// String returns the concatenated text contents of the response messages.
func (r *ResponseUpdate) String() string {
	var sb strings.Builder
	for _, c := range r.Contents {
		if textContent, ok := c.(*TextContent); ok {
			sb.WriteString(textContent.Text)
		}
	}
	return sb.String()
}

func (m ResponseUpdate) Usage() UsageDetails {
	return m.Contents.Usage()
}
