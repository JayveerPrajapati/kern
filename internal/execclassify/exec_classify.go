package execclassify

// exec_classify.go — command-class classification for the exec firewall.
// Dangerous classes require approval by default regardless of KERN_EXEC_RISK
// (LOW opts out). Deterministic, lexical, quote-aware: a bar-raiser, not a
// sandbox; obfuscated exec (base64 -d | sh) is residual risk.

import (
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/destructive"
	"github.com/JayveerPrajapati/kern/internal/destrules"
	"github.com/JayveerPrajapati/kern/internal/execinterp"
	"github.com/JayveerPrajapati/kern/internal/execwin"
)

// DangerClass is the class of a dangerous command.
type DangerClass string

const (
	// DangerNone means the command matches no dangerous class.
	DangerNone DangerClass = ""
	// DangerDestructive matches rm -rf /…, dd to /dev/*, mkfs/wipefs,
	// chmod -R on root-ish targets, writes to /dev/sd*/disk*/nvme*, and the
	// classic fork bomb.
	DangerDestructive DangerClass = "destructive"
	// DangerPipeInstall matches curl/wget | sh-style network-pipe installs.
	DangerPipeInstall DangerClass = "pipe-install"
	// DangerNetworkFetch matches a plain curl/wget/fetch invocation — an
	// unattended outbound download (the download+interpret case is
	// pipe-install above).
	DangerNetworkFetch DangerClass = "network-fetch"
	// DangerInstaller matches npm/pip/go/apt/brew/make install (unless the
	// segment carries --dry-run).
	DangerInstaller DangerClass = "installer"
)

// ClassifyDangerousCommand returns the highest-precedence dangerous class
// matched by the command and a human-readable reason (empty when the command
// is not dangerous). Precedence: destructive > pipe-install > network-fetch
// > installer. The command may join simple commands with shell separators;
// classification is per-segment with one level of recursion into sh -c "...".
func ClassifyDangerousCommand(command string) (DangerClass, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return DangerNone, ""
	}
	// Whole-command literal: the classic fork bomb family. It cannot be
	// split into segments safely (it contains every separator), so detect it
	// before the splitter. Both `:(){:|:&};:` and `:(){ :|:& };:` forms.
	if strings.Contains(command, ":(){") || strings.Contains(command, ":() {") {
		return DangerDestructive, "fork bomb pattern"
	}

	stages := splitStages(command)
	// Destructive has the highest precedence: scan every stage first.
	for _, s := range stages {
		if cls, why := classifySegment(s.text); cls == DangerDestructive {
			return cls, why
		}
	}
	// Pipe-install: a pipeline where an earlier stage downloads and a later
	// stage interprets.
	if cls, why := classifyPipeInstall(stages); cls != DangerNone {
		return cls, why
	}
	// Network-fetch: any stage invoking a plain downloader (F2-2 — a plain
	// fetch has no interpreter in a pipeline, so pipe-install misses it).
	for _, s := range stages {
		if cls, why := classifySegment(s.text); cls == DangerNetworkFetch {
			return cls, why
		}
	}
	// Installer: per-stage, after destructive.
	for _, s := range stages {
		if cls, why := classifySegment(s.text); cls == DangerInstaller {
			return cls, why
		}
	}
	return DangerNone, ""
}

// stage is one top-level shell segment plus whether it was joined by an
// unquoted pipe (so pipe-install can be detected across a pipeline).
type stage struct {
	text string
	pipe bool
}

// splitStages splits a command line on top-level shell separators (| ; && || &)
// with quote awareness; separators inside quotes are literal text (sh
// semantics: a test filter such as -run 'A|B' is not a pipeline). A stage's
// pipe flag is true when it was JOINED TO THE PREVIOUS stage by an unquoted
// pipe, so a pipeline is a maximal run of pipe-joined stages.
func splitStages(command string) []stage {
	var stages []stage
	var cur strings.Builder
	var quote byte
	pipePending := false
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			stages = append(stages, stage{text: s, pipe: pipePending})
		}
		cur.Reset()
		pipePending = false
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
				c = command[i]
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
		case c == '&' && i+1 < len(command) && command[i+1] == '&':
			flush()
			i++
		case c == '|' && i+1 < len(command) && command[i+1] == '|':
			flush()
			i++
		case c == '|':
			flush()
			pipePending = true
		case c == ';' || c == '&':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return stages
}

