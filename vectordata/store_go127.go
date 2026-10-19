// Copyright (c) Microsoft. All rights reserved.

//go:build go1.27

package vectordata

// Collection returns a typed handle without creating the collection.
// It is equivalent to [GetCollection] and requires Go 1.27 or later.
func (s *Store) Collection[TKey, TRecord any](name string, definition *Definition) (Collection[TKey, TRecord], error) {
	return GetCollection[TKey, TRecord](s, name, definition)
}
