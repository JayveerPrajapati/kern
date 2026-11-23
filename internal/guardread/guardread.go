// Package guardread is the read half of the guard's classifier: it decides
// whether a command line is a pure-read pipeline and, when it is, which serve
// tier answers it (tier 1 = index-backed single read, tier 2 = read head plus
// bounded tails, tier 3 = readonly sandbox). It also owns the segment
// tokenizer the gate classifier shares — splitting on top-level separators,
// reading a segment's command name, and the redirect algebra — because both
// halves must agree on what a segment is. Extracted from internal/guardrule
// when that package filled its ARCHITECTURE.md cap (the same ratchet that
// produced internal/grepscope and internal/guardpaths); the verdict types live
// here and guardrule aliases them, so the two halves keep one vocabulary
// without an import cycle.
package guardread

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/execclassify"
	"github.com/JayveerPrajapati/kern/internal/grepscope"
	"github.com/JayveerPrajapati/kern/internal/guardpaths"
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

// The read-side name sets — the single Go source for the plugin/guard lists
// (P2). READ_CMDS serve-never-deny. awk and sed are intentionally absent:
// both can execute arbitrary shell commands from a “read” script.
var readCMDs = map[string]bool{
	"grep": true, "egrep": true, "rg": true, "ag": true, "find": true,
	"cat": true, "head": true, "tail": true, "less": true,
	"sort": true, "uniq": true, "cut": true,
}

var readOnlyGit = map[string]bool{
	"status": true, "log": true, "rev-parse": true, "diff": true, "show": true,
	"branch": true, "remote": true, "add": true, "commit": true,
	"shortlog": true, "rev-list": true, "describe": true, "merge-base": true,
}

var gitLogPatchFlags = map[string]bool{"-p": true, "--patch": true, "-u": true, "--raw": true}

var trivialCMDs = map[string]bool{
	"pwd": true, "date": true, "whoami": true, "true": true, "echo": true,
	"ls": true, "which": true, "wc": true, "cd": true,
}

// ShellWrapperCMDs are the shell wrappers whose `-c` payload is classified as
// the inner script. Shared with the gate classifier, which unwraps one level
// before deciding GATE.
var ShellWrapperCMDs = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
}

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

// UnquotedMetachar reports the first shell-significant character that
// appears OUTSIDE single quotes, or inside double quotes where it still
// expands. Metachars inside single quotes are literal text — `git commit -m
// 'pin 84->83'` carries a quoted arrow, not a redirection — so they do not
// disqualify a simple command; the same text unquoted (`84->83` bare) would
// redirect and must stay governed. Returns "" when the command has no
// unquoted operators and balanced quotes.
func UnquotedMetachar(cmd string) string {
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

// IsSimpleCommand mirrors the plugin's isSimpleCommand: content-free commands
// (trivial shell, read-only git, any kern CLI invocation) run raw.
func IsSimpleCommand(cmd string) bool {
	if UnquotedMetachar(cmd) != "" {
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
	if UnquotedMetachar(seg) != "" {
		return false
	}
	if !IsSimpleCommand(seg) {
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

// SplitStages splits a command line on top-level shell separators (| ; && || &)
// with quote awareness — the same semantics as the governance firewall's
// splitter (mirrored here so the classify surface is self-contained and the
// governance package stays under its ARCHITECTURE.md LOC cap). Separators
// inside quotes are literal text.
func SplitStages(command string) []string {
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

// HasWriteRedirect reports whether the segment contains a real write
// redirect: > or >> to any target other than the /dev/null sink (discarding
// output writes nothing), excluding fd merges like 2>&1, outside quotes —
// '>' inside quotes is text, not a redirection. Replaces the former raw
// strings.Contains(seg, ">") scan, which false-gated read pipelines that
// merely silenced stderr (cmd 2>/dev/null) — the top observed false-GATE.
func HasWriteRedirect(seg string) bool {
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

// StripNullRedirects removes output-discarding redirects (>/dev/null,
// 2>/dev/null, >>/dev/null, 2>&1, and their spaced variants) from the
// command, outside quotes. The cleaned command is what the risk classifier
// reasons over — a discard writes nothing, so it must neither gate the
// command (HasWriteRedirect already tolerates it) nor pollute read-serve
// argument parsing (tail f 2>/dev/null would otherwise take "2>/dev/null"
// for the file argument). The allow-once hash stays bound to the original
// command text.
func StripNullRedirects(seg string) string {
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

// ReadOnlyPipeline reports whether every segment is a read command or a
// read-only companion AND at least one segment is an actual read. One level
// of sh -c "..." / bash -c "..." wrapper unwrapping (mirrors the plugin's
// readPipeline): a wrapper segment classifies as its inner script.
func ReadOnlyPipeline(cmd string) bool {
	sawRead := false
	for _, seg := range SplitStages(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		seg = unwrapWrapper(seg)
		if UnquotedMetachar(seg) != "" {
			return false
		}
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
	if len(toks) >= 3 && strings.HasPrefix(toks[1], "-c") && ShellWrapperCMDs[toks[0]] {
		return strings.Join(toks[2:], " ")
	}
	return seg
}

// ReadPipeline classifies a pure-read pipeline into a serve tier.
func ReadPipeline(cmd string) ClassifyResult {
	segs := SplitStages(cmd)
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
		if name == "cat" {
			// Only a BARE `cat FILE` can be replaced by the symbolic summary:
			// `cat -n` adds line numbers and multiple targets concatenate,
			// both of which a summary would silently destroy. Everything else
			// stays a real read in the readonly sandbox (Tier 3).
			if len(args) == 1 && !strings.HasPrefix(args[0], "-") {
				return ClassifyResult{Verdict: VerdictServe, Tier: 1, Head: name, File: args[0]}
			}
			return ClassifyResult{Verdict: VerdictServe, Tier: 3}
		}
		if name == "head" || name == "tail" || name == "less" || name == "more" {
			// Windowed/paged reads must preserve the requested slice
			// (head -3, tail -2, pager): serve them verbatim as Tier 3.
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

// IsGitShowDiffRead reports a `git show` / `git diff` invocation that only
// reads history and content — the audited rev-read class (`git show
// HEAD:file`, `git diff <rev>`). Both write nothing, so they are served
// from the readonly sandbox (Tier 3) instead of being denied on hook hosts.
// --output/--ext-diff/--textconv and friends are refused (they write files
// or execute external programs); the simple --stat forms are already handled
// by IsSimpleCommand before this runs.
func IsGitShowDiffRead(cmd string) bool {
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
