// Copyright (c) Microsoft. All rights reserved.

package concurrent

import (
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"

	"github.com/microsoft/agent-framework-go/internal/hashmap"
	internalmaphash "github.com/microsoft/agent-framework-go/internal/maphash"
)

type entry[K, V any] struct {
	key   K
	value V
}

type hashMapState[K, V any] struct {
	mu    sync.RWMutex
	inner *hashmap.Map[K, V]
}

// HashMap is a synchronized wrapper around [hashmap.Map].
type HashMap[K, V any] struct {
	state *hashMapState[K, V]
}

// NewHashMap returns a new map that uses the specified hash function and
// key-equivalence relation.
func NewHashMap[K, V any](hasher internalmaphash.Hasher[K]) *HashMap[K, V] {
	return &HashMap[K, V]{
		state: &hashMapState[K, V]{
			inner: hashmap.NewMap[K, V](hasher),
		},
	}
}

func (m *HashMap[K, V]) snapshot() []entry[K, V] {
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()

	entries := make([]entry[K, V], 0, m.state.inner.Len())
	for key, value := range m.state.inner.All() {
		entries = append(entries, entry[K, V]{key: key, value: value})
	}
	return entries
}

// All returns an iterator over the key/value entries of the map in undefined order.
func (m *HashMap[K, V]) All() iter.Seq2[K, V] {
	_ = m.state
	return func(yield func(K, V) bool) {
		for _, entry := range m.snapshot() {
			if !yield(entry.key, entry.value) {
				return
			}
		}
	}
}

// Get reports whether the map contains the specified key, and returns the
// corresponding value if found, or the zero value if not.
func (m *HashMap[K, V]) Get(key K) (V, bool) {
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	return m.state.inner.Get(key)
}

// Delete removes the entry with the given key, if present. It reports whether
// the map changed, and returns the previous value, if any.
func (m *HashMap[K, V]) Delete(key K) (V, bool) {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.inner.Delete(key)
}

// Keys returns an iterator over the map keys in unspecified order.
func (m *HashMap[K, V]) Keys() iter.Seq[K] {
	_ = m.state
	return func(yield func(K) bool) {
		for key := range m.All() {
			if !yield(key) {
				return
			}
		}
	}
}

// Values returns an iterator over the map values in unspecified order.
func (m *HashMap[K, V]) Values() iter.Seq[V] {
	_ = m.state
	return func(yield func(V) bool) {
		for _, value := range m.All() {
			if !yield(value) {
				return
			}
		}
	}
}

// Set updates the map entry for key to value and returns the previous entry,
// if any. It reports whether the map size increased.
func (m *HashMap[K, V]) Set(key K, value V) (prev V, changed bool) {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.inner.Set(key, value)
}

// LoadOrStore returns the existing value for key if present. Otherwise, it
// calls newValue to construct a value, stores it, and returns it. newValue is
// only called on a miss, so callers don't pay the cost of constructing a
// value when the key is already present. The loaded result is true if the
// value was loaded, false if stored. Unlike a separate Get followed by Set,
// this check-and-set is performed atomically under a single lock, so
// concurrent callers racing to initialize the same key never lose an update.
func (m *HashMap[K, V]) LoadOrStore(key K, newValue func() V) (actual V, loaded bool) {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if existing, ok := m.state.inner.Get(key); ok {
		return existing, true
	}
	value := newValue()
	m.state.inner.Set(key, value)
	return value, false
}

func (m *HashMap[K, V]) Clear() {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.inner.Clear()
}

// String returns a string representation of the map's entries in an
// unspecified but deterministic order. Keys and values are printed as if by
// fmt.Sprint.
func (m *HashMap[K, V]) String() string {
	type formattedEntry struct {
		key   string
		value string
	}

	entries := m.snapshot()
	formatted := make([]formattedEntry, 0, len(entries))
	for _, entry := range entries {
		formatted = append(formatted, formattedEntry{
			key:   fmt.Sprint(entry.key),
			value: fmt.Sprint(entry.value),
		})
	}
	slices.SortStableFunc(formatted, func(a, b formattedEntry) int {
		if result := strings.Compare(a.key, b.key); result != 0 {
			return result
		}
		return strings.Compare(a.value, b.value)
	})

	var buf strings.Builder
	buf.WriteByte('{')
	for i, entry := range formatted {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(entry.key)
		buf.WriteString(": ")
		buf.WriteString(entry.value)
	}
	buf.WriteByte('}')
	return buf.String()
}
