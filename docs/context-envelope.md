# Context Envelope — internal/domain, internal/context
The context envelope is the versioned, validated machine-readable wrapper
around every assembled context packet. It is what an agent receives when a
tool returns context: a typed, evidence-backed, schema-versioned document
rather than an opaque string. Everything here is deterministic — no LLM.

## Envelope versioning — `internal/domain/context_packet.go`
- `ContextPacket` (`context_packet.go:19`) carries `EnvelopeVersion` and
  `SchemaVersion` alongside the content: `Facts []Claim`, `Risks`,
  `Symbols`, `TokenCount`, `FittedText`, `Consistency`.
- `EnvelopeVersionV1 = 1` (const) — the only supported version. Unknown
  versions must be rejected before use.
- `Validate()` — nil error only for a well-formed packet:
  - `EnvelopeVersion == EnvelopeVersionV1` (0 is accepted as legacy/unset),
  - `SchemaVersion == "1.0.0"`,
  - every `Claim` has a non-empty `Statement`, a known `ClaimType`
    (FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION), and confidence in `[0,1]`.
- `Migrate()` — no-op for v1; a placeholder for future breaking versions
  (documented: a v2 would map old packets forward).

## Assembly — `internal/context/engine.go`, `internal/context/planner.go`
- `context.NewEngine(ix, mem, fw, ...)` + `AnalyzeChange(change)` assemble
  the packet from the index, engineering memory, and governance evidence.
- The planner (`context.PlanPacket(pkt, intent, budget)`,
  `internal/context/planner.go`) is the composition entry that stamps the
  envelope: `EnvelopeVersionV1` + `SchemaVersion "1.0.0"` — assemble through
  it (or through `app.Platform.Analyze`, `internal/app/platform.go:248`)
  when the result must be envelope-valid.
- Rendering: `context.RenderText(pkt)` (`internal/context/render.go:13`)
  produces the human-readable text; `budget.Fit(text, maxTokens)`
  (`internal/budget`) fits it to a token budget.

## Surfaces
- MCP `kern_context_envelope` (`internal/mcp/tools.go` L676-694,
  `handlers_envelope.go`): `change`/`root`/`max_tokens` → JSON of the
  assembled packet with `envelope_version` + `schema_version`. RiskLow,
  Phase "explore".
- CLI `kern context-envelope --change X --root . --max-tokens N`
  (`cmd/kern/cmd_context_envelope.go`) — MCP alias `kern_context_envelope`.
- MCP `kern_plan_context` / CLI `kern explain-context` — the planner's
  envelope-stamped plan for a change.

## JSON shape (trimmed)
```
{Task, Facts[{Type, Statement, Evidence[{Type, Source, Content,
Relationship, Digest, Timestamp}], Source, Provenance, Timestamp, Scope,
Confidence}], ..., TokenCount, FittedText, Consistency,
envelope_version: 1, schema_version: "1.0.0"}
```
Consumers MUST check `envelope_version` before trusting fields; unknown
versions are rejected (`Validate`), never guessed.