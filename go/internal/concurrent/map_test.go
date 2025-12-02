package concurrent

import (
	"testing"
)

func TestMap_All(t *testing.T) {
	m := &Map[string, int]{}

	// Test empty map
	count := 0
	for range m.All() {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 items from empty map, got %d", count)
	}

	// Test populated map
	items := map[string]int{
		"one":   1,
		"two":   2,
		"three": 3,
	}
	for k, v := range items {
		m.Store(k, v)
	}

	got := make(map[string]int)
	for k, v := range m.All() {
		got[k] = v
	}

	if len(got) != len(items) {
		t.Errorf("expected %d items, got %d", len(items), len(got))
	}

	for k, v := range items {
		if gotVal, ok := got[k]; !ok || gotVal != v {
			t.Errorf("expected item %s: %d, got %d (ok=%v)", k, v, gotVal, ok)
		}
	}
}