// classifySegment classifies one stage's text. Returns destructive or
// installer (never pipe-install — that needs the whole pipeline).
func classifySegment(seg string) (DangerClass, string) {
	tokens := ShellTokens(seg)
	tokens = StripPrefixes(tokens)
	if len(tokens) == 0 {
		return DangerNone, ""
	}
	name := tokens[0]
	lname := strings.ToLower(name)

	// One level of recursion into POSIX and Windows shell wrappers. Windows
	// has no /bin/sh; the equivalent bypass is `cmd /c` or a PowerShell
	// `-Command` string, so both are unwrapped here (P0-4).
	if inner, encoded, ok := execwin.Unwrap(name, tokens); ok {
		if encoded {
			return DangerDestructive, name + " executes an encoded script"
		}
		if cls, why := ClassifyDangerousCommand(inner); cls != DangerNone {
			return cls, name + ": " + why
		}
		return DangerNone, ""
	}
	if (lname == "sh" || lname == "bash" || lname == "dash" || lname == "zsh") && len(tokens) >= 3 && tokens[1] == "-c" {
		inner := strings.Join(tokens[2:], " ")
		if cls, why := ClassifyDangerousCommand(inner); cls != DangerNone {
			return cls, name + " -c: " + why
		}
		return DangerNone, ""
	}
	// `eval 'rm -rf d'` is a shell wrapper too.
	if lname == "eval" && len(tokens) >= 2 {
		inner := strings.Join(tokens[1:], " ")
		if cls, why := ClassifyDangerousCommand(inner); cls != DangerNone {
			return cls, "eval: " + why
		}
		return DangerNone, ""
	}

	// Destructive triggers.
	if cls, why := classifyDestructive(name, tokens); cls != DangerNone {
		return cls, why
	}
	// Interpreter/eval forms (python -c subprocess, perl -e unlink, ruby -e
	// File.delete, node -e fs.rmSync) are best-effort pattern matches.
	if hit, why := execinterp.ClassifyInterpreter(name, seg, tokens); hit {
		return DangerDestructive, why
	}
	// Side-effect writes and outside-the-repo state live in destrules (shared
	// with the bash oracle, so both surfaces gate one list).
	if hit, why := destrules.ClassifyShellWrite(name, seg, tokens); hit {
		return DangerDestructive, why
	}
	if hit, why := destrules.ClassifyStateful(name, seg, tokens); hit {
		return DangerDestructive, why
	}

	// Network-fetch (after destructive; F2-2): a plain curl/wget/fetch
	// invocation is an unattended outbound download — approval-gated like
	// installers (`kern build "curl http://…"` executed a real network
	// fetch while verify --command refuses the same command).
	// KERN_ALLOW_LOOPBACK=1 is the explicit opt-out for loopback-only
	// targets: local-dev health probes (kern-server, localhost fixtures)
	// are the common no-risk case. Never a blanket exemption — a loopback
	// listener can proxy outward, and URL-less invocations stay gated.
	if execwin.NetworkFetch(lname) || lname == "curl" || lname == "wget" || lname == "fetch" {
		if os.Getenv("KERN_ALLOW_LOOPBACK") == "1" && destrules.LoopbackOnlySegment(seg) {
			return DangerNone, ""
		}
		return DangerNetworkFetch, name + " performs an outbound network fetch"
	}

	// Installer triggers (after destructive; --dry-run exempts).
	if strings.Contains(seg, "--dry-run") {
		return DangerNone, ""
	}
	if hit, why := destrules.ClassifyInstaller(name, tokens); hit {
		return DangerInstaller, why
	}
	return DangerNone, ""
}

// StripPrefixes removes leading sudo/doas and VAR=VALUE environment prefixes
// so the first token is the actual command name.
func StripPrefixes(tokens []string) []string {
	i := 0
	for i < len(tokens) {
		t := tokens[i]
		if t == "sudo" || t == "doas" {
			i++
			continue
		}
		if eq := strings.IndexByte(t, '='); eq > 0 && !strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "/") {
			i++ // VAR=VALUE env prefix (not a flag or path)
			continue
		}
		break
	}
	return tokens[i:]
}

