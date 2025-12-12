// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"cmp"
	"context"
	"iter"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

type Identity struct {
	id          string
	name        string
	description string
}

func NewIdentity(id, name, description string) Identity {
	if id == "" {
		id = uuid.NewString()
	}
	return Identity{
		id:          id,
		name:        name,
		description: description,
	}
}

func (iden Identity) ID() string {
	return iden.id
}

func (iden Identity) Name() string {
	return iden.name
}

func (iden Identity) Description() string {
	return iden.description
}

type Capabilities struct {
	Streaming        bool
	StructuredOutput format.Formatter // nil if structured output is not supported
	Middlewares      []Middleware
	Tools            []tool.Tool
}

type Agent interface {
	Runner

	Identity() Identity
	Capabilities() Capabilities

	NewThread() memory.Thread
	UnmarshalThread(data []byte) (memory.Thread, error)
}

type Runner interface {
	Run(ctx context.Context, options ...Option) iter.Seq2[*RunResponseUpdate, error]
}

type Middleware interface {
	Run(ctx context.Context, next Runner, options ...Option) iter.Seq2[*RunResponseUpdate, error]
}

func NewMessagesFromUpdates(updates []*RunResponseUpdate) []*message.Message {
	var msgs []*message.Message
	for _, update := range updates {
		if update == nil {
			continue
		}
		// If there is no message created yet, or if the last update we saw had a different
		// identifying parts, create a new message.
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
		msg.AuthorName = cmp.Or(update.AuthorName, msg.AuthorName)
		msg.Role = cmp.Or(update.Role, msg.Role)
		msg.ID = cmp.Or(update.MessageID, msg.ID)
		if msg.CreatedAt.IsZero() || (!update.CreatedAt.IsZero() && update.CreatedAt.After(msg.CreatedAt)) {
			msg.CreatedAt = update.CreatedAt
		}
		for _, content := range update.Contents {
			msg.Contents = append(msg.Contents, content)
		}
	}
	for _, msg := range msgs {
		msg.Contents = message.CoalesceContents(msg.Contents)
	}
	return msgs
}

// processUpdate updates the ChatResponse r with the information from the ChatResponseUpdate update.
func processUpdate(r *RunResponse, update *RunResponseUpdate) {
	if update == nil {
		return
	}
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
			r.Usage.Add(c.Details)
		default:
			msg.Contents = append(msg.Contents, content)
		}
	}
	// Other members on a ChatResponseUpdate map to members of the ChatResponse.
	// Update the response object with those, preferring the values from later updates.
	r.ID = cmp.Or(update.ResponseID, r.ID)
	if r.CreatedAt.IsZero() || (!update.CreatedAt.IsZero() && update.CreatedAt.After(r.CreatedAt)) {
		r.CreatedAt = update.CreatedAt
	}
	// If the update provides a nil ContinuationToken, clear it; otherwise, use the update's value.
	if update.ContinuationToken == nil {
		r.ContinuationToken = nil
	} else {
		r.ContinuationToken = update.ContinuationToken
	}
	if update.AdditionalProperties != nil {
		if r.AdditionalProperties == nil {
			r.AdditionalProperties = make(map[string]any)
		}
		maps.Copy(r.AdditionalProperties, update.AdditionalProperties)
	}
	if r.RawRepresentation == nil {
		r.RawRepresentation = update.RawRepresentation
	} else if s, ok := r.RawRepresentation.([]any); ok {
		r.RawRepresentation = append(s, update.RawRepresentation)
	} else {
		r.RawRepresentation = []any{r.RawRepresentation, update.RawRepresentation}
	}
}

// notEmptyNorEqual returns true if both strings are not empty and not the same as each other.
func notEmptyNorEqual(s1, s2 string) bool {
	return s1 != "" && s2 != "" && s1 != s2
}

// RunResponse represents the result of an agent execution.
type RunResponse struct {
	RawRepresentation    any
	AdditionalProperties map[string]any
	ID                   string
	AgentID              string
	ContinuationToken    any
	CreatedAt            time.Time
	Usage                *message.UsageDetails
	Messages             []*message.Message
}

// String returns the concatenated text contents of the response messages.
func (r *RunResponse) String() string {
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

func (r *RunResponse) UserInputRequests() iter.Seq[message.Content] {
	return func(yield func(message.Content) bool) {
		for _, msg := range r.Messages {
			for _, c := range msg.Contents {
				switch c := c.(type) {
				case *message.FunctionApprovalRequestContent:
					if !yield(c) {
						return
					}
				}
			}
		}
	}
}

// RunResponseUpdate represents a streaming update from an agent execution.
type RunResponseUpdate struct {
	RawRepresentation    any            `json:"-"`
	AdditionalProperties map[string]any `json:",omitzero"`
	AgentID              string
	MessageID            string
	ResponseID           string
	AuthorName           string            `json:",omitzero"`
	Role                 message.Role      `json:",omitzero"`
	ContinuationToken    any               `json:",omitzero"`
	CreatedAt            time.Time         `json:",omitzero"`
	Contents             []message.Content `json:",omitzero"`
}

// String returns the concatenated text contents of the response messages.
func (r *RunResponseUpdate) String() string {
	var sb strings.Builder
	for _, c := range r.Contents {
		if textContent, ok := c.(*message.TextContent); ok {
			sb.WriteString(textContent.Text)
		}
	}
	return sb.String()
}

func (r *RunResponseUpdate) UserInputRequests() iter.Seq[message.Content] {
	return func(yield func(message.Content) bool) {
		for _, c := range r.Contents {
			switch c := c.(type) {
			case *message.FunctionApprovalRequestContent:
				if !yield(c) {
					return
				}
			}
		}
	}
}
