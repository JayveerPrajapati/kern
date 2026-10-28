// Package guardrule is the guard's classification engine: the single Go
// source of truth for the bash-arm risk verdicts that the plugin (TS) and
// the shell guard used to re-implement separately (P3 of the guard-steer
// campaign; extracted from internal/contextcli when the classifier outgrew
// the CLI family's ARCHITECTURE.md cap). It returns the same risk-axis
// verdict the plugin's bash arm applies: pure-read pipelines are SERVED
// (tier 1/2/3), write/destructive/network/installer segments GATE (deny
// whole command), simple content-free commands ALLOW raw, and everything
// else is GOVERNED (kern build). Deny/hint messages become byte-parity by
// construction (one source).
package guardrule

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/execclassify"
	"github.com/JayveerPrajapati/kern/internal/grepscope"
	"github.com/JayveerPrajapati/kern/internal/guardpaths"
	"github.com/JayveerPrajapati/kern/internal/inplaceedit"
)

// ClassifyVerdict enumerates the risk-axis outcomes a command can receive.
type ClassifyVerdict string

const (
	VerdictAllow    ClassifyVerdict = "allow"    // simple content-free — run raw
	VerdictGate     ClassifyVerdict = "gate"     // write/destructive/network/installer — deny whole command
	VerdictServe    ClassifyVerdict = "serve"    // pure-read pipeline — served tier 1/2/3
	VerdictGoverned ClassifyVerdict = "governed" // everything else — kern build
)

// ClassifyResult is the machine-readable classification of one command.
type ClassifyResult struct {
	Verdict   ClassifyVerdict `json:"verdict"`
	Tier      int             `json:"tier"`                 // 1|2|3 when serve, else 0
	Head      string          `json:"head,omitempty"`       // serve: read command name (grep|cat|find|...)
	Pattern   string          `json:"pattern,omitempty"`    // serve: symbol pattern for grep-family
	Scope     string          `json:"scope,omitempty"`      // serve: repo-relative file/dir the pattern is scoped to
	File      string          `json:"file,omitempty"`       // serve: file path for cat/head/tail/less/more
	Tails     []string        `json:"tails,omitempty"`      // serve tier 2: the tail segments
	Hint      string          `json:"hint"`                 // one-line steer
	Hash      string          `json:"hash"`                 // sha256 of the command (allow-once subject)
	PathAware bool            `json:"path_aware,omitempty"` // allow via the path-aware write arm (in-tree targets)
}

// The risk-axis name sets — the single Go source for the plugin/guard lists
// (P2). READ_CMDS serve-never-deny; GATE_CMDS deny the whole command.
var readCMDs = map[string]bool{
	"grep": true, "egrep": true, "rg": true, "ag": true, "find": true,
	"cat": true, "head": true, "tail": true, "less": true, "awk": true, "sed": true,
	"sort": true, "uniq": true, "cut": true,
}

var gateCMDs = map[string]bool{
	"rm": true, "mv": true, "cp": true, "dd": true, "mkfs": true, "wipefs": true,
	"diskutil": true, "shred": true, "truncate": true, "chmod": true, "chown": true,
	"curl": true, "wget": true, "fetch": true,
}

var installerCMDs = map[string]bool{
	"npm": true, "pnpm": true, "yarn": true, "pip": true, "pip3": true, "go": true,
	"make": true, "apt": true, "apt-get": true, "brew": true, "pacman": true,
}

var installActions = map[string]bool{
	"install": true, "add": true, "ci": true, "upgrade": true, "get": true,
}

var shellWrapperCMDs = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
}

var trivialCMDs = map[string]bool{
	"pwd": true, "date": true, "whoami": true, "true": true, "echo": true,
	"ls": true, "which": true, "wc": true, "cd": true,
}

var readOnlyGit = map[string]bool{
	"status": true, "log": true, "rev-parse": true, "diff": true, "show": true,
	"branch": true, "remote": true, "add": true, "commit": true,
	"shortlog": true, "rev-list": true, "describe": true, "merge-base": true,
}

