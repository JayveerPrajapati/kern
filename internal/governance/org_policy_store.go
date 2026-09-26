package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// OrgRootEnv names the environment variable that configures the org root:
// the directory where org-level governance state lives (the org policy
// document today; org RBAC and org approvals in later governance stages).
const OrgRootEnv = "KERN_ORG_ROOT"

// OrgRoot resolves the configured org root: the KERN_ORG_ROOT value when
// set, "" when no org is configured. A "" org root means the classic
// per-project model — each project's own .kern store is authoritative and
// org governance is completely inactive. This resolver is the SINGLE
// org-root resolution mechanism: Stage 2 (org RBAC) and Stage 3 (org
// approvals) resolve the org root through it rather than re-reading the
// environment themselves.
func OrgRoot() string {
	return os.Getenv(OrgRootEnv)
}

// OrgPolicyPath returns the org policy document path at
// <org-root>/.kern/org-policy.json — next to the other governance stores
// (agents.json, approvals.json, rbac.json) under the standard "kern
// generated" gitignore section.
func OrgPolicyPath(root string) string {
	return filepath.Join(root, ".kern", "org-policy.json")
}

// orgPolicyVersion is the current org policy document schema version.
const orgPolicyVersion = 1

// OrgPolicy is the persisted org-level policy document. Version is the
// document schema version; Policies is the enforced rule set; Hash is the
// canonical content hash of Policies recorded at write time (the versioned
// fingerprint the drift-check API compares against); UpdatedAt is the write
// timestamp.
type OrgPolicy struct {
	Version   int             `json:"version"`
	Policies  []domain.Policy `json:"policies"`
	Hash      string          `json:"hash"`
	UpdatedAt time.Time       `json:"updated_at,omitempty"`
}

// PolicyHash returns the canonical content hash of a policy set: SHA-256 of
// the deterministic JSON encoding. domain.Policy is a fixed-field struct, so
// the encoding is byte-stable: two policy sets with identical content always
// produce the same hash, and any change to any field (or the ordering of
// distinct policies) changes it. This is the versioning primitive behind the
// org policy document and its drift checks.
func PolicyHash(policies []domain.Policy) string {
	if policies == nil {
		policies = []domain.Policy{}
	}
	data, err := json.MarshalIndent(policies, "", "  ")
	if err != nil {
		// domain.Policy is plain struct data; marshaling cannot fail.
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SaveOrgPolicy atomically persists the policy set to
// <org-root>/.kern/org-policy.json, recording the canonical content hash
// alongside it. It uses the same write discipline as the RBAC role store and
// the agent identity store: process-wide per-path lock + cross-process
// flock, then a unique temp file + atomic rename, owner-only (0o600). A
// failed write is surfaced (fail loud), never silently swallowed: an org
// policy that exists only in memory is exactly the "projects silently
// enforce defaults instead of the org policy" class of regression this store
// prevents. A nil set is persisted as an empty document. Returns the
// recorded content hash.
func SaveOrgPolicy(root string, policies []domain.Policy) (string, error) {
	path := OrgPolicyPath(root)
	fl, err := cache.LockFile(path)
	if err != nil {
		return "", fmt.Errorf("governance: lock org policy store: %w", err)
	}
	defer fl.Unlock()
	pl := cache.PathLock(path)
	pl.Lock()
	defer pl.Unlock()

	hash := PolicyHash(policies)
	doc := OrgPolicy{
		Version:   orgPolicyVersion,
		Policies:  policies,
		Hash:      hash,
		UpdatedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("governance: encode org policy: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("governance: create org policy store dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".org-policy-tmp-*")
	if err != nil {
		return "", fmt.Errorf("governance: create org policy store temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("governance: write org policy store temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("governance: close org policy store temp: %w", err)
	}
	// Owner-only (0o600): the org policy encodes the org's governance
	// posture; other local users must not read or modify it (mirrors the
	// rbac store's chmod).
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("governance: chmod org policy store: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("governance: rename org policy store: %w", err)
	}
	return hash, nil
}

// LoadOrgPolicy reads the org policy document from
// <org-root>/.kern/org-policy.json. A missing document (org root configured
// but no policy set yet) is not an error: ok=false with a nil error. A
// corrupt document FAILS CLOSED with an error instead of returning a partial
// policy set: a caller must never build a project firewall from a half-read
// org policy — the safe fallback is the caller's own default posture, never
// a truncated org rule set.
func LoadOrgPolicy(root string) (OrgPolicy, bool, error) {
	data, err := os.ReadFile(OrgPolicyPath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return OrgPolicy{}, false, nil
		}
		return OrgPolicy{}, false, fmt.Errorf("governance: read org policy store: %w", err)
	}
	if len(data) == 0 {
		// An empty file is treated as absent (same policy as the RBAC
		// store): an org that created the file but wrote nothing has no
		// policy yet.
		return OrgPolicy{}, false, nil
	}
	var doc OrgPolicy
	if err := json.Unmarshal(data, &doc); err != nil {
		return OrgPolicy{}, false, fmt.Errorf("governance: decode org policy store: %w", err)
	}
	return doc, true, nil
}

// DriftReport reports whether the policies applied to project firewalls
// match the org policy document on disk. OrgHash is the content hash of the
// document's current policies — what a project firewall built NOW would
// enforce. AppliedHash is the content hash of the applied policy set — what
// already-built project firewalls enforce. Drifted is true when the two
// differ.
type DriftReport struct {
	OrgHash     string `json:"org_hash"`
	AppliedHash string `json:"applied_hash"`
	Drifted     bool   `json:"drifted"`
}

// PolicyDrift is the versioned drift-check API: it compares the content hash
// of an applied policy set against the content hash of the org policy
// document on disk. It is the API enterprise mode uses to report whether the
// policies its project firewalls were built with still match the shared org
// policy (e.g. after an operator changed the file out-of-band while the
// server was running). A missing org policy document is an error — there is
// nothing to drift against.
func PolicyDrift(orgRoot string, applied []domain.Policy) (DriftReport, error) {
	doc, ok, err := LoadOrgPolicy(orgRoot)
	if err != nil {
		return DriftReport{}, err
	}
	if !ok {
		return DriftReport{}, fmt.Errorf("governance: no org policy at %s", OrgPolicyPath(orgRoot))
	}
	orgHash := PolicyHash(doc.Policies)
	appliedHash := PolicyHash(applied)
	return DriftReport{OrgHash: orgHash, AppliedHash: appliedHash, Drifted: orgHash != appliedHash}, nil
}
