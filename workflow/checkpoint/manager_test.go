// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"context"
	"fmt"
	"sync"
	"testing"

	icheckpoint "github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

func TestNewJSONManagerNilStorePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()

	_ = NewJSONManager(nil)
}

// The in-memory manager is keyed by session ID, so a single manager can be
// shared across concurrent workflow runs (distinct sessions). Its Store map and
// per-session caches must be synchronized: without a lock, concurrent Commit
// calls race on the map and can crash with "fatal: concurrent map writes".
// Run with -race.
func TestInMemoryManager_ConcurrentSessions_NoRace(t *testing.T) {
	mgr := &inMemoryManager{}
	ctx := context.Background()

	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("session-%d", i)
			if _, err := mgr.Commit(ctx, sid, &icheckpoint.Checkpoint{}); err != nil {
				t.Errorf("Commit(%s): %v", sid, err)
			}
			if _, err := mgr.RetrieveIndex(ctx, sid, nil); err != nil {
				t.Errorf("RetrieveIndex(%s): %v", sid, err)
			}
		}(i)
	}
	wg.Wait()
}