var gitLogPatchFlags = map[string]bool{"-p": true, "--patch": true, "-u": true, "--raw": true}

// isStatOnlyGit mirrors the plugin's isStatOnlyGit: 0 iff the git diff/show
// invocation prints only statistics or file names.
func isStatOnlyGit(sub string, args []string) bool {
	stat := false
	for _, arg := range args {
		switch {
		case arg == "--stat" || arg == "--shortstat" || arg == "--numstat" ||
			arg == "--name-only" || arg == "--name-status" || arg == "--summary" ||
			arg == "--dirstat" || arg == "--no-patch" || arg == "-s" ||
			strings.HasPrefix(arg, "--stat=") || strings.HasPrefix(arg, "--dirstat="):
			stat = true
		case arg == "-p" || arg == "--patch" || arg == "-u" ||
			arg == "--patch-with-stat" || arg == "--patch-with-raw" ||
			strings.HasPrefix(arg, "-U") || strings.HasPrefix(arg, "--unified") ||
			strings.HasPrefix(arg, "--word-diff") || strings.HasPrefix(arg, "--color-words") ||
			strings.HasPrefix(arg, "--output") || arg == "--ext-diff" || arg == "--textconv":
			return false
		case len(arg) >= 2 && arg[0] == '-' && !strings.HasPrefix(arg, "--") &&
			(strings.ContainsRune(arg[1:], 'p') || strings.ContainsRune(arg[1:], 'u')):
			return false
		case sub == "show" && strings.Contains(arg, ":"):
			return false
		}
	}
	return stat
}

// unquotedMetachar reports the first shell-significant character that
// appears OUTSIDE single quotes, or inside double quotes where it still
// expands. Metachars inside single quotes are literal text — `git commit -m
// 'pin 84->83'` carries a quoted arrow, not a redirection — so they do not
// disqualify a simple command; the same text unquoted (`84->83` bare) would
// redirect and must stay governed. Returns "" when the command has no
// unquoted operators and balanced quotes.
func unquotedMetachar(cmd string) string {
	inS, inD := false, false
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case inS:
			if c == '\'' {
				inS = false
			}
		case inD:
			switch c {
			case '\\':
				// A backslash inside double quotes escapes the next byte, so
				// `\"` is a literal quote, not the end of the string. Without
				// this, an escaped quote desynchronized the quote state and
				// later text (a pipe, a redirect in a commit message) was
				// misread as live shell syntax.
				i++
			case '"':
				inD = false
			case '`', '$':
				// Command substitution and expansion still fire inside
				// double quotes.
				return string(c)
			}
		case c == '\'':
			inS = true
		case c == '"':
			inD = true
		case strings.IndexByte(";&|<>`\n\r$", c) >= 0:
			return string(c)
		}
	}
	if inS || inD {
		return "unbalanced-quote"
	}
	return ""
}

// isSimpleCommand mirrors the plugin's isSimpleCommand: content-free commands
// (trivial shell, read-only git, any kern CLI invocation) run raw.
func isSimpleCommand(cmd string) bool {
	if unquotedMetachar(cmd) != "" {
		return false
	}
	trimmed := strings.TrimSpace(cmd)
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return false
	}
	// ANY invocation of the kern CLI itself runs raw. The CLI carries its
	// own governance (approval workflow for installer-class commands,
	// tty-gated allow-once minting, sandboxed exec, update provenance
	// guard), and the guard's deny messages steer agents TO kern commands —
	// blocking them here would be self-defeating.
	if parts[0] == "kern" {
		return true
	}
	if parts[0] == "git" {
		if len(parts) < 2 {
			return false // bare "git" has no subcommand — not simple
		}
		i := 1
		if parts[i] == "-C" {
			if len(parts) < i+2 || parts[i+1] == "" || strings.HasPrefix(parts[i+1], "-") {
				return false
			}
			i += 2
		}
		if len(parts) < i+1 || !readOnlyGit[parts[i]] {
			return false
		}
		rest := parts[i+1:]
		switch parts[i] {
		case "diff", "show":
			return isStatOnlyGit(parts[i], rest)
		case "branch":
			for _, a := range rest {
				if !gitBranchListFlag(a) {
					return false
				}
			}
			return true
		case "remote":
			if len(rest) == 0 {
				return true
			}
			if len(rest) == 1 && (rest[0] == "-v" || rest[0] == "--verbose") {
				return true
			}
			return rest[0] == "get-url"
		case "add":
			for _, a := range rest {
				if a == "-f" || a == "--force" || (len(a) >= 2 && a[0] == '-' && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'f')) {
					return false
				}
			}
			return true
		case "commit":
			for _, a := range rest {
				if a == "--no-verify" || (len(a) >= 2 && a[0] == '-' && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'n')) {
					return false
				}
			}
			return true
		case "log":
			for _, a := range rest {
				if gitLogPatchFlags[a] || strings.HasPrefix(a, "--patch") ||
					(len(a) >= 2 && a[0] == '-' && !strings.HasPrefix(a, "--") && (strings.ContainsRune(a[1:], 'p') || strings.ContainsRune(a[1:], 'u'))) {
					return false
				}
			}
			return true
		}
		return true
	}
	return trivialCMDs[parts[0]]
}

