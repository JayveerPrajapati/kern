package approvalcli

import (
	"github.com/JayveerPrajapati/kern/internal/cliflags"
)

// flags holds the parsed command-line flags the approval/governance family
// reads — the family's slice of the former cmd/kern flags struct, moved with
// the code. Only the fields these commands consume are kept.
type flags struct {
	root       string
	json       bool
	file       string
	approver   string
	reject     bool
	reason     string
	merge      bool
	timeout    int
	timeoutSet bool
	timeoutDur string // --timeout duration-string form (30m, 500ms); integer-seconds values stay in .timeout
}

// parseFlags parses the family's flags via the shared cliflags.Parser,
// mirroring the cmd/kern unified
// parser semantics the moved code depended on (preserved verbatim):
//
//  1. TRAILING VALUE-LESS FLAGS ARE ACCEPT-AND-DEFAULT (a value flag at the
//     end of the line with no value is accepted and left at its default).
//  2. --flag=value AND --flag value FORMS ARE BOTH ACCEPTED for value flags;
//     bool flags accept --flag, --flag=true and --flag=false.
//  3. Unknown flags are rejected with "unknown flag: X" (fail loud, usage
//     error, exit 2 via the caller's fatalUsage). Negative numbers and a
//     bare "-" (stdin) are positionals, not flags.
//  4. Invalid integer values fail loud: "FLAG: invalid integer \"V\"".
//  5. Positionals keep their order in rest; the parser does NOT stop at the
//     first positional.
func parseFlags(args []string) (flags, []string, error) {
	var f flags
	f.timeout = 120
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--file":
			p.SetStr(&f.file, inline, hasInline)
		case "--approver":
			p.SetStr(&f.approver, inline, hasInline)
		case "--reject":
			p.SetBool(&f.reject, "--reject", inline, hasInline)
		case "--reason":
			p.SetStr(&f.reason, inline, hasInline)
		case "--merge":
			p.SetBool(&f.merge, "--merge", inline, hasInline)
		case "--timeout":
			// Dual-form: an integer is seconds (historical), a Go duration
			// string (30m, 500ms) is captured verbatim for commands that need
			// sub-second or minute-scale bounds (kern approval wait).
			p.SetTimeout(&f.timeout, &f.timeoutDur, &f.timeoutSet, "--timeout", inline, hasInline)
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}
