// Package queue is a small FIFO queue, written for Remixware's tests of
// SCANOSS fingerprints. It isn't copied from anywhere.
package queue

import "errors"

// ErrEmpty is returned by Pop on an empty queue.
var ErrEmpty = errors.New("queue: empty")

// Queue holds items in the order they were pushed.
type Queue[T any] struct {
	items []T
	head  int
}

// Push adds an item at the back.
func (q *Queue[T]) Push(v T) {
	q.items = append(q.items, v)
}

// Pop removes and returns the item at the front.
func (q *Queue[T]) Pop() (T, error) {
	var zero T
	if q.head == len(q.items) {
		return zero, ErrEmpty
	}
	v := q.items[q.head]
	q.items[q.head] = zero
	q.head++
	if q.head > 32 && q.head*2 > len(q.items) {
		q.items = append([]T(nil), q.items[q.head:]...)
		q.head = 0
	}
	return v, nil
}

// Len reports how many items are waiting.
func (q *Queue[T]) Len() int { return len(q.items) - q.head }
