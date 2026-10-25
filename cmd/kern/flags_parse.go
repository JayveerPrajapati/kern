package main

// flags_parse.go holds the parseFlags execution machinery extracted from
// flags.go: the flagParser struct (loop state + the setters that were
// parseFlags' closures) and two per-family case tables (parseCoreFlag and
// parseMigratedFlag). The split follows the file's own structural marker —
// the `// ---- Migrated FlagSet flags (unified parser) ----` comment in
// flags.go — so every case body is moved verbatim, preserving the reviewed
// semantics (accept-and-default trailing values, --flag=value + --flag value
// forms, bool --flag/--flag=true/--flag=false, sticky first-error parsing,
// single-dash long-flag normalization, positionals before/after flags).

import (
	"fmt"
	"strconv"
	"time"
)

// flagParser carries the parseFlags loop state: the flags being filled, the
// positional rest, and the sticky first parse error. The setters below are
// the parseFlags closures, moved verbatim onto the struct so the per-family
// case tables can live in their own methods.
type flagParser struct {
	f        flags
	rest     []string
	parseErr error
}

// setInt parses an integer value into dst, recording a sticky error on
// failure. First error wins; later values are not evaluated.
func (p *flagParser) setInt(dst *int, val, flag string) {
	if p.parseErr != nil {
		return
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		p.parseErr = fmt.Errorf("%s: invalid integer %q", flag, val)
		return
	}
	*dst = n
}

// take advances the parser past the current token and returns the next
// one; ok=false at end of args (the documented accept-and-default
// semantic: a trailing value-less flag is left at its default).
func (p *flagParser) take(i *int, args []string) (string, bool) {
	*i++
	if *i < len(args) {
		return args[*i], true
	}
	return "", false
}

// setStr consumes the value of a string flag: the inline --flag=value
// form when present, else the next argument (accept-and-default when
// none).
func (p *flagParser) setStr(i *int, args []string, dst *string, inline string, hasInline bool) {
	if hasInline {
		*dst = inline
		return
	}
	if v, ok := p.take(i, args); ok {
		*dst = v
	}
}

// setIntFlag consumes the value of an int flag through setInt (error
// sticky via parseErr).
func (p *flagParser) setIntFlag(i *int, args []string, dst *int, name, inline string, hasInline bool) {
	if hasInline {
		p.setInt(dst, inline, name)
		return
	}
	if v, ok := p.take(i, args); ok {
		p.setInt(dst, v, name)
	}
}

// setTimeout consumes the --timeout value in its dual form: an integer is
// seconds (the historical contract read by toolTimeout), a Go duration
// string (30m, 500ms) is kept verbatim in timeoutDur for commands that
// need sub-second or minute-scale bounds (kern approval wait). Either
// form sets timeoutSet so commands can distinguish "given" from default;
// a value that is neither is a sticky parse error.
func (p *flagParser) setTimeout(i *int, args []string, val, name string) {
	if p.parseErr != nil {
		return
	}
	if _, derr := time.ParseDuration(val); derr == nil {
		p.f.timeoutDur = val
		p.f.timeoutSet = true
		return
	}
	if _, ierr := strconv.Atoi(val); ierr == nil {
		p.setInt(&p.f.timeout, val, name)
		p.f.timeoutSet = true
		return
	}
	p.parseErr = fmt.Errorf("%s: invalid timeout %q (want integer seconds or a duration like 30m)", name, val)
}

// setBool sets a bool flag: --flag, --flag=true or --flag=false (the
// stdlib flag package's bool forms, migrated verbatim).
func (p *flagParser) setBool(dst *bool, name, inline string, hasInline bool) {
	if hasInline {
		b, err := strconv.ParseBool(inline)
		if err != nil {
			p.parseErr = fmt.Errorf("%s: invalid boolean value %q", name, inline)
			return
		}
		*dst = b
	} else {
		*dst = true
	}
}

