// Copyright (c) Microsoft. All rights reserved.

package hashmap

import (
	"fmt"
	"iter"
)

type Hasher[T any] interface {
	Hash(T) uint64
	Equal(T, T) bool
}

type entry[K, V any] struct {
	key   K
	value V
}

// Map is a mapping from keys of type K to values of type V,
// using key-equivalence relation H.
type Map[K, V any] struct {
	entries map[uint64]entry[K, V]
	h       Hasher[K]
}

// NewMap returns a new mapping.
func NewMap[K, V any](h Hasher[K]) *Map[K, V] {
	return &Map[K, V]{
		entries: make(map[uint64]entry[K, V]),
		h:       h,
	}
}

// All returns an iterator over the key/value entries of the map in undefined order.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for _, entry := range m.entries {
			if !yield(entry.key, entry.value) {
				return
			}
		}
	}
}

// Load returns the map entry for the given key.
func (m *Map[K, V]) Load(key K) (V, bool) {
	entry, ok := m.entries[m.h.Hash(key)]
	return entry.value, ok
}

// Delete removes th//e entry with the given key, if any. It returns true if the entry was found.
func (m *Map[K, V]) Delete(key K) bool {
	hash := m.h.Hash(key)
	if _, exists := m.entries[hash]; exists {
		delete(m.entries, hash)
		return true
	}
	return false
}

// Keys returns an iterator over the map keys in unspecified order.
func (m *Map[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		for _, entry := range m.entries {
			if !yield(entry.key) {
				return
			}
		}
	}
}

// Values returns an iterator over the map values in unspecified order.
func (m *Map[K, V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		for _, entry := range m.entries {
			if !yield(entry.value) {
				return
			}
		}
	}
}

// Len returns the number of map entries.
func (m *Map[K, V]) Len() int {
	return len(m.entries)
}

// Set updates the map entry for key to value, and returns the previous entry, if any.
func (m *Map[K, V]) Set(key K, value V) (prev V) {
	hash := m.h.Hash(key)
	if entry, exists := m.entries[hash]; exists {
		prev = entry.value
	}
	m.entries[hash] = entry[K, V]{key, value}
	return prev
}

func (m *Map[K, V]) Clear() {
	clear(m.entries)
}

// String returns a string representation of the map's entries in unspecified order.
// Values are printed as if by fmt.Sprint.
func (m *Map[K, V]) String() string {
	s := "{"
	first := true
	for _, entry := range m.entries {
		if !first {
			s += " "
		}
		s += fmt.Sprintf("%v: %v", entry.key, entry.value)
		first = false
	}
	s += "}"
	return s
}
