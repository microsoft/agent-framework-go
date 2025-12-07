// Copyright (c) Microsoft. All rights reserved.

package concurrent

import (
	"iter"
	"sync"
)

type Map[K comparable, V any] struct {
	data sync.Map
}

// Load returns the value stored in the map for a key, or nil if no
// value is present.
// The ok result indicates whether value was found in the map.
func (m *Map[K, V]) Load(key K) (V, bool) {
	v, ok := m.data.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return v.(V), true
}

// Store sets the value for a key.
func (m *Map[K, V]) Store(key K, value V) {
	m.data.Store(key, value)
}

// Clear deletes all the entries, resulting in an empty Map.
func (m *Map[K, V]) Clear() {
	m.data.Clear()
}

// LoadOrStore returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool) {
	v, loaded := m.data.LoadOrStore(key, value)
	if !loaded {
		return value, false
	}
	return v.(V), true
}

// LoadAndDelete deletes the value for a key, returning the previous value if any.
// The loaded result reports whether the key was present.
func (m *Map[K, V]) LoadAndDelete(key K) (V, bool) {
	v, loaded := m.data.LoadAndDelete(key)
	if !loaded {
		var zero V
		return zero, false
	}
	return v.(V), true
}

// Delete deletes the value for a key.
// If the key is not in the map, Delete does nothing.
func (m *Map[K, V]) Delete(key K) {
	m.data.Delete(key)
}

// Swap swaps the value for a key and returns the previous value if any.
// The loaded result reports whether the key was present.
func (m *Map[K, V]) Swap(key K, value V) (V, bool) {
	v, loaded := m.data.Swap(key, value)
	if !loaded {
		var zero V
		return zero, false
	}
	return v.(V), true
}

// CompareAndSwap swaps the old and new values for key
// if the value stored in the map is equal to old.
// The old value must be of a comparable type.
func (m *Map[K, V]) CompareAndSwap(key K, old, new V) (swapped bool) {
	return m.data.CompareAndSwap(key, old, new)
}

// CompareAndDelete deletes the entry for key if its value is equal to old.
// The old value must be of a comparable type.
//
// If there is no current value for key in the map, CompareAndDelete
// returns false (even if the old value is the nil interface value).
func (m *Map[K, V]) CompareAndDelete(key K, old V) (deleted bool) {
	return m.data.CompareAndDelete(key, old)
}

func (m *Map[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		m.data.Range(func(key, value any) bool {
			return yield(key.(K))
		})
	}
}

func (m *Map[K, V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		m.data.Range(func(key, value any) bool {
			return yield(value.(V))
		})
	}
}

// All returns an iterator over the key-value pairs in the map.
// The iteration order is not specified and is not guaranteed to be the same from one iteration to the next.
//
// All does not necessarily correspond to any consistent snapshot of the Map's
// contents: no key will be visited more than once, but if the value for any key
// is stored or deleted concurrently, All may reflect any
// mapping for that key from any point during the All call.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m.data.Range(func(key, value any) bool {
			return yield(key.(K), value.(V))
		})
	}
}