// parseCoreFlag handles the core flag family — every case in flags.go's
// parseFlags switch BEFORE the `// ---- Migrated FlagSet flags` marker. It
// reports whether the flag was handled; unhandled names fall through to
// the migrated family and then to the help/unknown handling in parseFlags.
func (p *flagParser) parseCoreFlag(name, inline string, hasInline bool, i *int, args []string) bool {
	switch name {
	case "--attach":
		p.setStr(i, args, &p.f.attach, inline, hasInline)
	case "--session":
		p.setStr(i, args, &p.f.session, inline, hasInline)
	case "--model":
		p.setStr(i, args, &p.f.model, inline, hasInline)
	case "--days":
		p.setIntFlag(i, args, &p.f.days, "--days", inline, hasInline)
	case "--dir":
		p.setStr(i, args, &p.f.dir, inline, hasInline)
	case "--llm":
		p.setStr(i, args, &p.f.llm, inline, hasInline)
	case "--json":
		p.setBool(&p.f.json, "--json", inline, hasInline)
		p.f.jsonSet = true
	case "--status":
		// Dual-form flag: bool (index: `kern index --status`) AND string
		// (flight list: `kern flight list --status ok|--status=ok`). The
		// next token is never consumed, so `kern index --status <root>`
		// keeps its positional root; flight list falls back to the first
		// positional when no inline value was given.
		// Quirk (documented, gate-8): an inline bool form like
		// `--status=false` sets the bool AND records "false" as the
		// string filter; the old parser rejected inline forms
		// entirely, so this is a new accepted form, not a regression.
		p.f.status = true
		if hasInline {
			p.f.statusFilter = inline
		}
	case "--strict":
		p.setBool(&p.f.strict, "--strict", inline, hasInline)
	case "--update":
		p.setBool(&p.f.update, "--update", inline, hasInline)
	case "--force":
		p.setBool(&p.f.force, "--force", inline, hasInline)
	case "--pin":
		p.setStr(i, args, &p.f.pin, inline, hasInline)
	case "--preflight":
		p.setStr(i, args, &p.f.preflight, inline, hasInline)
		p.f.preflightSet = true
	case "--yes":
		p.setBool(&p.f.yes, "--yes", inline, hasInline)
	case "--terse-code", "-terse-code":
		p.setBool(&p.f.terseCode, "--terse-code", inline, hasInline)
	case "--reset":
		p.setBool(&p.f.reset, "--reset", inline, hasInline)
	case "--net":
		p.setBool(&p.f.net, "--net", inline, hasInline)
	case "--sarif":
		p.setBool(&p.f.sarif, "--sarif", inline, hasInline)
	case "--threshold":
		p.setIntFlag(i, args, &p.f.threshold, "--threshold", inline, hasInline)
	case "--commits":
		p.setIntFlag(i, args, &p.f.commits, "--commits", inline, hasInline)
	case "--thresholds":
		p.setStr(i, args, &p.f.thresholds, inline, hasInline)
	case "--csv":
		p.setBool(&p.f.csv, "--csv", inline, hasInline)
	case "--by-tool":
		p.setBool(&p.f.byTool, "--by-tool", inline, hasInline)
	case "--by-agent":
		p.setBool(&p.f.byAgent, "--by-agent", inline, hasInline)
	case "--by-root":
		p.setBool(&p.f.byRoot, "--by-root", inline, hasInline)
	case "--bpe":
		p.setBool(&p.f.bpe, "--bpe", inline, hasInline)
	case "--root":
		p.setStr(i, args, &p.f.root, inline, hasInline)
	case "--addr":
		p.setStr(i, args, &p.f.addr, inline, hasInline)
	case "--enterprise":
		p.setBool(&p.f.enterprise, "--enterprise", inline, hasInline)
	case "--project":
		if hasInline {
			p.f.projects = append(p.f.projects, inline)
		} else if v, ok := p.take(i, args); ok {
			p.f.projects = append(p.f.projects, v)
		}
	case "--pattern":
		p.setStr(i, args, &p.f.pattern, inline, hasInline)
	case "--query":
		p.setStr(i, args, &p.f.query, inline, hasInline)
	case "--symbol":
		p.setStr(i, args, &p.f.symbol, inline, hasInline)
	case "--change":
		p.setStr(i, args, &p.f.change, inline, hasInline)
	case "--fresh":
		p.setBool(&p.f.fresh, "--fresh", inline, hasInline)
	case "--level":
		p.setStr(i, args, &p.f.level, inline, hasInline)
	case "--task-type":
		p.setStr(i, args, &p.f.taskType, inline, hasInline)
	case "--lens":
		p.setStr(i, args, &p.f.lens, inline, hasInline)
	case "--profile":
		p.setStr(i, args, &p.f.profile, inline, hasInline)
	case "--eval":
		p.setStr(i, args, &p.f.evalDir, inline, hasInline)
	case "--skill":
		p.setStr(i, args, &p.f.skillDir, inline, hasInline)
	case "--check":
		p.setBool(&p.f.check, "--check", inline, hasInline)
	case "--ci":
		p.setBool(&p.f.ci, "--ci", inline, hasInline)
	case "--arch-drift":
		p.setBool(&p.f.archDrift, "--arch-drift", inline, hasInline)
	case "--calibration":
		p.setBool(&p.f.calibration, "--calibration", inline, hasInline)
	case "--correlate":
		p.setBool(&p.f.correlate, "--correlate", inline, hasInline)
	case "--runbook":
		p.setStr(i, args, &p.f.runbook, inline, hasInline)
	case "--list-playbooks":
		p.setBool(&p.f.listPlaybooks, "--list-playbooks", inline, hasInline)
	case "--code":
		p.setBool(&p.f.code, "--code", inline, hasInline)
	case "--verify":
		p.setBool(&p.f.verify, "--verify", inline, hasInline)
	case "--verify-pipeline":
		p.setBool(&p.f.verifyPipeline, "--verify-pipeline", inline, hasInline)
	case "--verify-silent":
		p.setBool(&p.f.verifySilent, "--verify-silent", inline, hasInline)
	case "--verify-token-reduction":
		p.setBool(&p.f.verifyTokenReduction, "--verify-token-reduction", inline, hasInline)
	case "--scan":
		p.setStr(i, args, &p.f.scanPath, inline, hasInline)
	case "--types":
		p.setStr(i, args, &p.f.types, inline, hasInline)
	case "--short":
		p.setBool(&p.f.short, "--short", inline, hasInline)
	case "--changed":
		p.setBool(&p.f.changed, "--changed", inline, hasInline)
	case "--changed-since":
		p.setStr(i, args, &p.f.changedSince, inline, hasInline)
	case "--fast":
		p.setBool(&p.f.fast, "--fast", inline, hasInline)
	case "--cve":
		p.setBool(&p.f.cve, "--cve", inline, hasInline)
	case "--license":
		p.setBool(&p.f.license, "--license", inline, hasInline)
	case "--secrets":
		p.setBool(&p.f.secrets, "--secrets", inline, hasInline)
	case "--detect":
		p.setBool(&p.f.detect, "--detect", inline, hasInline)
	case "--all-agents":
		p.setBool(&p.f.allAgents, "--all-agents", inline, hasInline)
	case "--plain":
		p.setBool(&p.f.plain, "--plain", inline, hasInline)
	case "--global-only":
		p.setBool(&p.f.globalOnly, "--global-only", inline, hasInline)
	case "--remove":
		p.setBool(&p.f.remove, "--remove", inline, hasInline)
	case "--global":
		p.setBool(&p.f.global, "--global", inline, hasInline)
	case "--index-only":
		p.setBool(&p.f.indexOnly, "--index-only", inline, hasInline)
	case "--global-rules":
		p.setBool(&p.f.globalRules, "--global-rules", inline, hasInline)
	case "--agents-md":
		p.setStr(i, args, &p.f.agentsMD, inline, hasInline)
	case "--apply":
		p.setBool(&p.f.apply, "--apply", inline, hasInline)
	case "--runtime":
		p.setBool(&p.f.runtime, "--runtime", inline, hasInline)
	case "--agents":
		p.setStr(i, args, &p.f.agents, inline, hasInline)
	case "--file":
		// Single-value readers use .file (last value); policy-dsl's
		// repeatable form accumulates every value in .files.
		if hasInline {
			p.f.file = inline
			p.f.files = append(p.f.files, inline)
		} else if v, ok := p.take(i, args); ok {
			p.f.file = v
			p.f.files = append(p.f.files, v)
		}
	case "--task":
		p.setStr(i, args, &p.f.task, inline, hasInline)
	case "--budget":
		if hasInline {
			p.setInt(&p.f.budget, inline, "--budget")
			p.f.budgetSet = true
		} else if v, ok := p.take(i, args); ok {
			p.setInt(&p.f.budget, v, "--budget")
			p.f.budgetSet = true
		}
	case "--uninstall":
		p.setBool(&p.f.hostUninstall, "--uninstall", inline, hasInline)
	case "--agent-id":
		p.setStr(i, args, &p.f.agentID, inline, hasInline)
	case "--mermaid":
		p.setBool(&p.f.mermaid, "--mermaid", inline, hasInline)
	case "--repos":
		p.setBool(&p.f.repos, "--repos", inline, hasInline)
	case "--mask":
		p.setBool(&p.f.mask, "--mask", inline, hasInline)
	case "--names":
		p.setStr(i, args, &p.f.names, inline, hasInline)
	case "--schema":
		p.setStr(i, args, &p.f.schema, inline, hasInline)
	case "--cmd":
		p.setStr(i, args, &p.f.cmd, inline, hasInline)
	case "--timeout":
		// Dual-form: an integer is seconds (historical), a Go duration
		// string (30m, 500ms) is captured verbatim for commands that need
		// sub-second or minute-scale bounds (kern approval wait).
		if hasInline {
			p.setTimeout(i, args, inline, "--timeout")
		} else if v, ok := p.take(i, args); ok {
			p.setTimeout(i, args, v, "--timeout")
		}
	case "--wait":
		if hasInline {
			p.setInt(&p.f.wait, inline, "--wait")
		} else if v, ok := p.take(i, args); ok {
			p.setInt(&p.f.wait, v, "--wait")
		}
	case "--cache":
		p.setBool(&p.f.cache, "--cache", inline, hasInline)
	case "--fewshot":
		p.setBool(&p.f.fewshot, "--fewshot", inline, hasInline)
	case "--mode":
		p.setStr(i, args, &p.f.mode, inline, hasInline)
	case "--merge":
		p.setBool(&p.f.merge, "--merge", inline, hasInline)
	case "--schedule":
		p.setStr(i, args, &p.f.schedule, inline, hasInline)
	case "--with-skill":
		p.setStr(i, args, &p.f.withSkill, inline, hasInline)
	case "--kind":
		p.setStr(i, args, &p.f.kind, inline, hasInline)
	case "--etag":
		p.setStr(i, args, &p.f.etag, inline, hasInline)
	case "--one-line":
		p.setBool(&p.f.oneLine, "--one-line", inline, hasInline)
	case "--entities":
		p.setBool(&p.f.entities, "--entities", inline, hasInline)
	case "--risk":
		p.setBool(&p.f.risk, "--risk", inline, hasInline)
	case "--sinks":
		p.setStr(i, args, &p.f.sinks, inline, hasInline)
	case "--once":
		p.setBool(&p.f.once, "--once", inline, hasInline)
	case "--semantic":
		p.setBool(&p.f.semantic, "--semantic", inline, hasInline)
	case "--bridges-only":
		p.setBool(&p.f.bridgesOnly, "--bridges-only", inline, hasInline)
	case "--interval":
		p.setIntFlag(i, args, &p.f.interval, "--interval", inline, hasInline)
	case "--http":
		p.setStr(i, args, &p.f.http, inline, hasInline)
	case "--tls-cert":
		p.setStr(i, args, &p.f.tlsCert, inline, hasInline)
	case "--tls-key":
		p.setStr(i, args, &p.f.tlsKey, inline, hasInline)
	case "--hold":
		p.setBool(&p.f.hold, "--hold", inline, hasInline)
	case "--graphml":
		p.setBool(&p.f.graphml, "--graphml", inline, hasInline)
	case "--cypher":
		p.setBool(&p.f.cypher, "--cypher", inline, hasInline)
	case "--html":
		p.setBool(&p.f.html, "--html", inline, hasInline)
	case "--out":
		p.setStr(i, args, &p.f.out, inline, hasInline)
	case "--compact":
		p.setBool(&p.f.compact, "--compact", inline, hasInline)
	case "--all":
		p.setBool(&p.f.all, "--all", inline, hasInline)
	case "--clear":
		p.setBool(&p.f.clear, "--clear", inline, hasInline)
	case "--max":
		p.setIntFlag(i, args, &p.f.max, "--max", inline, hasInline)
	case "--limit":
		p.setIntFlag(i, args, &p.f.limit, "--limit", inline, hasInline)
	case "--range":
		p.setStr(i, args, &p.f.range_, inline, hasInline)
	case "--lines", "--line":
		// --line is the documented singular form (lsp-bridge help); the
		// parser previously rejected it because only --lines existed.
		// evidence-anchor reads the same flag as a STRING (line number):
		// the raw value is captured in .lineRaw while .lines stays the
		// int form the lsp/context commands consume.
		if hasInline {
			p.setInt(&p.f.lines, inline, "--line")
			p.f.lineRaw = inline
		} else if v, ok := p.take(i, args); ok {
			p.setInt(&p.f.lines, v, "--line")
			p.f.lineRaw = v
		}
	case "--depth":
		p.setIntFlag(i, args, &p.f.depth, "--depth", inline, hasInline)
	case "--full":
		p.setBool(&p.f.full, "--full", inline, hasInline)
	case "--from":
		p.setStr(i, args, &p.f.from, inline, hasInline)
	case "--to":
		p.setStr(i, args, &p.f.to, inline, hasInline)
	case "--severity":
		p.setStr(i, args, &p.f.severity, inline, hasInline)
	case "--engine":
		p.setStr(i, args, &p.f.engine, inline, hasInline)
	case "--lang":
		p.setStr(i, args, &p.f.lang, inline, hasInline)
	case "--stdin":
		p.setStr(i, args, &p.f.stdin, inline, hasInline)
	case "--output":
		p.setStr(i, args, &p.f.execOutput, inline, hasInline)
	case "--anchor":
		p.setStr(i, args, &p.f.execAnchor, inline, hasInline)
	case "--no-instructions":
		p.setBool(&p.f.noinstructions, "--no-instructions", inline, hasInline)
	case "--max-tokens":
		p.setIntFlag(i, args, &p.f.maxTokens, "--max-tokens", inline, hasInline)
	case "--max-files":
		p.setIntFlag(i, args, &p.f.maxFiles, "--max-files", inline, hasInline)
	case "--context-before":
		p.setIntFlag(i, args, &p.f.contextBefore, "--context-before", inline, hasInline)
	case "--context-after":
		p.setIntFlag(i, args, &p.f.contextAfter, "--context-after", inline, hasInline)
	case "--tier":
		p.setStr(i, args, &p.f.tier, inline, hasInline)
	case "--precision":
		p.setStr(i, args, &p.f.precision, inline, hasInline)
	case "--min-confidence":
		p.setStr(i, args, &p.f.minConfidence, inline, hasInline)
	case "--fold":
		p.setBool(&p.f.fold, "--fold", inline, hasInline)
	case "--explain":
		p.setBool(&p.f.explain, "--explain", inline, hasInline)
	case "--graph":
		p.setBool(&p.f.graph, "--graph", inline, hasInline)
	case "--generate":
		p.setBool(&p.f.generate, "--generate", inline, hasInline)
	case "--staged":
		p.setBool(&p.f.staged, "--staged", inline, hasInline)
	case "--subject":
		p.setBool(&p.f.subject, "--subject", inline, hasInline)
	case "--message":
		p.setStr(i, args, &p.f.message, inline, hasInline)
	case "--name":
		p.setStr(i, args, &p.f.name, inline, hasInline)
	case "--dry-run":
		p.setBool(&p.f.dryRun, "--dry-run", inline, hasInline)
	case "--approver":
		p.setStr(i, args, &p.f.approver, inline, hasInline)
	case "--version":
		p.setStr(i, args, &p.f.version, inline, hasInline)
	case "--reject":
		p.setBool(&p.f.reject, "--reject", inline, hasInline)
	case "--reason":
		p.setStr(i, args, &p.f.reason, inline, hasInline)
	case "--action":
		p.setStr(i, args, &p.f.action, inline, hasInline)
	case "--column", "--col":
		p.setIntFlag(i, args, &p.f.column, "--column", inline, hasInline)
	case "--compiler-output":
		p.setStr(i, args, &p.f.compilerOutput, inline, hasInline)
	case "--edits":
		p.setStr(i, args, &p.f.edits, inline, hasInline)
	case "--server-cmd":
		p.setStr(i, args, &p.f.serverCmd, inline, hasInline)
	case "--target":
		p.setStr(i, args, &p.f.target, inline, hasInline)
	case "--min-fixes":
		p.setIntFlag(i, args, &p.f.minFixes, "--min-fixes", inline, hasInline)
	default:
		return false
	}
	return true
}

