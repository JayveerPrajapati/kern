# kern audit findings — todo tracker

Status legend: `[ ]` open · `[x]` done · `[~]` in progress · `[!]` blocked/needs discussion

## Batch 1 — initial audit

- [x] **1 · 🔴** kern_security MCP tool discards all findings — `.opencode/plugins/kern.ts:42-46`, `cmd/kern/main.go:1596`
- [x] **2 · 🔴** IP false positives (127.0.0.1, 10.x, 192.168.x flagged as secrets) — `internal/sec/sec.go:70-78`, `internal/pii/pii.go:38`
- [x] **3 · 🟡** Scanner flags its own test fixtures (`_test.go` not skipped) — `internal/sec/sec.go:127-149`
- [x] **4 · 🟡** kern dead/test_gaps miss method-call edges (receiver var vs type) — `internal/index/goast.go:184-198`, `internal/intel/dead.go:41`

## Batch 2 — deeper audit

- [x] **5 · 🟡** kern exec "isolated runtime" has no network/filesystem isolation — `internal/script/script.go:1-12`
- [x] **6 · 🟡** index.Watch silently swallows Build/Save errors — `internal/index/watch.go:116-124`
- [x] **7 · 🟡** sandbox.Restore partial failure leaves tree half-reverted — `internal/sandbox/sandbox.go:90-161`
- [x] **8 · 🟢** install.sh:71 no-op `[ "$VERSION" = "latest" ] && VERSION="latest"` — `install.sh:71`
- [x] **9 · 🟢** Unbounded stdin reads (no LimitReader, 14 sites) — `cmd/kern/main.go`
- [x] **10 · 🟢** README claims "CSRF protection" but Origin-only, empty Origin allowed — `internal/mcp/http.go:99-117`

## Batch 3 — deep review

- [x] **11 · 🔴** heal.failingFiles stats paths against CWD not project root — `internal/heal/heal.go:215`
- [x] **12 · 🔴** intel.Churn reports file count as commit count — `internal/intel/churn.go:79`
- [x] **13 · 🟡** DeleteCheck checks wrong char for export status on methods — `internal/intel/delete.go:55`
- [x] **14 · 🟡** lock.List reports stale holder PID for free locks — `internal/lock/lock_unix.go:91-99`
- [x] **15 · 🟡** Windows locks have no stale-lock cleanup — crash blocks scope forever — `internal/lock/lock_windows.go:22-42`
- [x] **16 · 🟡** parsePorcelain mishandles renames and quoted paths — `internal/intel/intel.go:58-73`
- [x] **17 · 🟡** diff.parseDiffOutput silently drops binary file changes — `internal/intel/diff.go:53-94`
- [x] **18 · 🟡** semcache.Store orphans payload files when entries evicted — `internal/semcache/semcache.go:195-196`
- [x] **19 · 🟡** sqlite_store.SearchFTS FTS5 query escaping fragile (AND/OR/NOT substring) — `internal/index/sqlite_store.go:443-446`
- [x] **20 · 🟡** foreign.stripLine shares one inTriple flag between `"""` and `'''` — `internal/index/foreign.go:486-507`
- [x] **21 · 🟡** kern_sandbox/kern_validate panic on whitespace-only command — `internal/mcp/server.go:1512,1617`
- [x] **22 · 🟡** kern_diff_files/kern_compact_file read arbitrary paths outside root — `internal/mcp/server.go:1546,1690`
- [x] **23 · 🟢** docComment keeps `/* */` markers in block comments — `internal/intel/why.go:89-91`
- [x] **24 · 🟢** flows.longestPath exponential blowup on cyclic graphs — `internal/intel/flows.go:104-137`
- [x] **25 · 🟢** stats.Summarize with days=0 returns all-time not today — `internal/stats/stats.go:143`
- [x] **26 · 🟢** index.computeCallers skips legitimate edges (simple name == caller full name) — `internal/index/engine.go:283`
- [x] **27 · 🟢** index.Save non-atomic JSON write (concurrent corruption risk) — `internal/index/engine.go:82`
- [x] **28 · 🟢** intel.RepoRegistry.Save non-atomic JSON write — `internal/intel/repos.go:60`
- [x] **29 · 🟢** kern_guard_check threshold=-1 rejects on zero violations — `internal/mcp/server.go:2439-2444`

