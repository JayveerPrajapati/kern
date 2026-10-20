# ADR-0013: The Governance Audit Chain Is Append-Only

## Status

Accepted

## Context

kern persists every governed decision (approvals, firewall verdicts,
exec-gate outcomes, audited bypasses) into a per-project hash-chained log
at `.kern/audit/chain.jsonl`: each record carries a keyed hash (HMAC with
a local secret, `~/.cache/kern/audit-chain.key`) over its own content AND
its predecessor's hash. `AuditLog.VerifyChain` replays the whole file and
fails at the first link whose hash does not match — the full-chain walk IS
the tamper-evidence.

The chain is never trimmed today. On an active project it grows without
bound (a live `kern doctor` report showed 13.5 MB after a few months of
heavy operation), and the doctor check `audit-chain-size`
(internal/doctor/doctor.go) reports the size on every run and warns past
50 MB. Periodic proposals ask for retention/rotation: compact the chain,
archive old records automatically, keep only the tail.

## Decision

The chain stays append-only, forever, and kern implements NO automatic
retention, rotation, compaction, or deletion. Rationale: any in-place
trimming breaks the hash linkage — a truncated chain cannot prove the
removed suffix was unmodified, which is precisely the guarantee the chain
exists to provide. The proportionate close for growth is:

1. **Visibility**: `kern doctor` reports the chain size every run and warns
   past 50 MB with the archive procedure inline.
2. **Documented policy**: the operator-facing procedure lives in
   [governance-troubleshooting.md](../governance-troubleshooting.md).
3. **Manual archival as an operator decision**: moving `chain.jsonl`
   aside (never letting kern do it) starts a fresh chain on the next
   append and preserves the archive as audit history; the archive can be
   re-verified with `kern audit --root <scratch>` under a scratch
   `.kern/audit/` directory.

## Consequences

- Chain files grow unboundedly on heavily-governed projects; the operator
  decides when size warrants archiving (disk usage, backup size, replay
  time in `kern audit`), not kern.
- Verification cost is O(chain length) — the same property that makes
  tamper-evidence possible makes replay linear. Accepted.
- No retention configuration exists by design: `retentionDays` /
  `maxSizeMB` knobs would re-introduce the silent-deletion path this ADR
  rejects.
- `kern audit repair` (re-chain from the first broken link) remains the
  only writer that rewrites existing records, and only after detected
  tampering.
