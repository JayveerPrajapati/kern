package domain

import (
	"fmt"
	"time"
)

// EnvelopeVersionV1 is the current context envelope version. Packets at or
// below this version are understood by every consumer; higher versions are
// rejected by Validate until a Migrate path lands.
const EnvelopeVersionV1 = 1

// ContextPacket is the assembled context for a task, combining graph + memory
// + evidence + architecture + git + risk into one structured response. It
// reuses only already-shipped domain types (Claim, File, Symbol, Edge, Policy,
// Memory, Evidence, Risk).
type ContextPacket struct {
	Task               string
	Facts              []Claim
	Files              []File
	Symbols            []Symbol
	Dependencies       []Edge
	ArchitectureRules  []Policy
	Memory             []Memory
	Incidents          []Memory // incident-type memories
	RuntimeEvidence    []Evidence
	Risks              []Risk
	RequiredValidation []string // what verification is needed
	Intent             Intent   // parsed intent; zero-value when not analyzed
	GeneratedAt        time.Time
	TokenCount         int    // measured token count of the packet
	FittedText         string // budget-fitted rendered text (empty when no budgeting applied)
	// Consistency is the cross-engine consistency report for the packet's
	// claims. It is nil when no conflicts/staleness were detected —
	// a nil report means the packet may be treated as internally consistent.
	Consistency *ConsistencyReport
	// EnvelopeVersion is the context envelope version. Zero means the packet
	// predates versioning and is treated as V1 by Validate.
	EnvelopeVersion int `json:"envelope_version,omitempty"`
	// SchemaVersion is the human-readable schema identifier for the envelope
	// (e.g. "1.0.0"). It is set only when the emitter provides one.
	SchemaVersion string `json:"schema_version,omitempty"`
}

// Validate reports whether the packet's envelope version is supported. A zero
// EnvelopeVersion is treated as V1 (pre-versioning packets).
func (p *ContextPacket) Validate() error {
	if p.EnvelopeVersion > EnvelopeVersionV1 {
		return fmt.Errorf("unsupported envelope version %d (latest: %d)", p.EnvelopeVersion, EnvelopeVersionV1)
	}
	return nil
}

// Migrate upgrades the packet to the latest envelope version. V1 is current:
// it returns nil without mutation.
func (p *ContextPacket) Migrate() error {
	return nil
}
