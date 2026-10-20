# Loop history — attempt 001 (loop-mv9xg30q-jiq3zz)

**Result: PASS** (one finding found → fixed → live-verified closed; zero open findings)

## Scope
Multi-persona live QA of kern at HEAD `7de6ab86` (binary parity confirmed: installed
kern/kern-mcp both `7de6ab86`, doctor `parity` check [ok]). Working tree clean before
and after. No repo code changed → no affected-package test obligations beyond targeted
gates below (per test discipline: no whole-repo suite mid-iteration).

## Personas & probes (all live, black-box)

| Persona | Probe | Result |
|---|---|---|
| Developer | `go build ./...` (kern_verify types=build) | PASS |
| Developer | `go vet ./...` (kern_verify command mode) | PASS (exit 0) |
| Developer | `go test ./internal/architecture/ -short` (parity+cap gate) | PASS |
| Developer | `go test ./internal/setup/ -short -run 'TestShippedPluginHashRegistryCurrent\|TestPluginMatchesMCPCatalog\|TestPluginDefaultToolsMatchGoDefaultTools'` | PASS |
| Developer | `go test ./internal/mcp/ -short -run 'TestDefaultToolsMatchToolsurface\|TestMCPToolCatalogSizeCap\|TestIsDefault\|TestAnnotate'` | PASS |
| CLI user | `kern version`, `kern exitcode` | exit 0; doc table prints |
| CLI user | `kern search NewServer` (hit) / `kern search zzznomatchxyz` (miss) | exit 0 / exit 1 — matches exit contract |
| CLI user | `kern search` (no args), `kern definitelynotacommand` | both exit 2 (usage) — matches contract |
| CLI user | fresh dir, no index: `kern search foo` | exit 1 with actionable message: `kern: no kern index at . — run: kern index .` |
| Agent (MCP) | raw stdio probe: initialize + tools/list | default surface = 30 tools, KERN_MCP_FULL=1 = 117, no duplicate names, server `kern` |
| Agent (MCP) | session-surface kern tools exercised live (kern_verify ×5 incl. command-mode) | all responsive |
| Plugin host | sha256 of all 4 plugin copies (.opencode/plugins/kern.ts, internal/agentwire/assets/plugin/kern.ts, ~/.config/opencode/plugins/kern.ts, ~/.opencode/plugins/kern.ts) | all identical `2ea086de…` (= registered hash) |
| Operator | `kern doctor` (KERN_DOCTOR_EXEC_TIMEOUT_MS=30000) | exit 0, 4 warnings → 1 actionable |
| New user | cold-start journey: tiny repo → `kern index .` → `kern search Greeter` | index exit 0 (2 symbols), search exit 0, correct hit `main.go:3 Greeter` |

## Finding (1) — FIXED & VERIFIED
- **[warn] global rules** (doctor): kern managed block not canonical in
  `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.cursor/rules/kern.mdc`,
  `~/.gemini/GEMINI.md`, `~/.codeium/windsurf/memories/global_rules.md`; and absent at
  `~/.config/opencode/AGENTS.md`.
- **Fix**: `kern setup --global-rules` (binary = HEAD, so assets current) — exit 0,
  "global rules written" ×6.
- **Verify**: re-ran `kern doctor` — `global rules` now `[ok]` listing all six paths.
  Repo tree still clean (all project wiring files gitignored).

## Remaining doctor warnings — assessed NOT defects (environmental/optional, by design)
- `index-shadow`: parent index at `~/ai_workspace/kern_opensource/.kern`; serving the
  nested repo index is correct here (parent holds only the kern repo + review notes).
- `ollama` not reachable: optional; LLM calls fall back to local agent CLIs.
- `runtime` no Prometheus/OTel/K8s source: optional telemetry, off by default.

## Conclusion
Success criteria met on attempt 1 of 5: every probed surface green; the single
actionable finding closed and re-verified. No fixes required in repo code, so nothing
to commit (tree clean) and no install performed (per user directive).
