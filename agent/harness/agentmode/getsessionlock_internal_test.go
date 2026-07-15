// Copyright (c) Microsoft. All rights reserved.

package agentmode

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
)

func sessionOption(s *agent.Session) []agent.Option {
	return []agent.Option{agent.WithSession(s)}
}

func countLocks(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// The same session must always resolve to the same lock; otherwise concurrent
// tool invocations on that session would not be mutually excluded.
func TestGetSessionLock_SameSessionSameLock(t *testing.T) {
	p := New(Config{})
	s := agenttest.CreateSession()

	first := p.getSessionLock(sessionOption(s))
	second := p.getSessionLock(sessionOption(s))
	if first != second {
		t.Fatalf("same session resolved to different locks: %p vs %p", first, second)
	}
}

// Distinct sessions with empty service IDs must not share a lock. Keying by
// service ID would collapse them onto a single "_default" entry.
func TestGetSessionLock_EmptyServiceIDsGetDistinctLocks(t *testing.T) {
	p := New(Config{})
	s1 := agenttest.CreateSession()
	s2 := agenttest.CreateSession()
	s1.SetServiceID("")
	s2.SetServiceID("")

	if l1, l2 := p.getSessionLock(sessionOption(s1)), p.getSessionLock(sessionOption(s2)); l1 == l2 {
		t.Fatalf("distinct sessions with empty service IDs shared a lock: %p", l1)
	}
}

// Distinct sessions that happen to share a service ID must not share a lock.
// Keying by service ID would incorrectly serialize unrelated sessions.
func TestGetSessionLock_SameServiceIDGetsDistinctLocks(t *testing.T) {
	p := New(Config{})
	s1 := agenttest.CreateSession()
	s2 := agenttest.CreateSession()
	s1.SetServiceID("shared-id")
	s2.SetServiceID("shared-id")

	if l1, l2 := p.getSessionLock(sessionOption(s1)), p.getSessionLock(sessionOption(s2)); l1 == l2 {
		t.Fatalf("distinct sessions sharing a service ID shared a lock: %p", l1)
	}
}

// A missing or nil session must resolve to the shared fallback lock.
func TestGetSessionLock_NilSessionUsesFallback(t *testing.T) {
	p := New(Config{})
	want := &p.nullSessionLock

	if got := p.getSessionLock(nil); got != want {
		t.Errorf("no session: got %p, want fallback %p", got, want)
	}
	if got := p.getSessionLock(sessionOption(nil)); got != want {
		t.Errorf("nil session: got %p, want fallback %p", got, want)
	}
}

// Registry entries for collected sessions must not accumulate indefinitely: the
// weak-key cleanup should drop them once the sessions are garbage collected.
func TestGetSessionLock_StaleEntriesDoNotAccumulate(t *testing.T) {
	p := New(Config{})

	const n = 200
	for i := 0; i < n; i++ {
		s := agenttest.CreateSession()
		_ = p.getSessionLock(sessionOption(s))
		// s is unreachable after this iteration, so its entry becomes eligible
		// for cleanup on the next GC.
	}

	// runtime.AddCleanup runs cleanups asynchronously after a GC, so poll with a
	// bounded deadline rather than assuming a single GC drains everything.
	var remaining int
	for i := 0; i < 100; i++ {
		runtime.GC()
		if remaining = countLocks(&p.sessionLocks); remaining == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if remaining != 0 {
		t.Fatalf("expected stale lock entries to be cleaned up, %d of %d remain", remaining, n)
	}
}
