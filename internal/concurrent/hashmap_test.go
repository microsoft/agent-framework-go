// Copyright (c) Microsoft. All rights reserved.

package concurrent

import (
	"hash/maphash"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type collisionHasher struct{}

func (collisionHasher) Hash(*maphash.Hash, string) {}

func (collisionHasher) Equal(x, y string) bool {
	return strings.EqualFold(x, y)
}

func TestHashMapCollisions(t *testing.T) {
	m := NewHashMap[string, int](collisionHasher{})

	if value, ok := m.Get("missing"); ok || value != 0 {
		t.Errorf("Get() on empty HashMap = %d, %t, want 0, false", value, ok)
	}
	if prev, changed := m.Set("two", 2); prev != 0 || !changed {
		t.Errorf("Set(\"two\") = %d, %t, want 0, true", prev, changed)
	}
	m.Set("one", 1)

	for key, want := range map[string]int{"one": 1, "two": 2} {
		if got, ok := m.Get(key); !ok || got != want {
			t.Errorf("Get(%q) = %d, %t, want %d, true", key, got, ok, want)
		}
	}
	if prev, changed := m.Set("ONE", 10); prev != 1 || changed {
		t.Errorf("Set(\"ONE\") = %d, %t, want 1, false", prev, changed)
	}
	if prev, removed := m.Delete("oNe"); prev != 10 || !removed {
		t.Errorf("Delete(\"oNe\") = %d, %t, want 10, true", prev, removed)
	}
	if got, ok := m.Get("two"); !ok || got != 2 {
		t.Errorf("Get(\"two\") after colliding delete = %d, %t, want 2, true", got, ok)
	}

	m.Set("three", 3)
	keys := slices.Collect(m.Keys())
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"three", "two"}) {
		t.Errorf("Keys() = %v, want [three two]", keys)
	}
	values := slices.Collect(m.Values())
	slices.Sort(values)
	if !slices.Equal(values, []int{2, 3}) {
		t.Errorf("Values() = %v, want [2 3]", values)
	}
	if got, want := m.String(), "{three: 3, two: 2}"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	m.Clear()
	if _, ok := m.Get("two"); ok {
		t.Error("Get(\"two\") found entry after Clear")
	}
}

func TestNilHashMapIteratorsPanicImmediately(t *testing.T) {
	var m *HashMap[string, int]
	for _, test := range []struct {
		name string
		call func()
	}{
		{name: "All", call: func() { m.All() }},
		{name: "Keys", call: func() { m.Keys() }},
		{name: "Values", call: func() { m.Values() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("call did not panic")
				}
			}()
			test.call()
		})
	}
}

func TestHashMapConcurrentCollisions(t *testing.T) {
	m := NewHashMap[string, int](collisionHasher{})
	keys := []string{"zero", "one", "two", "three", "four", "five", "six", "seven"}

	var wg sync.WaitGroup
	for value, key := range keys {
		wg.Go(func() {
			for range 100 {
				m.Set(key, value)
				if _, ok := m.Get(key); !ok {
					t.Errorf("Get(%q) did not find concurrently stored key", key)
				}
			}
		})
	}
	wg.Wait()

	for value, key := range keys {
		if got, ok := m.Get(key); !ok || got != value {
			t.Errorf("Get(%q) = %d, %t, want %d, true", key, got, ok, value)
		}
	}
}

func TestHashMapMutationDuringIteration(t *testing.T) {
	m := NewHashMap[string, int](collisionHasher{})
	m.Set("one", 1)
	m.Set("two", 2)

	for key := range m.Keys() {
		m.Set("three", 3)
		m.Delete(key)
	}
}

type reentrantStringer struct {
	m *HashMap[string, any]
}

func (s reentrantStringer) String() string {
	s.m.Set("updated", true)
	return "value"
}

func TestHashMapStringFormatsWithoutLock(t *testing.T) {
	m := NewHashMap[string, any](collisionHasher{})
	m.Set("key", reentrantStringer{m: m})

	done := make(chan struct{})
	go func() {
		_ = m.String()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("String deadlocked while formatting a value that mutated the map")
	}
}
