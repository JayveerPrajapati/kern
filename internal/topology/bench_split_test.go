package topology

import "testing"

// The benchmarks in this file moved from internal/intel/bench_test.go with
// the hubs family; they use the bench fixture copies in fixtures_test.go.

// BenchmarkGraphHubs measures hub ranking (caller count + score sort) on the
// same graph as intel's BenchmarkGraphQuery.
func BenchmarkGraphHubs(b *testing.B) {
	ix := buildBenchIndex(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if hubs := Hubs(ix, 10); len(hubs) == 0 {
			b.Fatal("expected hubs")
		}
	}
}

// BenchmarkGraphHubsScaled measures hub ranking over the scaled index: it
// scans every symbol, computes per-symbol caller counts and sorts by weight.
func BenchmarkGraphHubsScaled(b *testing.B) {
	ix := buildBenchIndexScaled(b)
	assertScaledScale(b, ix)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if hubs := Hubs(ix, 10); len(hubs) == 0 {
			b.Fatal("expected hubs")
		}
	}
}
