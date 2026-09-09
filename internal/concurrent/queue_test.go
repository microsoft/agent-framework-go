// Copyright (c) Microsoft. All rights reserved.

package concurrent

import (
	"sync"
	"testing"
	"time"
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

// TestQueue_DequeueZeroesVacatedSlot verifies that Dequeue clears the vacated
// head slot in the backing array so it no longer pins the dequeued reference.
// The queue keeps later elements, so the backing array stays alive; the test
// inspects the shared backing array directly to confirm the head slot was
// zeroed rather than left holding the dead reference until reallocation.
func TestQueue_DequeueZeroesVacatedSlot(t *testing.T) {
	q := &Queue[*int]{}

	head := new(int)
	*head = 42
	q.Enqueue(head)
	for i := range 4 {
		v := i
		q.Enqueue(&v)
	}

	// A full-capacity view over the same backing array lets the test observe
	// the physical head slot after it has been advanced past by Dequeue.
	backing := q.items[:cap(q.items)]
	if backing[0] != head {
		t.Fatalf("expected head at slot 0, got %v", backing[0])
	}

	got, ok := q.Dequeue()
	if !ok || got != head {
		t.Fatalf("expected to dequeue head, got %v (ok=%v)", got, ok)
	}

	if backing[0] != nil {
		t.Errorf("expected vacated head slot to be zeroed, still holds %v", backing[0])
	}
}

// TestQueue_DequeueDropsEmptyBackingArray verifies that draining the queue
// releases the backing array entirely so no residual references are retained.
func TestQueue_DequeueDropsEmptyBackingArray(t *testing.T) {
	q := &Queue[*int]{}

	head := new(int)
	q.Enqueue(head)
	q.Enqueue(new(int))

	if _, ok := q.Dequeue(); !ok {
		t.Fatal("expected first dequeue to succeed")
	}
	if _, ok := q.Dequeue(); !ok {
		t.Fatal("expected second dequeue to succeed")
	}

	if q.items != nil {
		t.Errorf("expected backing array to be released on empty, got %v", q.items)
	}
}

func TestQueue_AllSnapshotAllowsMutationInLoop(t *testing.T) {
	// All must iterate over a snapshot taken at the start, releasing the lock
	// before yielding. Mutating the same queue from the loop body must not
	// deadlock and must not affect the values already being iterated.
	q := &Queue[int]{}
	original := []int{1, 2, 3, 4, 5}
	for _, item := range original {
		q.Enqueue(item)
	}

	done := make(chan []int, 1)
	go func() {
		var got []int
		for v := range q.All() {
			got = append(got, v)
			// Reentrant mutation on the same queue: would deadlock if All
			// held the read lock across the yield callback.
			q.Enqueue(v * 10)
			q.Dequeue()
		}
		done <- got
	}()

	select {
	case got := <-done:
		if len(got) != len(original) {
			t.Fatalf("expected %d snapshot items, got %d (%v)", len(original), len(got), got)
		}
		for i, v := range got {
			if v != original[i] {
				t.Errorf("expected snapshot value %d at index %d, got %d", original[i], i, v)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Queue.All deadlocked when mutating the queue inside the iteration")
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
