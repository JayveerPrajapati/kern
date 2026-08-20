package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/sec"
)

// This file contains adapter functions that convert v1 types into their 2.0
// domain equivalents. v1 types stay in their packages; these adapters are the
// bridge, and no v1 API is modified.

// isExported reports whether name is exported (starts with an uppercase
// letter). It mirrors Go's exported-identifier rule.
func isExported(name string) bool {
	for _, r := range name {
		return unicode.IsUpper(r) && unicode.IsLetter(r)
	}
	return false
}

// FromIndexSymbol promotes an index.Symbol into a domain.Symbol via a direct
// field mapping.
func FromIndexSymbol(s index.Symbol) Symbol {
	return Symbol{
		Name:      s.Name,
		Qualified: s.FullName(),
		Kind:      s.Kind,
		File:      s.File,
		Line:      s.Line,
		Language:  s.Lang,
		Signature: strings.Join(s.Params, ", "),
		Receiver:  s.Receiver,
		Exported:  isExported(s.Name),
	}
}

// FromMemoryLesson wraps a plain lesson content string into a Memory of type
// lesson.
func FromMemoryLesson(content string) Memory {
	return Memory{
		Type:      MemoryLesson,
		Content:   content,
		Source:    "human",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// digest returns a stable hex-encoded SHA-256 of the given content.
func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// FromSecFinding wraps a security finding as a FACT claim with a policy-type
// evidence entry. Security scans are deterministic static-analysis checks, so
// the claim confidence is 1.0.
func FromSecFinding(f sec.Finding) Claim {
	now := time.Now()
	evidence := Evidence{
		Type:      EvidencePolicy,
		Source:    "sec",
		Content:   f.Message,
		Digest:    digest(f.File + f.Message + f.Snippet),
		Timestamp: now,
	}
	return Claim{
		Type:       ClaimFact,
		Statement:  f.Message,
		Source:     "sec",
		Provenance: "sec:" + f.Rule,
		Timestamp:  now,
		Scope:      f.File,
		Confidence: 1.0,
		Evidence:   []Evidence{evidence},
	}
}

// FromGuardRule maps an architectural guard boundary rule into a Policy. A
// boundary rule forbids or allows a dependency edge between package/directory
// patterns, which is governance policy by nature.
func FromGuardRule(r intel.BoundaryRule) Policy {
	return Policy{
		Name:        "boundary:" + r.From + "->" + r.To,
		Description: "architectural boundary rule: " + r.Action + " dependency " + r.From + " -> " + r.To,
		Rule:        r.Action + " " + r.From + " -> " + r.To,
		Scope:       "all",
		Enabled:     true,
	}
}
