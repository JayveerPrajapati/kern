// Package cliflags holds the shared CLI flag-token plumbing used by the kern
// command surface: stdin reading with a size cap, flag-token splitting
// (--flag=value), and stdlib-compat error rendering. Extracted from cmd/kern
// and internal/approvalcli, which carried byte-identical copies (deep-verify
// D1); parseFlagsOrDie stays local to each package — it is thin glue over
// each family's own parseFlags + fatalUsage.
package cliflags

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// MaxStdinBytes caps piped stdin reads: input larger than this is rejected
// instead of being buffered fully.
const MaxStdinBytes = 64 << 20

// ReadStdin returns the full piped stdin, rejecting input larger than
// MaxStdinBytes instead of buffering it all. If stdin is a character device
// (interactive terminal) rather than a pipe/redirect, it returns nil
// immediately to prevent indefinite blocking.
func ReadStdin() ([]byte, error) {
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, MaxStdinBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxStdinBytes {
		return nil, fmt.Errorf("stdin exceeds %d bytes", MaxStdinBytes)
	}
	return b, nil
}

// IsFlagToken reports whether arg looks like a flag token: a --long form or
// a -x short form with an alphabetic letter.
func IsFlagToken(arg string) bool {
	return len(arg) > 2 && strings.HasPrefix(arg, "--") ||
		len(arg) > 1 && arg[0] == '-' && IsAlpha(arg[1])
}

// SplitFlag splits a flag token into (name, inlineValue, hasInlineValue):
// `--flag=value` becomes ("--flag", "value", true). Non-flag tokens (and
// flag tokens without '=') pass through unsplit.
func SplitFlag(arg string) (name, val string, hasVal bool) {
	if IsFlagToken(arg) {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			return arg[:eq], arg[eq+1:], true
		}
	}
	return arg, "", false
}

// IsAlpha reports whether b is an ASCII letter.
func IsAlpha(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// HasFlag reports whether the given flag appears in args. Used for
// early-checks in main() before the unified parser runs.
func HasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// TimeoutSeconds returns the effective timeout for subprocess-running
// commands (build, validate, exec, sandbox, heal): an unset --timeout gets a
// 120s default so a hung subprocess can never wedge a tool call forever; an
// explicit "--timeout 0" means no limit. Mirrors cmd/kern's toolTimeout
// contract. The shared fold of the byte-identical cliTimeout helpers the
// extracted families carried (consolidation tranche C5).
func TimeoutSeconds(timeoutSet bool, timeout int) time.Duration {
	if timeoutSet && timeout == 0 {
		return 0
	}
	if timeout <= 0 {
		return 120 * time.Second
	}
	return time.Duration(timeout) * time.Second
}

// StdlibFlagErr renders a unified-parser error in the stdlib flag package's
// phrasing for the few handlers whose stderr message is pinned to that
// format (e.g. synthesize-test's "flag provided but not defined" wording).
// Parsing is still fully unified through the family parser — only the error
// message rendering differs.
func StdlibFlagErr(err error) error {
	if err == nil {
		return nil
	}
	if rest, ok := strings.CutPrefix(err.Error(), "unknown flag: "); ok {
		return fmt.Errorf("flag provided but not defined: %s", rest)
	}
	return err
}
