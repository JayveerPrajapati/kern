package domain

import "time"

// FreshnessState classifies how current a knowledge item is.
type FreshnessState string

const (
	FreshFresh   FreshnessState = "FRESH"   // recently observed, likely current
	FreshStale   FreshnessState = "STALE"   // old, may be outdated
	FreshUnknown FreshnessState = "UNKNOWN" // no timestamp, unknown freshness
)

// FreshnessRecord tracks the freshness of a knowledge item.
type FreshnessRecord struct {
	Subject       string         `json:"subject"`        // what this freshness applies to
	CreatedAt     time.Time      `json:"created_at"`     // when the item was created
	ObservedAt    time.Time      `json:"observed_at"`    // when the data was last observed
	SourceVersion string         `json:"source_version"` // git commit hash, API version, etc.
	State         FreshnessState `json:"state"`
}

// DefaultStalenessThreshold is the default time after which knowledge is
// considered stale (24 hours).
const DefaultStalenessThreshold = 24 * time.Hour
