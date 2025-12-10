package concurrent

import (
	"sync"
	"testing"
)

func TestQueue_EnqueueDequeue(t *testing.T) {
	q := &Queue[int]{}

	if !q.IsEmpty() {
		t.Error("expected queue to be empty")
	}

	q.Enqueue(1)
	q.Enqueue(2)

	if q.IsEmpty() {
		t.Error("expected queue not to be empty")
	}

	val, ok := q.Dequeue()
	if !ok || val != 1 {
		t.Errorf("expected 1, got %v (ok=%v)", val, ok)
	}

	val, ok = q.Dequeue()
	if !ok || val != 2 {
		t.Errorf("expected 2, got %v (ok=%v)", val, ok)
	}

	val, ok = q.Dequeue()
	if ok {
		t.Errorf("expected empty queue, got %v", val)
	}

	if !q.IsEmpty() {
		t.Error("expected queue to be empty")
	}
}

func TestQueue_Concurrent(t *testing.T) {
	q := &Queue[int]{}
	count := 1000
	var wg sync.WaitGroup

	// Concurrent Enqueue
	wg.Add(count)
	for i := range count {
		go func() {
			defer wg.Done()
			q.Enqueue(i)
		}()
	}
	wg.Wait()

	if q.IsEmpty() {
		t.Error("expected queue not to be empty after concurrent enqueue")
	}

	// Concurrent Dequeue
	wg.Add(count)
	receivedCount := 0
	var mu sync.Mutex
	for range count {
		go func() {
			defer wg.Done()
			_, ok := q.Dequeue()
			if ok {
				mu.Lock()
				receivedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if receivedCount != count {
		t.Errorf("expected to dequeue %d items, got %d", count, receivedCount)
	}

	if !q.IsEmpty() {
		t.Error("expected queue to be empty after concurrent dequeue")
	}
}

func TestQueue_Len(t *testing.T) {
	q := &Queue[int]{}

	if q.Len() != 0 {
		t.Errorf("expected length 0, got %d", q.Len())
	}

	q.Enqueue(1)
	if q.Len() != 1 {
		t.Errorf("expected length 1, got %d", q.Len())
	}

	q.Enqueue(2)
	if q.Len() != 2 {
		t.Errorf("expected length 2, got %d", q.Len())
	}

	q.Dequeue()
	if q.Len() != 1 {
		t.Errorf("expected length 1, got %d", q.Len())
	}

	q.Dequeue()
	if q.Len() != 0 {
		t.Errorf("expected length 0, got %d", q.Len())
	}
}

func TestQueue_All(t *testing.T) {
	q := &Queue[int]{}

	// Test empty queue
	count := 0
	for range q.All() {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 items from empty queue, got %d", count)
	}

	// Test populated queue
	items := []int{1, 2, 3, 4, 5}
	for _, item := range items {
		q.Enqueue(item)
	}

	var got []int
	for item := range q.All() {
		got = append(got, item)
	}

	if len(got) != len(items) {
		t.Errorf("expected %d items, got %d", len(items), len(got))
	}

	for i, item := range got {
		if item != items[i] {
			t.Errorf("expected item %d at index %d, got %d", items[i], i, item)
		}
	}
}
