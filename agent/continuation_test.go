// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func continuationPortFixture(t *testing.T) (string, []*message.Message, []*ResponseUpdate) {
	t.Helper()
	inputs := []*message.Message{message.NewText("Hello!"), message.NewText("How are you?")}
	updates := []*ResponseUpdate{
		{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "I'm fine, thank you."}}},
		{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "How can I assist you today?"}}},
	}
	token, err := wrapContinuationToken(string([]byte{1, 2, 3, 4, 5}), inputs, updates)
	if err != nil {
		t.Fatal(err)
	}
	return token, inputs, updates
}

func assertContinuationPortState(t *testing.T, state continuationTokenState, inputs []*message.Message, updates []*ResponseUpdate) {
	t.Helper()
	if state.InnerToken != string([]byte{1, 2, 3, 4, 5}) {
		t.Fatalf("inner token = %q, want original token bytes", state.InnerToken)
	}
	if len(state.InputMessages) != len(inputs) || len(state.ResponseUpdates) != len(updates) {
		t.Fatalf("message/update counts = %d/%d, want %d/%d", len(state.InputMessages), len(state.ResponseUpdates), len(inputs), len(updates))
	}
	for i, input := range inputs {
		got := state.InputMessages[i]
		if got.Role != input.Role || got.String() != input.String() {
			t.Errorf("input %d = (%q, %q), want (%q, %q)", i, got.Role, got.String(), input.Role, input.String())
		}
	}
	for i, update := range updates {
		got := state.ResponseUpdates[i]
		if got.Role != update.Role || got.String() != update.String() {
			t.Errorf("update %d = (%q, %q), want (%q, %q)", i, got.Role, got.String(), update.Role, update.String())
		}
	}
}

func TestContinuationToken_EncodedRoundtrip(t *testing.T) {
	token, inputs, updates := continuationPortFixture(t)
	state, err := parseContinuationToken(token)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := wrapContinuationToken(state.InnerToken, state.InputMessages, state.ResponseUpdates)
	if err != nil {
		t.Fatal(err)
	}
	if encoded != token {
		t.Fatalf("encoded token = %q, want %q", encoded, token)
	}
	assertContinuationPortState(t, state, inputs, updates)
}

func TestContinuationToken_JSONRoundtrip(t *testing.T) {
	token, inputs, updates := continuationPortFixture(t)
	data, err := json.Marshal(token)
	if err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	state, err := parseContinuationToken(restored)
	if err != nil {
		t.Fatal(err)
	}
	if restored != token {
		t.Fatalf("restored token = %q, want %q", restored, token)
	}
	assertContinuationPortState(t, state, inputs, updates)
}
