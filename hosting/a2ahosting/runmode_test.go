// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"context"
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
)

func TestAgentRunMode_DisallowBackground_ReturnsFalse(t *testing.T) {
	ok, err := DisallowBackground().shouldRunInBackground(context.Background(), A2ARunDecisionContext{})
	if err != nil {
		t.Fatalf("shouldRunInBackground returned error: %v", err)
	}
	if ok {
		t.Fatal("expected false")
	}
}

func TestAgentRunMode_AllowBackgroundIfSupported_ReturnsTrue(t *testing.T) {
	ok, err := AllowBackgroundIfSupported().shouldRunInBackground(context.Background(), A2ARunDecisionContext{})
	if err != nil {
		t.Fatalf("shouldRunInBackground returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestAgentRunMode_AllowBackgroundWhen_UsesCallback(t *testing.T) {
	called := false
	mode := AllowBackgroundWhen(func(_ context.Context, decision A2ARunDecisionContext) (bool, error) {
		called = true
		if decision.MessageSendParams == nil || decision.MessageSendParams.Message == nil {
			t.Fatal("expected decision context message")
		}
		return true, nil
	})

	ok, err := mode.shouldRunInBackground(context.Background(), A2ARunDecisionContext{
		MessageSendParams: &a2a.MessageSendParams{Message: &a2a.Message{ID: "m1"}},
	})
	if err != nil {
		t.Fatalf("shouldRunInBackground returned error: %v", err)
	}
	if !called {
		t.Fatal("expected callback to be called")
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestAgentRunMode_AllowBackgroundWhen_NilCallback_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil callback")
		}
	}()
	_ = AllowBackgroundWhen(nil)
}
