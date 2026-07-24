// Copyright (c) Microsoft. All rights reserved.

package concurrent

import (
	"iter"
	"sync"
)

type Queue[T any] struct {
	items []T
	mu    sync.RWMutex
}

func (q *Queue[T]) Enqueue(item T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, item)
}

func (q *Queue[T]) Dequeue() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}

	item := q.items[0]
	// Zero the vacated head slot so the backing array no longer pins the
	// dequeued value; drop the array entirely once the queue empties. This
	// matches slices.Delete semantics and lets dead references be collected.
	var zero T
	q.items[0] = zero
	q.items = q.items[1:]
	if len(q.items) == 0 {
		q.items = nil
	}
	return item, true
}

func (q *Queue[T]) IsEmpty() bool {
	return q.Len() == 0
}

func (q *Queue[T]) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.items)
}

// All returns an iterator over the items in the queue.
// The iteration order is first-to-last.
//
// All corresponds to a consistent snapshot of the Queue's contents at the
// time iteration starts.
func (q *Queue[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		q.mu.RLock()
		items := make([]T, len(q.items))
		copy(items, q.items)
		q.mu.RUnlock()

		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}
