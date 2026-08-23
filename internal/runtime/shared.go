package runtime

import (
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// SharedCorrelator is a single process-wide correlator shared by every
// consumer (incident, deployment, audit, learning) so they all reason over the
// same runtime source and lookback window. Injecting one instance (rather than
// letting each consumer build its own) keeps their correlations consistent.
type SharedCorrelator struct {
	corr *Correlator
}

// NewSharedCorrelator builds the shared correlator over src with the given
// lookback window. It is the single constructor the incident/deployment/audit/
// learning lanes should all receive via dependency injection.
func NewSharedCorrelator(src Source, window time.Duration) *SharedCorrelator {
	return &SharedCorrelator{corr: NewCorrelator(src, window)}
}

// Correlator exposes the underlying *Correlator so DI can hand the exact same
// instance to every consumer (see tests for the identity guarantee).
func (s *SharedCorrelator) Correlator() *Correlator { return s.corr }

// Correlate delegates to the shared instance.
func (s *SharedCorrelator) Correlate(a domain.Alert) Correlation { return s.corr.Correlate(a) }

// CorrelateChain delegates to the shared instance.
func (s *SharedCorrelator) CorrelateChain(a domain.Alert) CorrelationChain {
	return s.corr.CorrelateChain(a)
}

// package-level singleton accessor, so consumers that do not want to wire DI
// can still share one process-wide correlator.

var (
	defaultSharedOnce sync.Once
	defaultShared     *Correlator
)

// DefaultSharedCorrelator returns the process-wide shared *Correlator, building
// it once from src/window on the first call and reusing it thereafter. Every
// caller gets the same instance, so incident/deployment/audit/learning all
// observe the same correlations.
func DefaultSharedCorrelator(src Source, window time.Duration) *Correlator {
	defaultSharedOnce.Do(func() {
		defaultShared = NewCorrelator(src, window)
	})
	return defaultShared
}