## Batch 3 — pattern scan

- [x] **30 · 🟡** semcache.Clear desyncs in-memory state from on-disk on remove failure — `internal/semcache/semcache.go:308,314`
- [x] **31 · 🟢** HTTP server missing WriteTimeout/IdleTimeout (slowloris risk, loopback-mitigated) — `internal/mcp/http.go:55-58`
- [x] **32 · 🟢** precache.Watch goroutine leak — first send outside select can block forever — `internal/precache/precache.go:109-123`
- [x] **33 · ⚪** json.NewEncoder().Encode() return value ignored (security JSON path) — `internal/mcp/server.go:1286`
- [x] **34 · ⚪** json.Marshal/Unmarshal errors ignored in SQLite store load/save — `internal/index/sqlite_store.go:175,240-241,275,327,397-398`
- [x] **35 · ⚪** filepath.WalkDir walk errors silently swallowed (scan/invalidation stops) — `internal/sec/sec.go:129`, `internal/index/watch.go:25,57`
- [x] **36 · ⚪** strconv.Atoi/fmt.Sscanf parse errors silently ignored in flag parsing — `cmd/kern/main.go:264,337,355,380`, `server.go:1281`
- [x] **37 · ⚪** regexp.Compile error discarded — `internal/mcp/server.go:1913`

## New bugs (#38–#56)

