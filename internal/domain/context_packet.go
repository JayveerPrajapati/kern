package domain

import "time"

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
}
