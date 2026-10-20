package project

import (
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// IndexBuildingError is returned by IndexWithin when the index is still being
// built after the wait budget. The build continues in the background, so a
// retry shortly afterwards succeeds without starting over.
type IndexBuildingError struct {
	Root    string
	Elapsed time.Duration
}

func (e *IndexBuildingError) Error() string {
	return fmt.Sprintf("kern is still building the index for %s (%.0fs so far; it keeps running in the background) — retry in a few seconds", e.Root, e.Elapsed.Seconds())
}

// IndexWithin is Index with a wait budget. A fresh cached index returns
// immediately. Otherwise the build/refresh runs on in the background and, if
// it has not finished within d, the call returns the stale snapshot when one
// exists or an *IndexBuildingError when none does — instead of blocking the
// caller (and risking a host-side tool timeout) for the full build, which can
// take tens of seconds on a large repo. d <= 0 means wait indefinitely.
func (s *Session) IndexWithin(d time.Duration) (*index.Index, error) {
	if d <= 0 {
		return s.Index()
	}
	s.mu.Lock()
	if s.ix != nil && !s.stale && time.Now().Before(s.staleUntil) {
		ix := s.ix
		s.mu.Unlock()
		return ix, nil
	}
	s.mu.Unlock()

	type result struct {
		ix  *index.Index
		err error
	}
	done := make(chan result, 1)
	go func() {
		ix, err := s.Index()
		done <- result{ix, err}
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.ix, r.err
	case <-timer.C:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ix != nil {
		return s.ix, nil
	}
	elapsed := d
	if s.rebuilding && !s.buildStarted.IsZero() {
		elapsed = time.Since(s.buildStarted)
	}
	return nil, &IndexBuildingError{Root: s.Root, Elapsed: elapsed}
}