func gitBranchListFlag(a string) bool {
	switch a {
	case "--show-current", "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "--list":
		return true
	}
	return false
}

// isReadOnlyCompanion mirrors the plugin's: a segment that may legally co-head
// a pure-read pipeline — trivial content-free or READ-ONLY git (never git
// add/commit, which are writes) and never carrying unquoted shell operators
// (quoted text is literal and safe).
func isReadOnlyCompanion(seg string) bool {
	if unquotedMetachar(seg) != "" {
		return false
	}
	if !isSimpleCommand(seg) {
		return false
	}
	parts := strings.Fields(strings.TrimSpace(seg))
	if len(parts) == 0 {
		return false
	}
	// A kern CLI command is NOT a read companion: it runs raw as a simple
	// command, but it must never ride into a Tier-3 readonly-sandbox serve
	// — pipes containing kern stay governed (split them instead).
	if parts[0] == "kern" {
		return false
	}
	if parts[0] != "git" {
		return true
	}
	i := 1
	if i < len(parts) && parts[i] == "-C" {
		if i+1 >= len(parts) || parts[i+1] == "" || strings.HasPrefix(parts[i+1], "-") {
			return false
		}
		i += 2
	}
	return i < len(parts) && parts[i] != "add" && parts[i] != "commit"
}

// segName returns the command name of a segment after prefix stripping.
func segName(seg string) string {
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(seg))
	if len(toks) == 0 {
		return ""
	}
	return toks[0]
}