// parseMigratedFlag handles the migrated FlagSet flag family — every case in
// parseFlags' switch AFTER the `// ---- Migrated FlagSet flags` marker (the
// 29 stdlib flag.FlagSet parsers folded into the unified parser). It reports
// whether the flag was handled; unhandled names fall through to the help /
// unknown-flag handling in parseFlags.
func (p *flagParser) parseMigratedFlag(name, inline string, hasInline bool, i *int, args []string) bool {
	switch name {
	case "--agent":
		p.setStr(i, args, &p.f.agent, inline, hasInline)
	case "--auto":
		p.setBool(&p.f.auto, "--auto", inline, hasInline)
	case "--auto-gap":
		p.setBool(&p.f.autoGap, "--auto-gap", inline, hasInline)
	case "--base":
		p.setStr(i, args, &p.f.base, inline, hasInline)
	case "--body":
		p.setStr(i, args, &p.f.body, inline, hasInline)
	case "--channel":
		p.setStr(i, args, &p.f.channel, inline, hasInline)
	case "--chunk-size":
		p.setStr(i, args, &p.f.chunkSize, inline, hasInline)
	case "--claim":
		p.setStr(i, args, &p.f.claim, inline, hasInline)
	case "--deny-path":
		if hasInline {
			p.f.denyPaths = append(p.f.denyPaths, inline)
		} else if v, ok := p.take(i, args); ok {
			p.f.denyPaths = append(p.f.denyPaths, v)
		}
	case "--diff":
		p.setStr(i, args, &p.f.diff, inline, hasInline)
	case "--expect-fingerprint":
		p.setStr(i, args, &p.f.expectFingerprint, inline, hasInline)
	case "--field":
		p.setStr(i, args, &p.f.field, inline, hasInline)
	case "--field-type":
		p.setStr(i, args, &p.f.fieldType, inline, hasInline)
	case "--format":
		p.setStr(i, args, &p.f.format, inline, hasInline)
	case "--half-life":
		p.setStr(i, args, &p.f.halfLife, inline, hasInline)
	case "--interface":
		p.setStr(i, args, &p.f.iface, inline, hasInline)
	case "--inject-memory":
		p.setStr(i, args, &p.f.injectMemory, inline, hasInline)
	case "--k", "-k":
		p.setStr(i, args, &p.f.k, inline, hasInline)
	case "--keep-tasks":
		p.setIntFlag(i, args, &p.f.keepTasks, "--keep-tasks", inline, hasInline)
	case "--key":
		p.setStr(i, args, &p.f.key, inline, hasInline)
	case "--local":
		p.setStr(i, args, &p.f.local, inline, hasInline)
	case "--notes":
		p.setStr(i, args, &p.f.notes, inline, hasInline)
	case "--older-than":
		p.setStr(i, args, &p.f.olderThan, inline, hasInline)
	case "--payload":
		p.setStr(i, args, &p.f.payload, inline, hasInline)
	case "--percent":
		p.setStr(i, args, &p.f.percent, inline, hasInline)
	case "--pipeline":
		p.setStr(i, args, &p.f.pipeline, inline, hasInline)
	case "--policy":
		p.setStr(i, args, &p.f.policy, inline, hasInline)
	case "--progress-token":
		p.setStr(i, args, &p.f.progressToken, inline, hasInline)
	case "--prompt":
		p.setStr(i, args, &p.f.prompt, inline, hasInline)
	case "--remote":
		p.setStr(i, args, &p.f.remote, inline, hasInline)
	case "--repo":
		if hasInline {
			p.f.repoPaths = append(p.f.repoPaths, inline)
		} else if v, ok := p.take(i, args); ok {
			p.f.repoPaths = append(p.f.repoPaths, v)
		}
	case "--resource":
		p.setStr(i, args, &p.f.resource, inline, hasInline)
	case "--role":
		p.setStr(i, args, &p.f.role, inline, hasInline)
	case "--sign":
		p.setBool(&p.f.sign, "--sign", inline, hasInline)
	case "--full-state":
		p.setBool(&p.f.fullState, "--full-state", inline, hasInline)
	case "--restore":
		p.setStr(i, args, &p.f.restore, inline, hasInline)
	case "--sig":
		p.setStr(i, args, &p.f.sig, inline, hasInline)
	case "--tag":
		p.setStr(i, args, &p.f.tag, inline, hasInline)
	case "--template":
		p.setStr(i, args, &p.f.template, inline, hasInline)
	case "--text":
		p.setStr(i, args, &p.f.text, inline, hasInline)
	case "--tool":
		p.setStr(i, args, &p.f.tool, inline, hasInline)
	case "--ttl":
		p.setStr(i, args, &p.f.ttl, inline, hasInline)
	case "--url":
		p.setStr(i, args, &p.f.url, inline, hasInline)
	default:
		return false
	}
	return true
}
