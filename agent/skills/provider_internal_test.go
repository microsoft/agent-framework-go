// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
)

// Cache waiters retry the build path after an unsuccessful owner; that retry
// must preserve the same panic-to-error behavior as the cache owner.
func TestProvider_CacheWaiterBuildPanicIsReturnedAsError(t *testing.T) {
	loading := make(chan struct{})
	close(loading)
	state := &providerState{
		sources: []Source{SourceFunc(func(context.Context) ([]*Skill, error) {
			panic("boom")
		})},
		logger:  slog.New(slog.DiscardHandler),
		loading: loading,
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("cache waiter allowed source panic to escape: %v", recovered)
		}
	}()
	_, _, err := state.provide(context.Background(), agent.InvokingContext{})
	if err == nil || !strings.Contains(err.Error(), "building skills context panicked: boom") {
		t.Fatalf("provider error = %v, want recovered source panic", err)
	}
}
