// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

type messageMerger struct {
	states        map[string]*responseMergeState
	stateOrder    []string
	danglingState responseMergeState
}

func newMessageMerger() *messageMerger {
	return &messageMerger{
		states: make(map[string]*responseMergeState),
	}
}

func (m *messageMerger) AddUpdate(update *agent.ResponseUpdate) {
	if update == nil {
		return
	}
	if update.ResponseID == "" {
		m.danglingState.addUpdate(update)
		return
	}
	state, ok := m.states[update.ResponseID]
	if !ok {
		state = &responseMergeState{responseID: update.ResponseID}
		m.states[update.ResponseID] = state
		m.stateOrder = append(m.stateOrder, update.ResponseID)
	}
	state.addUpdate(update)
}

func (m *messageMerger) ComputeMerged(primaryResponseID string, primaryAgentID string, primaryAgentName string) *agent.Response {
	var messages []*message.Message
	agentIDs := make(map[string]struct{})
	finishReasons := make(map[string]struct{})
	var additionalProperties map[string]any

	for _, responseID := range m.stateOrder {
		state := m.states[responseID]
		responses := state.computeResponses()
		merged := mergeResponseList(responses)
		if merged == nil {
			continue
		}
		if merged.AgentID != "" {
			agentIDs[merged.AgentID] = struct{}{}
		}
		if merged.FinishReason != "" {
			finishReasons[merged.FinishReason] = struct{}{}
		}
		additionalProperties = mergeProperties(additionalProperties, merged.AdditionalProperties)
		messages = append(messages, messagesWithCreatedAt(merged)...)
	}

	messages = append(messages, m.danglingState.computeFlattened()...)
	messages = cleanupMergedMessages(messages)

	response := &agent.Response{
		ID:                   primaryResponseID,
		CreatedAt:            time.Now(),
		Messages:             messages,
		AdditionalProperties: additionalProperties,
	}
	if primaryAgentID != "" {
		response.AgentID = primaryAgentID
	} else if primaryAgentName != "" {
		response.AgentID = primaryAgentName
	} else if len(agentIDs) == 1 {
		response.AgentID = slices.Collect(maps.Keys(agentIDs))[0]
	}
	if len(finishReasons) == 1 {
		response.FinishReason = slices.Collect(maps.Keys(finishReasons))[0]
	}
	return response
}

type responseMergeState struct {
	responseID        string
	messageStatesByID map[string]*messageMergeState
	messageStateOrder []*messageMergeState
	lastObservedState *messageMergeState
}

type messageMergeState struct {
	messageID      string
	identifierless bool
	updates        []*agent.ResponseUpdate
}

func (s *responseMergeState) addUpdate(update *agent.ResponseUpdate) {
	state := s.getOrCreateMessageState(update.MessageID)
	state.updates = append(state.updates, update)
	s.lastObservedState = state
}

func (s *responseMergeState) getOrCreateMessageState(messageID string) *messageMergeState {
	if messageID == "" {
		if s.lastObservedState != nil && s.lastObservedState.identifierless {
			return s.lastObservedState
		}
		state := &messageMergeState{identifierless: true}
		s.messageStateOrder = append(s.messageStateOrder, state)
		return state
	}
	if s.messageStatesByID == nil {
		s.messageStatesByID = make(map[string]*messageMergeState)
	}
	if existing, ok := s.messageStatesByID[messageID]; ok {
		return existing
	}
	state := &messageMergeState{messageID: messageID}
	s.messageStatesByID[messageID] = state
	s.messageStateOrder = append(s.messageStateOrder, state)
	return state
}

func (s *responseMergeState) computeResponses() []*agent.Response {
	responses := make([]*agent.Response, 0, len(s.messageStateOrder))
	for _, state := range s.messageStateOrder {
		responses = append(responses, responseFromUpdates(state.updates))
	}
	return responses
}

func (s *responseMergeState) computeFlattened() []*message.Message {
	var messages []*message.Message
	for _, response := range s.computeResponses() {
		messages = append(messages, response.Messages...)
	}
	return messages
}

func responseFromUpdates(updates []*agent.ResponseUpdate) *agent.Response {
	response := &agent.Response{}
	for _, update := range updates {
		response.Update(update)
	}
	response.Coalesce()
	return response
}

func mergeResponseList(responses []*agent.Response) *agent.Response {
	var current *agent.Response
	for _, incoming := range responses {
		if incoming == nil {
			continue
		}
		if current == nil {
			clone := *incoming
			clone.AdditionalProperties = maps.Clone(incoming.AdditionalProperties)
			clone.Messages = slices.Clone(incoming.Messages)
			current = &clone
			continue
		}
		current.AgentID = cmp.Or(incoming.AgentID, current.AgentID)
		current.AdditionalProperties = mergeProperties(current.AdditionalProperties, incoming.AdditionalProperties)
		if !incoming.CreatedAt.IsZero() && (current.CreatedAt.IsZero() || incoming.CreatedAt.After(current.CreatedAt)) {
			current.CreatedAt = incoming.CreatedAt
		}
		current.FinishReason = cmp.Or(incoming.FinishReason, current.FinishReason)
		current.Messages = append(current.Messages, incoming.Messages...)
		current.ID = cmp.Or(current.ID, incoming.ID)
	}
	return current
}

func messagesWithCreatedAt(response *agent.Response) []*message.Message {
	if response == nil {
		return nil
	}
	messages := make([]*message.Message, 0, len(response.Messages))
	for _, msg := range response.Messages {
		clone := msg.Clone()
		if clone != nil && clone.CreatedAt.IsZero() && !response.CreatedAt.IsZero() {
			clone.CreatedAt = response.CreatedAt
		}
		messages = append(messages, clone)
	}
	return messages
}

func mergeProperties(current, incoming map[string]any) map[string]any {
	if current == nil {
		return maps.Clone(incoming)
	}
	if incoming == nil {
		return current
	}
	merged := maps.Clone(current)
	maps.Copy(merged, incoming)
	return merged
}

func cleanupMergedMessages(messages []*message.Message) []*message.Message {
	keptMessages := messages[:0]
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		keptContents := msg.Contents[:0]
		for _, content := range msg.Contents {
			if text, ok := content.(*message.TextContent); ok && strings.TrimSpace(text.Text) == "" {
				continue
			}
			keptContents = append(keptContents, content)
		}
		clear(msg.Contents[len(keptContents):])
		msg.Contents = keptContents
		if len(msg.Contents) == 0 {
			continue
		}
		keptMessages = append(keptMessages, msg)
	}
	clear(messages[len(keptMessages):])
	return keptMessages
}
