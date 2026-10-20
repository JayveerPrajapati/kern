package queue

// Queue is a FIFO of strings.
type Queue struct{ items []string }

// New returns an empty Queue.
func New() *Queue { return &Queue{} }

// Push appends s.
func (q *Queue) Push(s string) { q.items = append(q.items, s) }

// Pop removes and returns the oldest item, or "" when empty.
func (q *Queue) Pop() string {
	if len(q.items) == 0 {
		return ""
	}
	s := q.items[0]
	q.items = q.items[1:]
	return s
}
