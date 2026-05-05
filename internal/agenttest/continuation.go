// Copyright (c) Microsoft. All rights reserved.

package agenttest

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const ContinuationTokenType = "agentContinuationToken"

type ContinuationToken struct {
	Type            string                  `json:"type"`
	InnerToken      string                  `json:"innerToken"`
	InputMessages   []*message.Message      `json:"inputMessages,omitempty"`
	ResponseUpdates []*agent.ResponseUpdate `json:"responseUpdates,omitempty"`
}

func NewContinuationToken(t testing.TB, innerToken string) string {
	t.Helper()
	return EncodeContinuationToken(t, ContinuationToken{
		Type:       ContinuationTokenType,
		InnerToken: innerToken,
	})
}

func DecodeContinuationToken(t testing.TB, token string) ContinuationToken {
	t.Helper()
	var out ContinuationToken
	if err := json.Unmarshal([]byte(token), &out); err != nil {
		t.Fatalf("failed to unmarshal continuation token: %v", err)
	}
	return out
}

func EncodeContinuationToken(t testing.TB, token ContinuationToken) string {
	t.Helper()
	data, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("failed to marshal continuation token: %v", err)
	}
	return string(data)
}
