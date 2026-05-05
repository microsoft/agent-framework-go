// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/microsoft/agent-framework-go/message"
)

const continuationTokenType = "agentContinuationToken"

var errInvalidContinuationToken = errors.New("continuation token is not a valid agent continuation token")

type continuationTokenState struct {
	Type            string             `json:"type"`
	InnerToken      string             `json:"innerToken"`
	InputMessages   []*message.Message `json:"inputMessages,omitempty"`
	ResponseUpdates []*ResponseUpdate  `json:"responseUpdates,omitempty"`
}

func parseContinuationToken(token string) (continuationTokenState, error) {
	if token == "" {
		return continuationTokenState{}, nil
	}
	var state continuationTokenState
	if err := json.Unmarshal([]byte(token), &state); err != nil || state.Type != continuationTokenType || state.InnerToken == "" {
		return continuationTokenState{}, errInvalidContinuationToken
	}
	return state, nil
}

func wrapContinuationToken(innerToken string, inputMessages []*message.Message, responseUpdates []*ResponseUpdate) (string, error) {
	if innerToken == "" {
		return "", nil
	}
	state := continuationTokenState{
		Type:            continuationTokenType,
		InnerToken:      innerToken,
		InputMessages:   cloneMessages(inputMessages),
		ResponseUpdates: cloneResponseUpdates(responseUpdates),
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func inputMessagesForContinuation(inputMessages []*message.Message, state continuationTokenState) []*message.Message {
	if len(inputMessages) > 0 {
		return inputMessages
	}
	return state.InputMessages
}

func cloneMessages(messages []*message.Message) []*message.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]*message.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.Clone())
	}
	return out
}

func cloneResponseUpdate(update *ResponseUpdate) *ResponseUpdate {
	if update == nil {
		return nil
	}
	out := *update
	out.Contents = slices.Clone(update.Contents)
	return &out
}

func cloneResponseUpdates(updates []*ResponseUpdate) []*ResponseUpdate {
	if len(updates) == 0 {
		return nil
	}
	out := make([]*ResponseUpdate, 0, len(updates))
	for _, update := range updates {
		out = append(out, cloneResponseUpdate(update))
	}
	return out
}

func responseFromUpdates(updates []*ResponseUpdate) Response {
	var resp Response
	for _, update := range updates {
		resp.Update(update)
	}
	resp.Coalesce()
	return resp
}
