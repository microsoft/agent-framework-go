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
	// entries maps each key hash to the bucket of entries sharing that hash.
	// Buckets disambiguate hash collisions via the Hasher's Equal method.
	entries map[uint64][]entry[K, V]
	// count is the total number of entries across all buckets, kept so Len is
	// O(1) rather than summing bucket lengths.
	count int
	h     Hasher[K]
}

// NewMap returns a new mapping.
func NewMap[K, V any](h Hasher[K]) *Map[K, V] {
	return &Map[K, V]{
		entries: make(map[uint64][]entry[K, V]),
		h:       h,
	}
}

// All returns an iterator over the key/value entries of the map in undefined order.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for _, bucket := range m.entries {
			for _, entry := range bucket {
				if !yield(entry.key, entry.value) {
					return
				}
			}
		}
	}
}

// Load returns the map entry for the given key.
func (m *Map[K, V]) Load(key K) (V, bool) {
	for _, entry := range m.entries[m.h.Hash(key)] {
		if m.h.Equal(entry.key, key) {
			return entry.value, true
		}
	}
	var zero V
	return zero, false
}

// Delete removes the entry with the given key, if any. It returns true if the entry was found.
func (m *Map[K, V]) Delete(key K) bool {
	hash := m.h.Hash(key)
	bucket := m.entries[hash]
	for i, e := range bucket {
		if !m.h.Equal(e.key, key) {
			continue
		}
		if len(bucket) == 1 {
			delete(m.entries, hash)
		} else {
			// Shift the tail down and clear the vacated slot so the removed
			// entry's key/value are not retained by the backing array.
			copy(bucket[i:], bucket[i+1:])
			bucket[len(bucket)-1] = entry[K, V]{}
			m.entries[hash] = bucket[:len(bucket)-1]
		}
		m.count--
		return true
	}
	return false
}

// Keys returns an iterator over the map keys in unspecified order.
func (m *Map[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		for _, bucket := range m.entries {
			for _, entry := range bucket {
				if !yield(entry.key) {
					return
				}
			}
		}
	}
}

// Values returns an iterator over the map values in unspecified order.
func (m *Map[K, V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		for _, bucket := range m.entries {
			for _, entry := range bucket {
				if !yield(entry.value) {
					return
				}
			}
		}
	}
}

// Len returns the number of map entries.
func (m *Map[K, V]) Len() int {
	return m.count
}

// Set updates the map entry for key to value, and returns the previous entry, if any.
func (m *Map[K, V]) Set(key K, value V) (prev V) {
	hash := m.h.Hash(key)
	bucket := m.entries[hash]
	for i, entry := range bucket {
		if m.h.Equal(entry.key, key) {
			prev = entry.value
			bucket[i].value = value
			return prev
		}
	}
	m.entries[hash] = append(bucket, entry[K, V]{key, value})
	m.count++
	return prev
}

func (m *Map[K, V]) Clear() {
	clear(m.entries)
	m.count = 0
}

// String returns a string representation of the map's entries in unspecified order.
// Values are printed as if by fmt.Sprint.
func (m *Map[K, V]) String() string {
	s := "{"
	first := true
	for _, bucket := range m.entries {
		for _, entry := range bucket {
			if !first {
				s += " "
			}
			s += fmt.Sprintf("%v: %v", entry.key, entry.value)
			first = false
		}
	}
	s += "}"
	return s
}
