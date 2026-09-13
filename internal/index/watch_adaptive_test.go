package index

import (
	"context"
	"testing"
	"time"
)

// TestAdaptiveIntervalNoChangesPollsAtBase: with no changes the watcher
// stays at the base interval when it is already there.
func TestAdaptiveIntervalNoChangesPollsAtBase(t *testing.T) {
	base := 2 * time.Second
	if got := adaptiveInterval(base, 0, base); got != base {
		t.Errorf("0 changes at base interval: got %v, want %v", got, base)
	}
}

// TestAdaptiveIntervalDecayTowardBase: after a large change set backed the
// interval off, idle cycles halve it toward base and never go below base.
func TestAdaptiveIntervalDecayTowardBase(t *testing.T) {
	base := 2 * time.Second
	cases := []struct {
		last time.Duration
		want time.Duration
	}{
		{16 * time.Second, 8 * time.Second},
		{8 * time.Second, 4 * time.Second},
		{4 * time.Second, 2 * time.Second},
		{2 * time.Second, 2 * time.Second},
		{3 * time.Second, 2 * time.Second}, // half (1.5s) below base -> floor at base
		{1 * time.Second, 2 * time.Second}, // already below base -> base
	}
	for _, c := range cases {
		if got := adaptiveInterval(base, 0, c.last); got != c.want {
			t.Errorf("idle decay from %v: got %v, want %v", c.last, got, c.want)
		}
	}
}

// TestAdaptiveIntervalSmallChangesUseBase: small change sets (1..adaptSmall)
// poll at the base interval, even when the previous cycle had backed off.
func TestAdaptiveIntervalSmallChangesUseBase(t *testing.T) {
	base := 2 * time.Second
	for _, n := range []int{1, 5, 19, 20} {
		if got := adaptiveInterval(base, n, base); got != base {
			t.Errorf("%d changes: got %v, want %v", n, got, base)
		}
		if got := adaptiveInterval(base, n, 16*time.Second); got != base {
			t.Errorf("%d changes after back-off: got %v, want %v", n, got, base)
		}
	}
}

// TestAdaptiveIntervalLargeChangesBackOff: change sets above adaptSmall scale
// the interval by 1 + changes/adaptScalePerHundred, capped at adaptMaxScale.
func TestAdaptiveIntervalLargeChangesBackOff(t *testing.T) {
	base := 2 * time.Second
	cases := []struct {
		changes int
		want    time.Duration
	}{
		{21, 2 * time.Second},      // 21/100 = 0 -> scale 1
		{99, 2 * time.Second},      // still scale 1
		{100, 4 * time.Second},     // scale 1+1 = 2
		{250, 6 * time.Second},     // scale 1+2 = 3
		{300, 8 * time.Second},     // scale 1+3 = 4
		{700, 16 * time.Second},    // scale 1+7 = 8
		{1000, 16 * time.Second},   // scale 1+10 = 11 -> capped at adaptMaxScale 8
		{100000, 16 * time.Second}, // any larger set still capped at 8
	}
	for _, c := range cases {
		if got := adaptiveInterval(base, c.changes, base); got != c.want {
			t.Errorf("%d changes: got %v, want %v", c.changes, got, c.want)
		}
	}
}

// TestAdaptiveIntervalTransitionsDeterministic: a small-then-large-then-idle
// history produces a fully deterministic interval sequence (the same inputs
// always yield the same outputs — no time source, no randomness).
func TestAdaptiveIntervalTransitionsDeterministic(t *testing.T) {
	base := 2 * time.Second

	// Small edit: fast follow-up at base.
	wait := adaptiveInterval(base, 5, base)
	if wait != base {
		t.Fatalf("after small change: got %v, want %v", wait, base)
	}
	// Large change set: back off to base * (1 + 500/100) = 12s.
	wait = adaptiveInterval(base, 500, wait)
	if want := 12 * time.Second; wait != want {
		t.Fatalf("after 500 changes: got %v, want %v", wait, want)
	}
	// Idle cycles decay geometrically toward base: 12 -> 6 -> 3 -> 2 -> 2.
	wants := []time.Duration{6 * time.Second, 3 * time.Second, 2 * time.Second, 2 * time.Second}
	for _, want := range wants {
		wait = adaptiveInterval(base, 0, wait)
		if wait != want {
			t.Fatalf("idle decay: got %v, want %v", wait, want)
		}
	}
	// And the whole sequence replays identically from the same history.
	if got := adaptiveInterval(base, 5, base); got != base {
		t.Errorf("replay diverged: got %v, want %v", got, base)
	}
}

// TestWatchFiresOnChange is a light integration check that Watch detects a
// file addition and invokes onChange with the fresh index. The temp-dir tree
// is tiny, the poll interval is 10ms and the timeout is generous, so the test
// is deterministic in practice: the first poll after start sees the added
// file (an empty prev manifest) and must fire long before the timeout.
func TestWatchFiresOnChange(t *testing.T) {
	root := t.TempDir()
	writeFileAt(t, root, "sample.go", "package sample\n\nfunc Hello() string { return \"hi\" }\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fired := make(chan struct{}, 1)
	errc := make(chan error, 4)
	go func() {
		err := Watch(ctx, root, 10*time.Millisecond, func(changes []Change, ix *Index) {
			if len(changes) == 0 || ix == nil {
				return
			}
			select {
			case fired <- struct{}{}:
			default:
			}
		}, func(err error) { errc <- err })
		if err != nil && err != context.Canceled {
			errc <- err
		}
	}()

	select {
	case <-fired:
		// onChange fired with a non-empty change set and a fresh index.
	case err := <-errc:
		t.Fatalf("watch error before first onChange: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("no onChange within 10s")
	}
}
