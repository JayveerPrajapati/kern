// Package edges builds process and runtime relationship edges for the
// knowledge graph. These connect existing entities that other subsystems
// compute relationships for but don't persist as graph edges: changed_by
// (git), affects (impact), caused (incident root cause), fixed_by (incident
// fix), violates (architecture), documented_by (docs), owns (ownership),
// related_to (semantic). All deterministic — no LLM.
package edges
