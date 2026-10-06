package list

// clamp constrains n to the inclusive range [lo, hi].
func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// Items returns count items starting at offset start. BUG: it currently
// returns one element too many.
func Items(start, count int, xs []int) []int {
	lo := clamp(start, 0, len(xs))
	hi := clamp(start+count+1, 0, len(xs))
	return xs[lo:hi]
}