- [x] **38 · 🔴** Temp-file race: concurrent tool calls clobber fixed temp filenames — `.opencode/plugins/kern.ts:17-19`
- [x] **39 · 🟡** Temp files leaked on every CLI error (no try/finally) — `.opencode/plugins/kern.ts` (9 tools)
- [x] **40 · 🟡** kern_pack pushes `--max-tokens` twice; max_tokens=0 = unlimited — `.opencode/plugins/kern.ts:115-124`
- [x] **41 · 🟡** kern_bridges plugin calls wrong CLI subcommand (hubs not bridges) — `.opencode/plugins/kern.ts:360`
- [x] **42 · 🟡** kern_semcache "similarity" action returns stats instead (CLI wants "sim") — `.opencode/plugins/kern.ts:192-196`
- [x] **43 · 🟡** commitmsg subject noun is "func" for every Go method declaration — `internal/commitmsg/commitmsg.go:267-280`
- [x] **44 · 🟡** optimize.Prompt cache key ignores FewShot, Mask, MaskNames — `internal/optimize/optimize.go:119`
- [x] **45 · 🟡** Python pip shim crashes on Windows (`os.uname()` doesn't exist) — `python/kern/_bootstrap.py:72`
- [x] **46 · 🟡** chat.message hook floods project memory, evicting real lessons — `.opencode/plugins/kern.ts:1018-1033`
- [x] **47 · 🟢** ignore.go doesn't translate `[!...]` negation to regex `[^...]` — `internal/ignore/ignore.go:169-178`
- [x] **48 · 🟢** doctor checkIndex builds index then discards it (wasteful + misleading) — `internal/doctor/doctor.go:129-131`
- [x] **49 · 🟢** RunBuild records inaccurate token stats (command prefix not counted) — `internal/optimize/optimize.go:257-259`
- [x] **50 · 🟢** setup.go mergeJSON never updates a stale kern entry (can't repair broken path) — `internal/setup/setup.go:332-334`
- [x] **51 · 🟢** pack.tree() renders full paths not basenames (not a real tree) — `internal/pack/pack.go:317-336`
- [x] **52 · 🟢** Python `_bootstrap.py` uses `tarfile.extractall` without filter (deprecation/path-traversal) — `python/kern/_bootstrap.py:91`
- [x] **53 · 🟢** commitmsg.parseDiff doesn't handle quoted paths (spaces in filenames) — `internal/commitmsg/commitmsg.go:132-136`
- [x] **54 · 🟢** rename cross-package suffix matching fragile (directory-name collisions) — `internal/rename/rename.go:99,176,443`
- [x] **55 · 🟢** tool.execute.after FAIL_RE false-positive on successful commands containing "error" — `.opencode/plugins/kern.ts:29,989-993`
- [x] **56 · 🟢** kern docs usage says `--k N` but the actual flag is `--limit` — `cmd/kern/main.go:1231`

## Agent-bias recommendations

- [x] R1 Ship a kern rules bundle per peer agent (CLAUDE.md/GEMINI.md/etc. derived from one AGENTS.md source) — `setup.go wireAgentRules`
- [x] R2 Reuse one "rules" source: root AGENTS.md instantiated per host file
- [x] R3 Ship equivalent compression hooks for Claude/Gemini/CodeBuddy (PostToolUse/AfterTool) or document interception as opencode-only
- [x] R4 Fix wrong README claim about "guidance automatically via initialize" (`README.md:440-441`) — state opencode auto-intercepts via plugin, all agents get same 61 MCP tools
- [x] R5 Make wirePlugin idempotent like mergeJSON (compare-and-write) so re-running setup doesn't torch user plugin edits
- [x] R6 Fix README Supported Agents table — give each agent a row or stop calling opencode "first-class" (`README.md:524-527`)

## Summary

| Severity | Count | Open |
|----------|-------|------|
| 🔴 High  | 5     | 0    |
| 🟡 Medium | 24    | 0    |
| 🟢 Low   | 22    | 0    |
| ⚪ Info  | 5     | 0    |
| Agent-bias (R) | 6 | 0    |
| **Total** | **62** | **0** |

All findings closed. Item notes:
- **1**: `kern sec`/`guard check`/`delete` payloads surface via `runPayload` even on non-zero exit.
- **9**: `readStdin()` caps piped input at 64 MiB (all 14 sites).
- **10**: README + http.go now describe an Origin allow-list, not "CSRF protection".
- **24**: `longestPath` breaks cycles with an on-path guard (+ test).
- **29**: `threshold=-1` on `guard_check`/`guard check` now means never reject.
- **31**: `WriteTimeout`/`IdleTimeout` 60s added (loopback server).
- **32**: precache.Watch sends are stop-aware (no goroutine leak).
- **47**: `[!...]`/`[^...]` glob classes translate to regex `[^...]` (+ test).
- **48**: doctor uses `index.HasIndexableSources` instead of building a throwaway index.
- **49**: RunBuild token stats count the `cmd:` prefix on both sides of the ledger.
- **50**: mergeJSON repairs stale kern entries; skips writes when unchanged (+ test).
- **51**: pack.tree renders basenames nested by depth, not full paths (+ test).
- **54**: rename resolves import paths to the longest matching package dir (+ test).
- **R1/R2**: rules block from the single AGENTS.md source is instantiated into existing CLAUDE.md/GEMINI.md.
- **R3**: README documents auto-interception as opencode-only.
- **R4**: removed the false "guidance via initialize" claim.
- **R5**: wirePlugin is compare-and-write; user edits are never overwritten.
- **R6**: dropped "first-class" wording; table now states opencode-only plugin.

## Independent re-verification (all items checked against source)

Every item above was re-verified against the working tree, not just the
checklist. This pass caught two classes of unchecked fixes, both now resolved:

- **Plugin asset drift (#1, #38-#42, #46, #55)**: the fixes existed only in the
  live `.opencode/plugins/kern.ts`; the embedded `internal/setup/assets/plugin/kern.ts`
  (what `kern setup` installs) still shipped the old code. Asset re-synced and a
  byte-equality drift guard added to `TestPluginMatchesMCPCatalog` so CI fails
  if the copies ever diverge again.
- **#56**: the usage banner said `--limit` but the fatal usage error still said
  `--k N`; fixed to `--limit N`.

`go build ./...`, `go vet ./...`, `go test ./...` all pass.
