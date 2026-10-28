package destrules

// shell_write.go adds the shell-write family to the governed-exec classifier.
//
// Why this is here: the bash/agent-shell oracle (guardrule) gates every shell
// write — `echo x > f`, `: > f`, `truncate -s0 f`, and a `mv` whose source is
// home/system state — because a redirect or truncation destroys content just as
// surely as `rm`, with no undo and no `rm -rf` to notice in a log. The
// governed-exec oracle only knew about argv-shaped destruction, so the same
// command routed through `kern build`/`kern_exec` ran ungoverned. That is the
// exact bypass `kern guard parity` exists to catch; its corpus pinned the pair
// and this file closes it.

import "strings"

// shellWriteTarget returns the destination of the first real shell WRITE
// redirect in seg, or "" when the segment writes nothing.
//
// Quote-aware: a '>' inside '…' or "…" is text (a comparison in `python3 -c
// "if a > b:"`), not a redirect. Null sinks are ignored (`>/dev/null`,
// `2>/dev/null`, `2>&1`, `&>/dev/null`) — those are how a command SUPPRESSES
// output, which is why the bash oracle allows them too.
func shellWriteTarget(seg string) string {
	var quote byte
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
			continue
		case quote == '"':
			if c == '"' {
				quote = 0
			}
			continue
		case c == '\'' || c == '"':
			quote = c
			continue
		case c != '>':
			continue
		}
		// Found a redirect operator. Skip the doubled form (>>).
		j := i + 1
		for j < len(seg) && seg[j] == '>' {
			j++
		}
		for j < len(seg) && (seg[j] == ' ' || seg[j] == '\t' || seg[j] == '\n' || seg[j] == '\r') {
			j++
		}
		if j >= len(seg) {
			return ""
		}
		if seg[j] == '&' {
			continue // fd duplication (2>&1, >&2): no file is written
		}
		start := j
		for j < len(seg) && !strings.ContainsRune(" \t\n\r;|&", rune(seg[j])) {
			j++
		}
		target := seg[start:j]
		if target == "" || isNullSink(target) {
			continue
		}
		return target
	}
	return ""
}

// isNullSink reports whether a redirect target discards output instead of
// writing a file.
func isNullSink(target string) bool {
	switch target {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/zero":
		return true
	}
	return strings.HasPrefix(target, "/dev/fd/")
}

// isHomeOrSystemPath reports whether an operand names home or system state —
// the sources a `mv` must never relocate silently (the bash oracle gates those
// and allows in-project moves).
func isHomeOrSystemPath(t string) bool {
	if t == "~" || strings.HasPrefix(t, "~/") || t == "$HOME" || strings.HasPrefix(t, "$HOME/") || t == "${HOME}" || strings.HasPrefix(t, "${HOME}/") {
		return true
	}
	if !strings.HasPrefix(t, "/") {
		return false
	}
	for _, prefix := range []string{
		"/home/", "/Users/", "/root/", "/etc/", "/usr/", "/bin/", "/sbin/",
		"/var/", "/opt/", "/System/", "/Library/", "/Applications/",
	} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return t == "/etc" || t == "/usr" || t == "/bin" || t == "/sbin" || t == "/var" || t == "/root"
}

// classifyShellWrite covers the write-by-side-effect family: a write redirect,
// `truncate`, and a `mv` that relocates home/system state.
func ClassifyShellWrite(name, seg string, tokens []string) (bool, string) {
	if target := shellWriteTarget(seg); target != "" {
		return true, "shell write redirect to " + target
	}
	if name == "truncate" || name == "shred" {
		return true, name + " destroys file content"
	}
	if name == "mv" {
		for _, t := range tokens[1:] {
			if strings.HasPrefix(t, "-") {
				continue
			}
			if isHomeOrSystemPath(t) {
				return true, "mv relocates " + t
			}
		}
	}
	return false, ""
}
