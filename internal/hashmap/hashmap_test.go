// Copyright (c) Microsoft. All rights reserved.

package hashmap_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/internal/hashmap"
)

// collidingHasher forces every key to the same hash so distinct keys land in the
// same bucket, exercising the hash-collision path. Equal still distinguishes keys.
type collidingHasher struct{}

func (collidingHasher) Hash(string) uint64     { return 1 }
func (collidingHasher) Equal(a, b string) bool { return a == b }

func TestMap_DistinctKeysWithCollidingHashCoexist(t *testing.T) {
	m := hashmap.NewMap[string, string](collidingHasher{})
	m.Set("alice", "a")
	m.Set("bob", "b")

	if got, ok := m.Load("alice"); !ok || got != "a" {
		t.Errorf(`Load("alice") = (%q, %v), want ("a", true)`, got, ok)
	}
	if got, ok := m.Load("bob"); !ok || got != "b" {
		t.Errorf(`Load("bob") = (%q, %v), want ("b", true)`, got, ok)
	}
	if got := m.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}

	// A missing key that collides with stored keys must not resolve to a stored value.
	if got, ok := m.Load("carol"); ok {
		t.Errorf(`Load("carol") = (%q, %v), want ("", false)`, got, ok)
	}

	// Deleting one colliding key must not remove the other.
	if !m.Delete("alice") {
		t.Error(`Delete("alice") = false, want true`)
	}
	if _, ok := m.Load("alice"); ok {
		t.Error(`Load("alice") after delete still found, want not found`)
	}
	if got, ok := m.Load("bob"); !ok || got != "b" {
		t.Errorf(`Load("bob") after deleting alice = (%q, %v), want ("b", true)`, got, ok)
	}
}
