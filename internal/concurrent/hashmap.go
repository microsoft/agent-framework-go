// Copyright (c) Microsoft. All rights reserved.

package concurrent

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
type HashMap[K, V any] struct {
	entries Map[uint64, entry[K, V]]
	h       Hasher[K]
}

// NewMap returns a new mapping.
func NewHashMap[K, V any](h Hasher[K]) *HashMap[K, V] {
	return &HashMap[K, V]{
		h: h,
	}
}

// All returns an iterator over the key/value entries of the map in undefined order.
func (m *HashMap[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for entry := range m.entries.Values() {
			if !yield(entry.key, entry.value) {
				return
			}
		}
	}
}

// Load returns the map entry for the given key.
func (m *HashMap[K, V]) Load(key K) (V, bool) {
	entry, ok := m.entries.Load(m.h.Hash(key))
	if !ok {
		var zero V
		return zero, false
	}
	return entry.value, ok
}

// Delete removes th//e entry with the given key, if any.
func (m *HashMap[K, V]) Delete(key K) {
	m.entries.Delete(m.h.Hash(key))
}

// Keys returns an iterator over the map keys in unspecified order.
func (m *HashMap[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		for entry := range m.entries.Values() {
			if !yield(entry.key) {
				return
			}
		}
	}
}

// Values returns an iterator over the map values in unspecified order.
func (m *HashMap[K, V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		for entry := range m.entries.Values() {
			if !yield(entry.value) {
				return
			}
		}
	}
}

// Swap updates the map entry for key to value, and returns the previous entry, if any.
func (m *HashMap[K, V]) Swap(key K, value V) (V, bool) {
	hash := m.h.Hash(key)
	entry, ok := m.entries.Swap(hash, entry[K, V]{key: key, value: value})
	if !ok {
		var zero V
		return zero, false
	}
	return entry.value, ok
}

func (m *HashMap[K, V]) Clear() {
	m.entries.Clear()
}

// String returns a string representation of the map's entries in unspecified order.
// Values are printed as if by fmt.Sprint.
func (m *HashMap[K, V]) String() string {
	s := "{"
	first := true
	for entry := range m.entries.Values() {
		if !first {
			s += " "
		}
		s += fmt.Sprintf("%v: %v", entry.key, entry.value)
		first = false
	}
	s += "}"
	return s
}
