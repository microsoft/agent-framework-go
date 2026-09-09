// Copyright (c) Microsoft. All rights reserved.

// Package maphash provides temporary hash interfaces used by hash-based containers.
package maphash

import stdmaphash "hash/maphash"

// TODO: Replace Hasher with hash/maphash.Hasher once Go 1.27 is the minimum supported version.

// Hasher defines a hash function and equivalence relation over values of type T.
// If Equal(x, y) is true, Hash must write the same representation for x and y.
type Hasher[T any] interface {
	Hash(*stdmaphash.Hash, T)
	Equal(x, y T) bool
}