// classifyDestructive applies the destructive-class rules to one segment.
func classifyDestructive(name string, tokens []string) (DangerClass, string) {
	rest := tokens[1:]
	// Windows-native destructive families (P0-4) live in execwin to keep this
	// file under its LOC cap.
	if why, ok := execwin.Destructive(name, tokens); ok {
		return DangerDestructive, why
	}
	if strings.EqualFold(name, "unlink") {
		return DangerDestructive, "unlink deletes a file"
	}

	// rm: recursive flag with an absolute/~/..-escaping operand, or -f
	// (force) present — `rm -rf /tmp/x` is always destructive; `rm -r build`
	// (relative, cwd-scoped, no -f) is allowed.
	if name == "rm" {
		recursive := false
		force := false
		for _, t := range rest {
			if strings.HasPrefix(t, "-") {
				if strings.Contains(t, "r") || strings.Contains(t, "R") {
					recursive = true
				}
				if strings.Contains(t, "f") {
					force = true
				}
				continue
			}
		}
		if recursive {
			for _, t := range rest {
				if strings.HasPrefix(t, "-") {
					continue
				}
				if force || isRootishOperand(t) {
					return DangerDestructive, "rm recursive " + joinFlags(rest) + " " + t
				}
			}
		}
	}

	// dd writing to a block device: dd ... of=/dev/...
	if name == "dd" {
		for _, t := range rest {
			if strings.HasPrefix(t, "of=/dev/") {
				return DangerDestructive, "dd writes to " + t[len("of="):]
			}
		}
	}

	// mkfs*/wipefs/diskutil erase*: filesystem destruction.
	if strings.HasPrefix(name, "mkfs") || name == "wipefs" || name == "diskutil" && len(rest) > 0 && rest[0] == "erase" {
		return DangerDestructive, name + " destroys a filesystem"
	}

	// chmod/chown -R on a root-ish target.
	if name == "chmod" || name == "chown" {
		recursive := false
		for _, t := range rest {
			if strings.HasPrefix(t, "-") && (strings.Contains(t, "R") || strings.Contains(t, "r")) {
				recursive = true
			}
		}
		if recursive {
			for _, t := range rest {
				if strings.HasPrefix(t, "-") {
					continue
				}
				if t == "/" || t == "/*" || (strings.HasPrefix(t, "/") && !strings.Contains(t[1:], "/")) {
					return DangerDestructive, name + " -R " + t
				}
			}
		}
	}

	// find is a READ command by default, so its destructive predicates must
	// be classified explicitly. The 2026-10-26 audit executed
	// `find <dir> -delete` while the guard classified the command "serve":
	// the file tree was deleted without an approval. xargs/tee/shred forward
	// the same destruction from a benign head, and dd/git clean/reset destroy
	// state too. The rules live in internal/destructive (execclassify sits at
	// its ARCHITECTURE.md cap).
	if hit, why := destructive.PredicateForwarding(name, rest); hit {
		return DangerDestructive, why
	}

	// Forced process kills (kill -9 / pkill -9 / killall).
	if name == "kill" || name == "pkill" || name == "killall" {
		for _, t := range rest {
			if t == "-9" || t == "-KILL" || t == "--signal=KILL" || strings.HasPrefix(t, "--signal=9") {
				return DangerDestructive, name + " forcibly kills a process"
			}
		}
	}

	// Redirect to a block device: ... > /dev/sda (also /dev/disk*, /dev/nvme*),
	// whether the device is attached (> /dev/sda) or in the next token ("> /dev/sda").
	for i, t := range rest {
		if !strings.HasPrefix(t, ">") {
			continue
		}
		dev := strings.TrimPrefix(t, ">")
		if dev == "" && i+1 < len(rest) {
			dev = rest[i+1]
		}
		if strings.HasPrefix(dev, "/dev/sd") || strings.HasPrefix(dev, "/dev/disk") || strings.HasPrefix(dev, "/dev/nvme") {
			return DangerDestructive, "writes to " + dev
		}
	}
	return DangerNone, ""
}

// isRootishOperand reports whether an operand is absolute, ~/$-derived, or
// ..-escaping — the targets a recursive rm must never touch silently.
func isRootishOperand(t string) bool {
	return strings.HasPrefix(t, "/") ||
		strings.HasPrefix(t, "~") ||
		strings.HasPrefix(t, "$") ||
		strings.Contains(t, "..")
}

// classifyPipeInstall detects a pipeline where an earlier stage downloads and
// a later stage interprets: curl/wget/fetch | sh/bash/zsh/dash/python*/perl/ruby.
func classifyPipeInstall(stages []stage) (DangerClass, string) {
	downloaders := map[string]bool{"curl": true, "wget": true, "fetch": true}
	interpreters := map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "python": true, "python3": true, "perl": true, "ruby": true}
	// Walk maximal pipe runs (consecutive stages joined by |).
	for i := 0; i < len(stages); i++ {
		if !stages[i].pipe {
			continue
		}
		// stages[i] is piped from stages[i-1]; extend the run backwards.
		runStart := i - 1
		for runStart > 0 && stages[runStart-1].pipe {
			runStart--
		}
		runEnd := i
		for runEnd+1 < len(stages) && stages[runEnd+1].pipe {
			runEnd++
		}
		var texts []string
		for k := runStart; k <= runEnd; k++ {
			texts = append(texts, stages[k].text)
		}
		if hit, why := execinterp.ClassifyEncodedPipe(texts); hit {
			return DangerPipeInstall, why
		}
		for a := runStart; a < runEnd; a++ {
			firstA := firstShellToken(stages[a].text)
			if !downloaders[firstA] {
				continue
			}
			for b := a + 1; b <= runEnd; b++ {
				firstB := firstShellToken(stages[b].text)
				if interpreters[firstB] {
					return DangerPipeInstall, firstA + " | " + firstB + " runs remote code"
				}
			}
		}
		i = runEnd
	}
	return DangerNone, ""
}

// firstShellToken returns the first token of a stage after stripping
// sudo/env prefixes (quote-aware).
func firstShellToken(seg string) string {
	tokens := StripPrefixes(ShellTokens(seg))
	if len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

// ShellTokens tokenizes a command segment on whitespace with quote awareness
// (single, double, and backslash escapes), dequoting each token — so
// `rm -rf 'my dir'` classifies the same as `rm -rf "my dir"`.
func ShellTokens(s string) []string {
	var tokens []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case quote == '"':
			if c == '"' {
				quote = 0
			} else if c == '\\' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == '\'' || c == '"':
			quote = c
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return tokens
}

// joinFlags is a tiny helper for reason strings.
func joinFlags(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		if strings.HasPrefix(t, "-") {
			b.WriteString(t)
			b.WriteByte(' ')
		}
	}
	return strings.TrimSpace(b.String())
}