// splitStages splits a command line on top-level shell separators (| ; && || &)
// with quote awareness — the same semantics as the governance firewall's
// splitter (mirrored here so the classify surface is self-contained and the
// governance package stays under its ARCHITECTURE.md LOC cap). Separators
// inside quotes are literal text.
func splitStages(command string) []string {
	var stages []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			stages = append(stages, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
			cur.WriteByte(c)
		case quote == '"':
			if c == '"' {
				quote = 0
			} else if c == '\\' && i+1 < len(command) {
				cur.WriteByte(c)
				i++
				cur.WriteByte(command[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
		case c == '&' && i+1 < len(command) && command[i+1] == '&':
			flush()
			i++
		case c == '|' && i+1 < len(command) && command[i+1] == '|':
			flush()
			i++
		case c == '\n' || c == '\r' || c == '|' || c == ';' || c == '&':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return stages
}

// redirectOperand scans the redirect target starting at k and returns the
// operand with trailing shell metacharacters trimmed, plus the index just past
// the raw operand. The operand ends at whitespace or at a shell metacharacter
// ( ); & | ), so in `(cat 2>/dev/null))` the sink is /dev/null — the trailing
// parens are syntax, not part of the path. Without this a read-only subshell
// compared as "/dev/null))" and falsely gated.
func redirectOperand(seg string, k int) (string, int) {
	end := k
	for end < len(seg) && seg[end] != ' ' && seg[end] != '\t' && !strings.ContainsRune(");&|", rune(seg[end])) {
		end++
	}
	return strings.TrimRight(seg[k:end], ");&|"), end
}

// hasWriteRedirect reports whether the segment contains a real write
// redirect: > or >> to any target other than the /dev/null sink (discarding
// output writes nothing), excluding fd merges like 2>&1, outside quotes —
// '>' inside quotes is text, not a redirection. Replaces the former raw
// strings.Contains(seg, ">") scan, which false-gated read pipelines that
// merely silenced stderr (cmd 2>/dev/null) — the top observed false-GATE.
func hasWriteRedirect(seg string) bool {
	i, n := 0, len(seg)
	for i < n {
		c := seg[i]
		switch {
		case c == '\'':
			j := i + 1
			for j < n && seg[j] != '\'' {
				j++
			}
			i = j + 1
		case c == '"':
			j := i + 1
			for j < n && seg[j] != '"' {
				if seg[j] == '\\' && j+1 < n {
					j++
				}
				j++
			}
			i = j + 1
		case c == '>':
			j := i + 1
			if j < n && seg[j] == '&' {
				// fd merge (2>&1, >&2) — not a file write.
				i = j + 1
				continue
			}
			if j < n && seg[j] == '>' {
				j++ // append form (>>)
			}
			k := j
			for k < n && (seg[k] == ' ' || seg[k] == '\t') {
				k++
			}
			target, end := redirectOperand(seg, k)
			if target != "/dev/null" {
				return true
			}
			i = end
		default:
			i++
		}
	}
	return false
}

// stripNullRedirects removes output-discarding redirects (>/dev/null,
// 2>/dev/null, >>/dev/null, 2>&1, and their spaced variants) from the
// command, outside quotes. The cleaned command is what the risk classifier
// reasons over — a discard writes nothing, so it must neither gate the
// command (hasWriteRedirect already tolerates it) nor pollute read-serve
// argument parsing (tail f 2>/dev/null would otherwise take "2>/dev/null"
// for the file argument). The allow-once hash stays bound to the original
// command text.
func stripNullRedirects(seg string) string {
	var b strings.Builder
	i, n := 0, len(seg)
	for i < n {
		c := seg[i]
		switch {
		case c == '\'':
			j := i + 1
			for j < n && seg[j] != '\'' {
				j++
			}
			if j < n {
				j++
			}
			b.WriteString(seg[i:j])
			i = j
		case c == '"':
			j := i + 1
			for j < n && seg[j] != '"' {
				if seg[j] == '\\' && j+1 < n {
					j++
				}
				j++
			}
			if j < n {
				j++
			}
			b.WriteString(seg[i:j])
			i = j
		case c == '>':
			j := i + 1
			merge := false
			if j < n && seg[j] == '&' {
				merge = true
				j++
			} else if j < n && seg[j] == '>' {
				j++
			}
			k := j
			for k < n && (seg[k] == ' ' || seg[k] == '\t') {
				k++
			}
			target, end := redirectOperand(seg, k)
			if merge || target == "/dev/null" {
				s := strings.TrimRight(b.String(), " \t")
				if len(s) > 0 && s[len(s)-1] >= '0' && s[len(s)-1] <= '9' {
					s = s[:len(s)-1]
				}
				s = strings.TrimRight(s, " \t&")
				b.Reset()
				b.WriteString(s)
				i = end
				continue
			}
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return strings.TrimSpace(b.String())
}

// allSegmentsSimple reports whether a multi-segment command (joined by
// &&/||/;/|/newline) consists ONLY of simple content-free segments —
// isSimpleCommand whitelists trivial shell, read-only git and kern
// diagnostics only, so such a compound has no write/network surface and
// runs raw like a single simple command instead of paying the governed
// wrapper (`ls a ; ls b`, `git log --oneline && git status`).
func allSegmentsSimple(cmd string) bool {
	segs := splitStages(cmd)
	if len(segs) < 2 {
		return false
	}
	for _, seg := range segs {
		if !isSimpleCommand(seg) {
			return false
		}
	}
	return true
}

// gateSegmentHit mirrors the plugin's gateSegmentHit: 0 iff the segment is a
// write/destructive/network/installer.
func gateSegmentHit(rawSeg string, depth int) bool {
	// A real write/append redirect makes the segment a gate; discarding
	// redirects (>/dev/null, 2>&1) and quoted '>' are not writes.
	if hasWriteRedirect(rawSeg) {
		return true
	}
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(rawSeg))
	if len(toks) == 0 {
		return false
	}
	name := toks[0]
	rest := toks[1:]
	// Destructive/pipe-install classes are shared with the exec firewall, so
	// find -delete, xargs rm, tee, git clean/reset and forced kills gate here
	// exactly as they do in kern_exec. The path-aware in-tree write arm runs
	// BEFORE gateHit, so this only sees commands that already failed it.
	if cls, _ := execclassify.ClassifyDangerousCommand(rawSeg); cls == execclassify.DangerDestructive || cls == execclassify.DangerPipeInstall {
		return true
	}
	if depth < 2 && len(toks) > 2 && strings.HasPrefix(toks[1], "-c") && shellWrapperCMDs[name] {
		inner := strings.Join(toks[2:], " ")
		if inner != "" && gateHit(inner, depth+1) {
			return true
		}
	}
	if gateCMDs[name] {
		return true
	}
	if installerCMDs[name] && len(rest) > 0 {
		if name == "pacman" {
			for _, t := range rest {
				if strings.HasPrefix(t, "-S") || t == "--sync" || t == "--upgrade" {
					return true
				}
			}
		} else if name == "go" || name == "make" {
			if rest[0] == "install" {
				return true
			}
		} else if installActions[rest[0]] {
			return true
		}
	}
	if name == "git" {
		// Any push publishes to a remote (network + irreversible history):
		// the operator exempted git reads/add/commit, not push. A dry run
		// writes nothing and stays out of the gate.
		push, dry := false, false
		for _, t := range rest {
			if t == "push" {
				push = true
			}
			if t == "--dry-run" || t == "-n" {
				dry = true
			}
		}
		if push && !dry {
			return true
		}
	}
	return false
}

// gateHit reports whether ANY &&/||/;/|/newline-separated segment is a gate.
func gateHit(cmd string, depth int) bool {
	// Whole-pipeline classification: a dangerous pipeline whose individual
	// segments look benign in isolation (printf | base64 -d | sh) must gate.
	if cls, _ := execclassify.ClassifyDangerousCommand(cmd); cls == execclassify.DangerDestructive || cls == execclassify.DangerPipeInstall {
		return true
	}
	for _, seg := range splitStages(cmd) {
		if gateSegmentHit(seg, depth) {
			return true
		}
	}
	return false
}

// readOnlyPipeline reports whether every segment is a read command or a
// read-only companion AND at least one segment is an actual read. One level
// of sh -c "..." / bash -c "..." wrapper unwrapping (mirrors the plugin's
// readPipeline): a wrapper segment classifies as its inner script.
func readOnlyPipeline(cmd string) bool {
	sawRead := false
	for _, seg := range splitStages(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		seg = unwrapWrapper(seg)
		if readCMDs[segName(seg)] {
			// sed -i REWRITES files — it must never ride the read serve
			// tiers (the readonly sandbox would only fail it later).
			if segIsInPlaceSed(seg) {
				return false
			}
			sawRead = true
			continue
		}
		if !isReadOnlyCompanion(seg) {
			return false
		}
	}
	return sawRead
}

// segIsInPlaceSed reports whether the segment is a sed invocation with an
// in-place editing flag.
func segIsInPlaceSed(seg string) bool {
	if segName(seg) != "sed" {
		return false
	}
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(unwrapWrapper(seg)))
	return len(toks) > 1 && guardpaths.HasSedinPlaceFlag(toks[1:])
}

// unwrapWrapper returns the inner script of a sh -c "..." / bash -c "..."
// wrapper (one level), or the segment unchanged.
func unwrapWrapper(seg string) string {
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(seg))
	if len(toks) >= 3 && strings.HasPrefix(toks[1], "-c") && shellWrapperCMDs[toks[0]] {
		return strings.Join(toks[2:], " ")
	}
	return seg
}

// readPipeline classifies a pure-read pipeline into a serve tier.
func readPipeline(cmd string) ClassifyResult {
	segs := splitStages(cmd)
	names := make([]string, len(segs))
	for i, s := range segs {
		names[i] = segName(unwrapWrapper(strings.TrimSpace(s)))
	}
	// A cd segment changes relative-path semantics for every later read;
	// Tier 1/2 answers resolve against the repo root and would serve the
	// WRONG file. Drop to Tier 3 — the sandboxed run preserves the cd.
	for _, n := range names {
		if n == "cd" {
			return ClassifyResult{Verdict: VerdictServe, Tier: 3}
		}
	}
	// Tier 1: single read segment (optionally with &&/;-joined companions),
	// never a pipe.
	readIdx := -1
	for i, n := range names {
		if readCMDs[n] {
			readIdx = i
			break
		}
	}
	if readIdx < 0 {
		return ClassifyResult{Verdict: VerdictGoverned}
	}
	nonReadTrivial := true
	for i, s := range segs {
		if i == readIdx {
			continue
		}
		if !isReadOnlyCompanion(unwrapWrapper(strings.TrimSpace(s))) {
			nonReadTrivial = false
			break
		}
	}
	if nonReadTrivial && !strings.Contains(cmd, "|") {
		name := names[readIdx]
		toks := execclassify.StripPrefixes(execclassify.ShellTokens(unwrapWrapper(strings.TrimSpace(segs[readIdx]))))
		args := toks[1:]
		if name == "find" {
			return ClassifyResult{Verdict: VerdictServe, Tier: 1, Head: "find"}
		}
		if name == "grep" || name == "egrep" || name == "rg" || name == "ag" {
			pattern, targets := grepscope.SplitArgs(args)
			// An unscoped symbol grep keeps the original tier-1 answer.
			if grepscope.SymbolShaped(pattern) && len(targets) == 0 {
				return ClassifyResult{Verdict: VerdictServe, Tier: 1, Head: name, Pattern: pattern}
			}
			// A scoped SYMBOL grep is answered from the index too, filtered
			// to the caller's file/dir (Tier 1 carries the scope so the
			// serve runs `kern search <pattern> --path <scope>`). Regex,
			// include/exclude and count/list shapes have no index
			// equivalent and stay Tier 3 — a real grep in the sandbox.
			if p, scope, ok := grepscope.Scoped(args); ok {
				return ClassifyResult{Verdict: VerdictServe, Tier: 1, Head: name, Pattern: p, Scope: scope}
			}
			return ClassifyResult{Verdict: VerdictServe, Tier: 3}
		}
		if name == "cat" || name == "head" || name == "tail" || name == "less" || name == "more" {
			file := ""
			for _, a := range args {
				if !strings.HasPrefix(a, "-") {
					file = a
				}
			}
			if file != "" {
				return ClassifyResult{Verdict: VerdictServe, Tier: 1, Head: name, File: file}
			}
			return ClassifyResult{Verdict: VerdictServe, Tier: 3}
		}
		return ClassifyResult{Verdict: VerdictServe, Tier: 3}
	}
	// Tier 2: read head + bounded simple tails.
	if len(segs) > 1 {
		headName := names[0]
		if (headName == "grep" || headName == "egrep" || headName == "rg" || headName == "ag") && readCMDs[headName] {
			headToks := execclassify.StripPrefixes(execclassify.ShellTokens(unwrapWrapper(strings.TrimSpace(segs[0]))))
			pattern, targets := grepscope.SplitArgs(headToks[1:])
			scope := ""
			servable := grepscope.SymbolShaped(pattern) && len(targets) == 0
			if !servable {
				if p, s, ok := grepscope.Scoped(headToks[1:]); ok {
					pattern, scope, servable = p, s, true
				}
			}
			if servable {
				tails := []string{}
				ok := true
				for _, s := range segs[1:] {
					t := strings.TrimSpace(s)
					if (strings.HasPrefix(t, "wc") && strings.Contains(t, "-l")) ||
						strings.HasPrefix(t, "head") || strings.HasPrefix(t, "tail") ||
						strings.HasPrefix(t, "grep -v") || strings.HasPrefix(t, "grep") {
						tails = append(tails, t)
						continue
					}
					ok = false
					break
				}
				if ok && len(tails) > 0 {
					return ClassifyResult{Verdict: VerdictServe, Tier: 2, Head: headName, Pattern: pattern, Scope: scope, Tails: tails}
				}
			}
		}
	}
	return ClassifyResult{Verdict: VerdictServe, Tier: 3}
}

// isGitShowDiffRead reports a `git show` / `git diff` invocation that only
// reads history and content — the audited rev-read class (`git show
// HEAD:file`, `git diff <rev>`). Both write nothing, so they are served
// from the readonly sandbox (Tier 3) instead of being denied on hook hosts.
// --output/--ext-diff/--textconv and friends are refused (they write files
// or execute external programs); the simple --stat forms are already handled
// by isSimpleCommand before this runs.
func isGitShowDiffRead(cmd string) bool {
	parts := strings.Fields(strings.TrimSpace(cmd))
	if len(parts) == 0 || parts[0] != "git" {
		return false
	}
	i := 1
	if i < len(parts) && parts[i] == "-C" {
		if i+1 >= len(parts) || parts[i+1] == "" || strings.HasPrefix(parts[i+1], "-") {
			return false
		}
		i += 2
	}
	if i >= len(parts) || (parts[i] != "show" && parts[i] != "diff") {
		return false
	}
	for _, a := range parts[i+1:] {
		switch {
		case a == "--output" || strings.HasPrefix(a, "--output="),
			a == "--ext-diff", a == "--textconv", a == "--no-textconv",
			a == "-O" || strings.HasPrefix(a, "--output-indicator"):
			return false
		case len(a) >= 2 && a[0] == '-' && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'O'):
			return false
		case a == "-O":
			return false
		}
	}
	return true
}

// ClassifyCommand classifies one command into the risk-axis verdict.
// Root-less: no path-awareness (flat GATE for write commands) — kept for
// callers and tests that classify command text alone. The guard surfaces
// (Go hook, kern guard classify CLI, and through it the script and the
// opencode plugin) call ClassifyCommandInRoot with the real project root.
func ClassifyCommand(cmd string) ClassifyResult {
	return ClassifyCommandInRoot(cmd, "")
}

// ClassifyCommandInRoot classifies with a project root, enabling the
// path-aware write arm: local file-shape writes (rm/mv/cp/mkdir/touch,
// sed -i) whose every path argument resolves INSIDE the root (or the temp
// dir), away from .git internals and credential-shaped files, are allowed —
// the same intent the hosts' native edit tools already exercise ungated, with
// commit-time verification as the backstop. Destructive-class commands are
// NEVER path-aware-allowed (a recursive forced delete gates even in-tree);
// everything else (out-of-root targets, .git, credentials, recursive rm of
// top-level entries, disk and network commands) keeps the flat GATE verdict.
func ClassifyCommandInRoot(cmd, root string) ClassifyResult {
	return ClassifyCommandInRoots(cmd, root, nil)
}

// ClassifyCommandInRoots additionally accepts extra workspace roots
// (the operator's KERN_MCP_ROOTS list): absolute write targets under a
// second configured root classify as in-tree instead of falling to the
// flat out-of-root GATE. Relative paths stay bound to the primary root,
// and no target may resolve AT a root (rm -rf on a workspace root still
// gates).
func ClassifyCommandInRoots(cmd, root string, extraRoots []string) ClassifyResult {
	if strings.TrimSpace(cmd) == "" {
		return ClassifyResult{Verdict: VerdictGoverned}
	}
	sum := sha256.Sum256([]byte(cmd))
	hash := hex.EncodeToString(sum[:])
	// The risk classifier reasons over the command with output-discarding
	// redirects (>/dev/null, 2>&1) removed: they write nothing. The
	// allow-once hash stays bound to the exact original text.
	eff := stripNullRedirects(cmd)
	if eff == "" {
		eff = cmd
	}
	if isSimpleCommand(eff) {
		return ClassifyResult{Verdict: VerdictAllow, Hash: hash,
			Hint: "content-free command — runs raw"}
	}
	if allSegmentsSimple(eff) {
		return ClassifyResult{Verdict: VerdictAllow, Hash: hash,
			Hint: "compound of content-free commands — runs raw"}
	}
	// In-place edits of CODE files are refactors, not scratch writes: gate
	// them so the change goes through the edit tool / kern_apply (journaled
	// and verifiable) instead of an opaque rewrite. Non-code files, temp
	// scratch and out-of-root targets keep their existing write policy.
	if root != "" && anyInPlaceCodeEdit(eff, root) {
		return ClassifyResult{Verdict: VerdictGate, Hash: hash,
			Hint: "GATE: in-place edit of a code file (sed -i / perl -pi) — use the edit tool or kern_apply"}
	}
	// Destructive commands only ride the path-aware arm when every target is
	// scratch space: `rm -rf internal/x` gates like `rm -rf .git`, while
	// /tmp scratch deletes stay raw (shared destructive class with exec).
	if root != "" && allSegmentsSafeWrites(eff, root, extraRoots) && guardpaths.DestructiveAllowed(eff) {
		return ClassifyResult{Verdict: VerdictAllow, Hash: hash, PathAware: true,
			Hint: "path-aware write — every target in-tree (or tmp), .git/credentials/disk/network excluded; audited"}
	}
	if root != "" && hasUnsafePathWrite(eff, root, extraRoots) {
		return ClassifyResult{Verdict: VerdictGate, Hash: hash,
			Hint: "GATE write — target outside the project root/temp, or .git/credential/recursive-top-level"}
	}
	if gateHit(eff, 0) {
		return ClassifyResult{Verdict: VerdictGate, Hash: hash,
			Hint: "GATE command (write/destructive/network/installer) — operator approval required"}
	}
	if isGitShowDiffRead(eff) {
		return ClassifyResult{Verdict: VerdictServe, Tier: 3, Hash: hash,
			Hint: "git history read — served from the readonly sandbox"}
	}
	if readOnlyPipeline(eff) {
		res := readPipeline(eff)
		res.Hash = hash
		res.Hint = "pure-read pipeline — served from the kern index / readonly sandbox"
		return res
	}
	return ClassifyResult{Verdict: VerdictGoverned, Hash: hash,
		Hint: "governed command — runs through kern build"}
}

// anyInPlaceCodeEdit reports whether any segment edits a code file in place
// (sed -i, perl/ruby -i) inside the root.
func anyInPlaceCodeEdit(cmd, root string) bool {
	for _, seg := range splitStages(cmd) {
		if inplaceedit.TargetsCodeFile(strings.TrimSpace(seg), root) {
			return true
		}
	}
	return false
}

// allSegmentsSafeWrites reports whether every stage of the command is
// either content-free-simple or a path-aware in-tree write command
// (internal/guardpaths owns the per-segment path math).
func allSegmentsSafeWrites(cmd, root string, extraRoots []string) bool {
	segs := splitStages(cmd)
	if len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		if isSimpleCommand(seg) {
			continue
		}
		if !guardpaths.SafeWriteSegment(seg, root, extraRoots) {
			return false
		}
	}
	return true
}

// hasUnsafePathWrite reports whether any segment is a path-write command
// (rm/mv/cp/mkdir/touch, or sed in-place) whose paths FAILED the safe
// check: the command's whole point is a write to somewhere disallowed.
// It gates even though the bare command name is not in gateCMDs — a
// governed verdict would let the write run on hosts that execute
// governed commands.
func hasUnsafePathWrite(cmd, root string, extraRoots []string) bool {
	for _, seg := range splitStages(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if isSimpleCommand(seg) {
			continue
		}
		if guardpaths.WriteSegment(seg) && !guardpaths.SafeWriteSegment(seg, root, extraRoots) {
			return true
		}
	}
	return false
}
