// Copyright (c) Microsoft. All rights reserved.

package hashmap

import (
	"hash/maphash"
	"slices"
	"testing"
	"unicode"
	"unicode/utf8"

	internalmaphash "github.com/microsoft/agent-framework-go/internal/maphash"
)

// caseInsensitive is a string Hasher that ignores letter case.
type caseInsensitive struct{}

var _ internalmaphash.Hasher[string] = caseInsensitive{}

func (caseInsensitive) Hash(h *maphash.Hash, s string) {
	var buf [utf8.UTFMax]byte
	for _, r := range s {
		n := utf8.EncodeRune(buf[:], unicode.ToLower(r))
		h.Write(buf[:n])
	}
}

func (caseInsensitive) Equal(x, y string) bool {
	for len(x) > 0 && len(y) > 0 {
		xr, xSize := utf8.DecodeRuneInString(x)
		yr, ySize := utf8.DecodeRuneInString(y)
		if unicode.ToLower(xr) != unicode.ToLower(yr) {
			return false
		}
		x = x[xSize:]
		y = y[ySize:]
	}
	return len(x) == len(y)
}

func TestMap(t *testing.T) {
	m := NewMap[string, int](caseInsensitive{})

	if got := m.Len(); got != 0 {
		t.Errorf("Len() on empty Map: got %d, want 0", got)
	}
	if value, ok := m.Get("foo"); ok || value != 0 {
		t.Errorf("Get() on empty Map: got %v, %v, want 0, false", value, ok)
	}
	if _, removed := m.Delete("foo"); removed {
		t.Error("Delete() on empty Map: got true, want false")
	}

	if prev, changed := m.Set("Hello", 1); prev != 0 {
		t.Errorf("Set() on empty Map returned non-zero previous value %d", prev)
	} else if !changed {
		t.Error("Set() on empty Map returned changed=false")
	}
	if got := m.Len(); got != 1 {
		t.Errorf("Len(): got %d, want 1", got)
	}
	for _, key := range []string{"Hello", "hello", "HELLO"} {
		if value, ok := m.Get(key); !ok || value != 1 {
			t.Errorf("Get(%q): got %v, %v, want 1, true", key, value, ok)
		}
	}

	if prev, changed := m.Set("HELLO", 2); prev != 1 {
		t.Errorf("Set(\"HELLO\") previous value: got %d, want 1", prev)
	} else if changed {
		t.Error("Set(\"HELLO\") returned changed=true")
	}
	if got := m.Len(); got != 1 {
		t.Errorf("Len() after update: got %d, want 1", got)
	}
	if prev, changed := m.Set("World", 3); prev != 0 {
		t.Errorf("Set(\"World\") previous value: got %d, want 0", prev)
	} else if !changed {
		t.Error("Set(\"World\") returned changed=false")
	}

	if got, want := m.String(), "{Hello: 2, World: 3}"; got != want {
		t.Errorf("String(): got %s, want %s", got, want)
	}

	keys := slices.Collect(m.Keys())
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"Hello", "World"}) {
		t.Errorf("Keys(): got %v", keys)
	}

	values := slices.Collect(m.Values())
	slices.Sort(values)
	if !slices.Equal(values, []int{2, 3}) {
		t.Errorf("Values(): got %v", values)
	}

	entries := make(map[string]int)
	for key, value := range m.All() {
		entries[key] = value
	}
	if len(entries) != 2 || entries["Hello"] != 2 || entries["World"] != 3 {
		t.Errorf("All(): got %v", entries)
	}

	if prev, removed := m.Delete("hello"); !removed || prev != 2 {
		t.Errorf("Delete(\"hello\") = %v, %v, want 2, true", prev, removed)
	}
	if got := m.Len(); got != 1 {
		t.Errorf("Len(): got %d, want 1", got)
	}
	if _, ok := m.Get("Hello"); ok {
		t.Error("Get(\"Hello\") found entry after Delete")
	}

	m.Clear()
	if got := m.Len(); got != 0 {
		t.Errorf("Len() after Clear: got %d, want 0", got)
	}
	if _, ok := m.Get("World"); ok {
		t.Error("Get(\"World\") found entry after Clear")
	}
}

func TestNilMapPanics(t *testing.T) {
	panics := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("expected panic for nil Map.%s", name)
			}
		}()
		f()
	}

	var m *Map[string, int]
	panics("Len", func() { m.Len() })
	panics("All", func() { m.All() })
	panics("Keys", func() { m.Keys() })
	panics("Values", func() { m.Values() })
	panics("Get", func() { m.Get("key") })
	panics("Set", func() { m.Set("key", 1) })
	panics("Delete", func() { m.Delete("key") })
	panics("Clear", func() { m.Clear() })
	panics("String", func() { _ = m.String() })
}

type collisionHasher struct{}

func (collisionHasher) Hash(*maphash.Hash, string) {}

func (collisionHasher) Equal(x, y string) bool {
	return x == y
}

func TestMapHashCollisions(t *testing.T) {
	m := NewMap[string, int](collisionHasher{})
	m.Set("one", 1)
	m.Set("two", 2)

	if got := m.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	for key, want := range map[string]int{"one": 1, "two": 2} {
		if got, ok := m.Get(key); !ok || got != want {
			t.Errorf("Get(%q) = %d, %t, want %d, true", key, got, ok, want)
		}
	}

	if prev, changed := m.Set("one", 10); prev != 1 || changed {
		t.Errorf("Set(%q) returned %d, want 1", "one", prev)
	}
	if _, removed := m.Delete("one"); !removed {
		t.Fatal("Delete(\"one\") = false, want true")
	}
	if got, ok := m.Get("two"); !ok || got != 2 {
		t.Errorf("Get(%q) after colliding delete = %d, %t, want 2, true", "two", got, ok)
	}

	m.Set("three", 3)
	if got := m.Len(); got != 2 {
		t.Fatalf("Len() after reusing deleted slot = %d, want 2", got)
	}
	if got, ok := m.Get("three"); !ok || got != 3 {
		t.Errorf("Get(%q) = %d, %t, want 3, true", "three", got, ok)
	}
}

type sameStringKey int

func (sameStringKey) String() string { return "key" }

type sameStringKeyHasher struct{}

func (sameStringKeyHasher) Hash(*maphash.Hash, sameStringKey) {}

func (sameStringKeyHasher) Equal(x, y sameStringKey) bool { return x == y }

func TestMapStringFormattedKeyCollision(t *testing.T) {
	m := NewMap[sameStringKey, int](sameStringKeyHasher{})
	m.Set(1, 2)
	m.Set(2, 1)

	if got, want := m.String(), "{key: 1, key: 2}"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
