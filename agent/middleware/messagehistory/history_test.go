// Copyright (c) Microsoft. All rights reserved.

package messagehistory_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/agent/middleware/messagehistory"
	"github.com/microsoft/agent-framework-go/message"
)

func TestInMemory_PersistsAndLoadsHistory(t *testing.T) {
	mw := messagehistory.New()
	session := memory.NewSession("")
	firstRequest := []*message.Message{message.NewText("hello")}

	var firstCaptured []*message.Message
	next := func(_ context.Context, messages []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		firstCaptured = messages
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "world"}},
			}, nil)
		}
	}

	for _, err := range mw.Run(next, t.Context(), firstRequest, agentopt.Session(session)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if len(firstCaptured) != 1 {
		t.Fatalf("expected 1 request message passed to next in first run, got %d", len(firstCaptured))
	}

	var secondCaptured []*message.Message
	secondNext := func(_ context.Context, messages []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		secondCaptured = messages
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant}, nil)
		}
	}

	for _, err := range mw.Run(secondNext, t.Context(), []*message.Message{message.NewText("current")}, agentopt.Session(session)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if len(secondCaptured) != 3 {
		t.Fatalf("expected previous run history (2) + current request (1), got %d", len(secondCaptured))
	}
	if secondCaptured[0].String() != "hello" || secondCaptured[1].String() != "world" || secondCaptured[2].String() != "current" {
		t.Fatalf("unexpected history order: got [%q, %q, %q]", secondCaptured[0].String(), secondCaptured[1].String(), secondCaptured[2].String())
	}
}

func TestInMemory_DoesNothingWithoutSession(t *testing.T) {
	mw := messagehistory.New()

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

func TestInMemory_PersistsIntermediateResultsOnError(t *testing.T) {
	mw := messagehistory.New()
	session := memory.NewSession("")
	request := []*message.Message{message.NewText("hello")}
	wantErr := errors.New("stream failed")

	next := func(_ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			if !yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "partial"}},
			}, nil) {
				return
			}
			yield(nil, wantErr)
		}
	}

	var gotErr error
	for _, err := range mw.Run(next, t.Context(), request, agentopt.Session(session)) {
		if err != nil {
			gotErr = err
		}
	}

	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("expected propagated error %v, got %v", wantErr, gotErr)
	}

	var replayCaptured []*message.Message
	replayNext := func(_ context.Context, messages []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		replayCaptured = messages
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Role: message.RoleAssistant}, nil)
		}
	}

	for _, err := range mw.Run(replayNext, t.Context(), []*message.Message{message.NewText("current")}, agentopt.Session(session)) {
		if err != nil {
			t.Fatalf("unexpected replay error: %v", err)
		}
	}

	if len(replayCaptured) != 3 {
		t.Fatalf("expected replay to include request + partial response + current message, got %d", len(replayCaptured))
	}
	if replayCaptured[0].String() != "hello" || replayCaptured[1].String() != "partial" || replayCaptured[2].String() != "current" {
		t.Fatalf("unexpected replay history order: got [%q, %q, %q]", replayCaptured[0].String(), replayCaptured[1].String(), replayCaptured[2].String())
	}
}
