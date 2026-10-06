package governance

// exec_classify.go — command-class classification for the exec firewall
// (audit-table-2 A). Dangerous command classes — destructive, network-pipe
// install, and installer — require human approval BY DEFAULT regardless of
// KERN_EXEC_RISK. KERN_EXEC_RISK=LOW is the explicit operator opt-out that
// disables class escalation. The classifier is deterministic, lexical, and
// quote-aware: a bar-raiser against naive hostile commands, not a sandbox.
// Obfuscated exec (base64 -d | sh, python -c "...") is residual risk.

import (
	"strings"
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
	// DangerInstaller matches npm/pip/go/apt/brew/make install (unless the
	// segment carries --dry-run).
	DangerInstaller DangerClass = "installer"
)

// ClassifyDangerousCommand returns the highest-precedence dangerous class
// matched by the command and a human-readable reason (empty when the command
// is not dangerous). Precedence: destructive > pipe-install > installer.
// The command may join simple commands with shell separators; classification
// is per-segment with one level of recursion into sh -c "...".
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
	tokens := shellTokens(seg)
	tokens = stripPrefixes(tokens)
	if len(tokens) == 0 {
		return DangerNone, ""
	}
	name := tokens[0]

	// One level of recursion into sh -c "…" / bash -c "…" wrappers.
	if (name == "sh" || name == "bash" || name == "dash" || name == "zsh") && len(tokens) >= 3 && tokens[1] == "-c" {
		inner := strings.Join(tokens[2:], " ")
		if cls, why := ClassifyDangerousCommand(inner); cls != DangerNone {
			return cls, name + " -c: " + why
		}
		return DangerNone, ""
	}

	// Destructive triggers.
	if cls, why := classifyDestructive(name, tokens); cls != DangerNone {
		return cls, why
	}

	// Installer triggers (after destructive; --dry-run exempts).
	if strings.Contains(seg, "--dry-run") {
		return DangerNone, ""
	}
	return classifyInstaller(name, tokens)
}

// stripPrefixes removes leading sudo/doas and VAR=VALUE environment prefixes
// so the first token is the actual command name.
func stripPrefixes(tokens []string) []string {
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

// classifyInstaller applies the installer-class rules to one segment.
func classifyInstaller(name string, tokens []string) (DangerClass, string) {
	rest := tokens[1:]
	// npm/pnpm/yarn install|add|ci; pip/pip3 install; go install.
	switch name {
	case "npm", "pnpm", "yarn":
		if len(rest) > 0 && (rest[0] == "install" || rest[0] == "add" || rest[0] == "ci") {
			return DangerInstaller, name + " " + rest[0]
		}
	case "pip", "pip3":
		if len(rest) > 0 && rest[0] == "install" {
			return DangerInstaller, name + " install"
		}
	case "go":
		if len(rest) > 0 && rest[0] == "install" {
			return DangerInstaller, "go install"
		}
	case "make":
		if len(rest) > 0 && rest[0] == "install" {
			return DangerInstaller, "make install"
		}
	case "apt", "apt-get", "dnf", "yum", "apk", "brew":
		if len(rest) > 0 && (rest[0] == "install" || rest[0] == "upgrade") {
			return DangerInstaller, name + " " + rest[0]
		}
	case "pacman":
		// pacman installs/upgrades with -S / -Syu / --sync / --upgrade flags.
		for _, t := range rest {
			if strings.HasPrefix(t, "--") && (t == "--sync" || t == "--upgrade") {
				return DangerInstaller, "pacman " + t
			}
			if strings.HasPrefix(t, "-S") && len(t) >= 2 {
				return DangerInstaller, "pacman " + t
			}
		}
	}
	return DangerNone, ""
}

// firstShellToken returns the first token of a stage after stripping
// sudo/env prefixes (quote-aware).
func firstShellToken(seg string) string {
	tokens := stripPrefixes(shellTokens(seg))
	if len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

// shellTokens tokenizes a command segment on whitespace with quote awareness
// (single, double, and backslash escapes), dequoting each token — so
// `rm -rf 'my dir'` classifies the same as `rm -rf "my dir"`.
func shellTokens(s string) []string {
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
