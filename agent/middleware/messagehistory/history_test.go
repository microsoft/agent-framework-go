// Copyright (c) Microsoft. All rights reserved.

package messagehistory

import (
	"context"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/message"
)

type testSession struct {
	state memory.StateBag
}

func (s *testSession) GetStateBag() *memory.StateBag { return &s.state }

func TestInMemory_PersistsHistoryInSessionStateBag(t *testing.T) {
	mw := New()
	session := &testSession{}
	request := []*message.Message{message.NewText("hello")}

	var captured []*message.Message
	next := func(_ context.Context, messages []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		captured = messages
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "world"}},
			}, nil)
		}
	}

	for _, err := range mw.Run(next, t.Context(), request, agentopt.Session(session)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if len(captured) != 1 {
		t.Fatalf("expected 1 request message passed to next, got %d", len(captured))
	}
	var stored []*message.Message
	ok, err := session.GetStateBag().Get(stateBagKey, &stored)
	if err != nil {
		t.Fatalf("unexpected error reading state bag: %v", err)
	}
	if !ok {
		t.Fatal("expected history in session state bag")
	}
	if len(stored) != 2 {
		t.Fatalf("expected 2 stored messages (request + response), got %d", len(stored))
	}
}

func TestInMemory_LoadsHistoryFromSessionStateBag(t *testing.T) {
	mw := New()
	session := &testSession{}
	session.GetStateBag().Set(stateBagKey, []*message.Message{message.NewText("previous")})

	var captured []*message.Message
	next := func(_ context.Context, messages []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		captured = messages
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant}, nil)
		}
	}

	for _, err := range mw.Run(next, t.Context(), []*message.Message{message.NewText("current")}, agentopt.Session(session)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if len(captured) != 2 {
		t.Fatalf("expected previous + current messages, got %d", len(captured))
	}
	text, _ := captured[0].Contents[0].(*message.TextContent)
	if text == nil || text.Text != "previous" {
		t.Fatalf("expected first message to come from state bag history")
	}
}

func TestInMemory_DoesNothingWithoutSession(t *testing.T) {
	mw := New()

	var captured []*message.Message
	next := func(_ context.Context, messages []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		captured = messages
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant}, nil)
		}
	}

	for _, err := range mw.Run(next, t.Context(), []*message.Message{message.NewText("current")}) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if len(captured) != 1 {
		t.Fatalf("expected middleware to pass through only current message, got %d", len(captured))
	}
	text, _ := captured[0].Contents[0].(*message.TextContent)
	if text == nil || text.Text != "current" {
		t.Fatalf("expected pass-through message to be current input")
	}
}

var _ middleware.Middleware = (*inmemory)(nil)
