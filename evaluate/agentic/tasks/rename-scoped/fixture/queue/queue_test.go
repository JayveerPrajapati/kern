package queue

import "testing"

func TestFIFO(t *testing.T) {
	q := New()
	q.Push("a")
	q.Push("b")
	if q.Pop() != "a" || q.Pop() != "b" || q.Pop() != "" {
		t.Fatal("queue is not FIFO")
	}
}
