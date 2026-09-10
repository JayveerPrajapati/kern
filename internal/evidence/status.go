package evidence

import (
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// ValidateClaim checks claim status-consistency rules. "" (unclassified) is
// always valid. Rules:
//   - ClaimStatusObserved: len(Evidence) >= 1 AND every Evidence entry has a
//     non-empty Digest (an observed claim must point at concrete local evidence)
//   - ClaimStatusVerifiedDerived: len(Evidence) >= 1
//   - ClaimStatusInferred: Confidence < ConfidenceCertain (1.0)
//   - ClaimStatusReported / ClaimStatusStale / "": no requirements
func ValidateClaim(c domain.Claim) error {
	switch c.Status {
	case domain.ClaimStatusObserved:
		if len(c.Evidence) == 0 {
			return fmt.Errorf("claim %q: status %s requires evidence", c.Statement, c.Status)
		}
		for _, e := range c.Evidence {
			if e.Digest == "" {
				return fmt.Errorf("claim %q: status %s requires evidence digest", c.Statement, c.Status)
			}
		}
	case domain.ClaimStatusVerifiedDerived:
		if len(c.Evidence) == 0 {
			return fmt.Errorf("claim %q: status %s requires evidence", c.Statement, c.Status)
		}
	case domain.ClaimStatusInferred:
		if c.Confidence >= ConfidenceCertain {
			return fmt.Errorf("claim %q: status %s requires confidence below %v", c.Statement, c.Status, ConfidenceCertain)
		}
	}
	return nil
}

// DownrankStale marks claims older than maxAge (Timestamp < now-maxAge) as
// stale and scales their Confidence by 0.5 with a floor of 0.2. Deterministic.
// Returns a NEW slice — the input slice and its claims are never mutated.
// Claims outside the horizon are copied through unchanged (status preserved).
func DownrankStale(claims []domain.Claim, maxAge time.Duration, now time.Time) []domain.Claim {
	out := make([]domain.Claim, len(claims))
	copy(out, claims)
	cutoff := now.Add(-maxAge)
	for i := range out {
		c := out[i]
		if c.Timestamp.Before(cutoff) {
			c.Status = domain.ClaimStatusStale
			c.Confidence *= 0.5
			if c.Confidence < 0.2 {
				c.Confidence = 0.2
			}
			out[i] = c
		}
	}
	return out
}
