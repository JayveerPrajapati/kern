// Memory retention policies.
//
// A RetentionPolicy controls the memory lifecycle:
//   - ExpireAfter auto-deletes memories older than the duration.
//   - ArchiveAfter retires memories older than the duration to the
//     "historical" state (kept for reference, no longer surfaced as current).
//   - MaxEntries caps the store size, dropping the oldest entries beyond it.
//
// Durations are configured as strings and accept a day suffix ("30d" = 720h)
// in addition to Go's standard duration syntax ("720h", "168h").

package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Duration is a time.Duration that marshals as a string and additionally
// accepts a day suffix ("30d") in JSON config.
type Duration time.Duration

// ParseDuration parses a Go duration string, additionally accepting a day
// suffix: "30d" = 720h, "1d" = 24h.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q", s)
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}
	return time.ParseDuration(s)
}

// UnmarshalJSON accepts "30d" or standard duration strings.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("memory: duration must be a string: %w", err)
	}
	parsed, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// MarshalJSON emits the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// String returns the duration in Go syntax.
func (d Duration) String() string { return time.Duration(d).String() }

// RetentionPolicy controls the memory lifecycle. The zero value (and
// DefaultRetention) never expires or archives; only the size cap applies,
// matching the store's legacy FIFO behavior.
type RetentionPolicy struct {
	// Enabled turns on expiry/archiving. The size cap (MaxEntries) always
	// applies, regardless of Enabled.
	Enabled bool `json:"enabled,omitempty"`
	// MaxEntries caps the number of memories kept. <= 0 means the default
	// (maxTypedEntries).
	MaxEntries int `json:"max_entries,omitempty"`
	// ExpireAfter auto-deletes memories older than this. 0 disables expiry.
	ExpireAfter Duration `json:"expire_after,omitempty"`
	// ArchiveAfter retires memories older than this to the historical state.
	// 0 disables archiving.
	ArchiveAfter Duration `json:"archive_after,omitempty"`
}

// DefaultRetention returns the default policy: no expiry/archiving, and the
// store's built-in size cap (maxTypedEntries). This preserves legacy behavior
// exactly.
func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{MaxEntries: maxTypedEntries}
}

// RetentionFromEnv loads a retention policy from KERN_MEMORY_RETENTION
// (inline JSON) or KERN_MEMORY_RETENTION_FILE (path). When neither is set it
// returns DefaultRetention. Malformed input is returned as an error.
func RetentionFromEnv() (RetentionPolicy, error) {
	if raw := os.Getenv("KERN_MEMORY_RETENTION"); raw != "" {
		p, err := parseRetentionJSON([]byte(raw))
		if err != nil {
			return RetentionPolicy{}, fmt.Errorf("memory: KERN_MEMORY_RETENTION: %w", err)
		}
		return p, nil
	}
	if path := os.Getenv("KERN_MEMORY_RETENTION_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return RetentionPolicy{}, fmt.Errorf("memory: KERN_MEMORY_RETENTION_FILE: %w", err)
		}
		p, err := parseRetentionJSON(b)
		if err != nil {
			return RetentionPolicy{}, fmt.Errorf("memory: retention file %s: %w", path, err)
		}
		return p, nil
	}
	return DefaultRetention(), nil
}

func parseRetentionJSON(b []byte) (RetentionPolicy, error) {
	var p RetentionPolicy
	if err := json.Unmarshal(b, &p); err != nil {
		return RetentionPolicy{}, err
	}
	return p, nil
}

// RetentionResult reports what a retention sweep did.
type RetentionResult struct {
	Kept     int `json:"kept"`
	Expired  int `json:"expired"`
	Archived int `json:"archived"`
	Trimmed  int `json:"trimmed"`
}

// Apply runs the policy over ms and returns the surviving memories plus a
// report of what happened. Expired memories are removed entirely; archived
// memories are marked MemoryHistorical; beyond MaxEntries the oldest are
// trimmed. Input order (oldest-first storage order) is preserved.
func (p RetentionPolicy) Apply(ms []domain.Memory, now time.Time) ([]domain.Memory, RetentionResult) {
	kept := make([]domain.Memory, 0, len(ms))
	var res RetentionResult

	maxEntries := p.MaxEntries
	if maxEntries <= 0 {
		maxEntries = maxTypedEntries
	}

	expireAfter := time.Duration(p.ExpireAfter)
	archiveAfter := time.Duration(p.ArchiveAfter)

	for _, m := range ms {
		if p.Enabled && expireAfter > 0 {
			if now.Sub(m.CreatedAt) > expireAfter {
				res.Expired++
				continue
			}
		}
		if p.Enabled && archiveAfter > 0 {
			if now.Sub(m.CreatedAt) > archiveAfter && m.Status != domain.MemoryHistorical {
				m.Status = domain.MemoryHistorical
				res.Archived++
			}
		}
		kept = append(kept, m)
	}

	if len(kept) > maxEntries {
		res.Trimmed = len(kept) - maxEntries
		kept = kept[len(kept)-maxEntries:]
	}
	res.Kept = len(kept)
	return kept, res
}

// ApplyRetention runs the store's attached retention policy and persists the
// result. Without governance attached it is a no-op returning an empty result.
// The sweep is recorded in the audit trail.
func (s *MemoryStore) ApplyRetention() (RetentionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gov := s.gov
	if gov == nil {
		return RetentionResult{}, nil
	}
	ms := s.load()
	kept, res := gov.Retention.Apply(ms, time.Now().UTC())
	if err := s.save(kept); err != nil {
		return res, err
	}
	s.recordAudit(AuditEvent{
		AgentID:   "retention",
		Operation: OpRetention,
		Allowed:   true,
		Reason: fmt.Sprintf("kept=%d expired=%d archived=%d trimmed=%d",
			res.Kept, res.Expired, res.Archived, res.Trimmed),
	})
	return res, nil
}
