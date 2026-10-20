package jobs

import "example.com/shop/queue"

// Drain pushes every item through a queue and returns them in order.
func Drain(items []string) []string {
	q := queue.New()
	for _, it := range items {
		q.Push(it)
	}
	var out []string
	for s := q.Pop(); s != ""; s = q.Pop() {
		out = append(out, s)
	}
	return out
}
