// Copyright (c) Microsoft. All rights reserved.

// Package hashmap provides a hash table with custom hashing and key equivalence.
//
// Map is adapted from the proposed standard library container/hash.Map:
// https://go.dev/issue/69559.
//
// TODO: Replace this package with container/hash once it is available in the
// minimum Go version supported by this module.
package hashmap

import (
	"fmt"
	"hash/maphash"
	"iter"
	"slices"
	"strings"
	"sync"

	internalmaphash "github.com/microsoft/agent-framework-go/internal/maphash"
)

type entry[K, V any] struct {
	used  bool
	key   K
	value V
}

// Map is a mapping from keys of type K to values of type V,
// using the hash function and key-equivalence relation specified at construction.
// Map values must not be copied.
type Map[K, V any] struct {
	seed   maphash.Seed
	table  map[uint64][]entry[K, V]
	len    int
	hasher internalmaphash.Hasher[K]

	_ noCopy
}

// NewMap returns a new mapping.
func NewMap[K, V any](h internalmaphash.Hasher[K]) *Map[K, V] {
	return &Map[K, V]{
		seed:   maphash.MakeSeed(),
		table:  make(map[uint64][]entry[K, V]),
		hasher: h,
	}
}

func (m *Map[K, V]) hash(key K) uint64 {
	h := hashPool.Get().(*maphash.Hash)
	h.SetSeed(m.seed)
	m.hasher.Hash(h, key)
	sum := h.Sum64()
	hashPool.Put(h)
	return sum
}

var hashPool = sync.Pool{
	New: func() any { return new(maphash.Hash) },
}

// All returns an iterator over the key/value entries of the map in undefined order.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	_ = m.len
	return func(yield func(K, V) bool) {
		for hash := range m.table {
			for i := 0; i < len(m.table[hash]); i++ {
				entry := &m.table[hash][i]
				if entry.used && !yield(entry.key, entry.value) {
					return
				}
			}
		}
	}
}

// Get reports whether the map contains the specified key, and returns the
// corresponding value if found, or the zero value if not.
func (m *Map[K, V]) Get(key K) (V, bool) {
	for _, entry := range m.table[m.hash(key)] {
		if entry.used && m.hasher.Equal(key, entry.key) {
			return entry.value, true
		}
	}
	var zero V
	return zero, false
}

// Delete removes the entry with the given key, if present. It reports whether
// the map changed, and returns the previous value, if any.
func (m *Map[K, V]) Delete(key K) (V, bool) {
	bucket := m.table[m.hash(key)]
	for i, e := range bucket {
		if e.used && m.hasher.Equal(key, e.key) {
			prev := e.value
			bucket[i] = entry[K, V]{}
			m.len--
			return prev, true
		}
	}
	var zero V
	return zero, false
}

// Keys returns an iterator over the map keys in unspecified order.
func (m *Map[K, V]) Keys() iter.Seq[K] {
	_ = m.len
	return func(yield func(K) bool) {
		for hash := range m.table {
			for i := 0; i < len(m.table[hash]); i++ {
				entry := &m.table[hash][i]
				if entry.used && !yield(entry.key) {
					return
				}
			}
		}
	}
}

// Values returns an iterator over the map values in unspecified order.
func (m *Map[K, V]) Values() iter.Seq[V] {
	_ = m.len
	return func(yield func(V) bool) {
		for hash := range m.table {
			for i := 0; i < len(m.table[hash]); i++ {
				entry := &m.table[hash][i]
				if entry.used && !yield(entry.value) {
					return
				}
			}
		}
	}
}

// Len returns the number of map entries.
func (m *Map[K, V]) Len() int {
	return m.len
}

// Set updates the map entry for key to value and returns the previous entry,
// if any. It reports whether the map size increased.
func (m *Map[K, V]) Set(key K, value V) (prev V, changed bool) {
	hash := m.hash(key)
	bucket := m.table[hash]
	var hole *entry[K, V]
	for i := range bucket {
		entry := &bucket[i]
		if entry.used {
			if m.hasher.Equal(key, entry.key) {
				prev = entry.value
				entry.value = value
				return prev, false
			}
		} else if hole == nil {
			hole = entry
		}
	}
	if hole != nil {
		*hole = entry[K, V]{used: true, key: key, value: value}
	} else {
		m.table[hash] = append(bucket, entry[K, V]{used: true, key: key, value: value})
	}
	m.len++
	return prev, true
}

func (m *Map[K, V]) Clear() {
	clear(m.table)
	m.len = 0
}

// String returns a string representation of the map's entries
// in an unspecified but deterministic order.
//
// Keys and values are printed as if by fmt.Sprint.
func (m *Map[K, V]) String() string {
	type formattedEntry struct {
		key   string
		value string
	}

	var buf strings.Builder
	buf.WriteByte('{')
	sorted := make([]formattedEntry, 0, m.len)
	for _, bucket := range m.table {
		for _, entry := range bucket {
			if entry.used {
				sorted = append(sorted, formattedEntry{
					key:   fmt.Sprint(entry.key),
					value: fmt.Sprint(entry.value),
				})
			}
		}
	}
	slices.SortStableFunc(sorted, func(a, b formattedEntry) int {
		if result := strings.Compare(a.key, b.key); result != 0 {
			return result
		}
		return strings.Compare(a.value, b.value)
	})

	for i, entry := range sorted {
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

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
