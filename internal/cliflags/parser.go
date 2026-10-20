// parser.go holds the shared per-command flag parser used by the kern
// command surface's extracted families (internal/mcpcli, internal/execution,
// internal/sandbox, internal/heal, internal/setup, internal/commitmsg,
// internal/hook, internal/verifycmd, internal/enterprise, internal/approvalcli,
// internal/graphcli…). It replicates the unified cmd/kern parser's semantics
// (cmd/kern/flags.go parseFlags — the canonical reference, which stays in
// cmd/kern): single-dash long flags normalize to their double-dash form,
// --flag=value and --flag value both work, a trailing value-less flag is
// accept-and-default, unknown flags are a sticky parse error, and non-flag
// tokens accumulate as positionals. Extracted so the ~20 per-family copies of
// the loop carry zero duplicated mechanics (consolidation tranche 7).
package cliflags

import (
	"fmt"
	"strconv"
	"time"
)

// Parser is a single-pass flag parser over one command's argument tail. A
// family converts by looping Next and switching on the returned (normalized)
// flag name, routing each arm into the typed setters; everything else —
// token splitting, single-dash normalization, positional collection,
// value consumption, sticky error handling — lives here, once.
//
// Usage:
//
//	p := cliflags.NewParser(args)
//	for {
//		name, inline, hasInline, ok := p.Next()
//		if !ok {
//			break
//		}
//		switch name {
//		case "--file":
//			p.SetStr(&f.file, inline, hasInline)
//		case "--lines":
//			p.SetInt(&f.lines, "--lines", inline, hasInline)
//		case "--json":
//			p.SetBool(&f.json, "--json", inline, hasInline)
//		case "--help", "-h":
//		default:
//			p.Unknown(name)
//		}
//	}
//	f, rest, err := f, p.Rest, p.Err()
type Parser struct {
	args []string
	i    int

	// Rest holds the positional arguments (tokens that are not flag tokens).
	Rest []string

	err error // first/last sticky parse error (see the setter docs)
}

// NewParser returns a Parser over args (the command's argument tail, after
// the subcommand name).
func NewParser(args []string) *Parser {
	return &Parser{args: args}
}

// Err returns the sticky parse error, if any.
func (p *Parser) Err() error { return p.err }

// Next advances to the next flag token and returns its (normalized) long
// name, inline value and whether an inline value was present. Non-flag
// tokens are collected into Rest as positionals and skipped. ok is false at
// the end of args. Single-dash long flags (`-json`, `-root`) normalize to
// their double-dash form; only tokens that look like a single-dash letter
// flag (IsFlagToken logic) and are not the short help form `-h` are
// rewritten; `-k`/`-terse-code` normalize to their dual-form cases, and
// negative numbers (`-1`) or a bare `-`/`--` are untouched (still
// positionals).
func (p *Parser) Next() (name, inline string, hasInline, ok bool) {
	for p.i < len(p.args) {
		tok := p.args[p.i]
		name, inline, hasInline = SplitFlag(tok)
		if len(name) > 1 && name[0] == '-' && name[1] != '-' && IsAlpha(name[1]) && name != "-h" {
			name = "--" + name[1:]
		}
		p.i++
		if IsFlagToken(tok) {
			return name, inline, hasInline, true
		}
		p.Rest = append(p.Rest, tok)
	}
	return "", "", false, false
}

// Take consumes the next raw argument as a flag value (the --flag value
// form). ok=false at end of args — the documented accept-and-default
// semantic: a trailing value-less flag is left at its default.
func (p *Parser) Take() (string, bool) {
	if p.i < len(p.args) {
		v := p.args[p.i]
		p.i++
		return v, true
	}
	return "", false
}

// SetStr consumes the value of a string flag: the inline --flag=value form
// when present, else the next argument (accept-and-default when none).
func (p *Parser) SetStr(dst *string, inline string, hasInline bool) {
	if hasInline {
		*dst = inline
		return
	}
	if v, ok := p.Take(); ok {
		*dst = v
	}
}

// SetInt consumes the value of an int flag. A non-integer value is a sticky
// parse error ("<name>: invalid integer %q"); an earlier sticky error is
// respected (the value is not parsed).
func (p *Parser) SetInt(dst *int, name, inline string, hasInline bool) {
	if p.err != nil {
		return
	}
	if hasInline {
		p.setInt(dst, inline, name)
		return
	}
	if v, ok := p.Take(); ok {
		p.setInt(dst, v, name)
	}
}

func (p *Parser) setInt(dst *int, val, name string) {
	n, err := strconv.Atoi(val)
	if err != nil {
		p.err = fmt.Errorf("%s: invalid integer %q", name, val)
		return
	}
	*dst = n
}

// SetBool sets a bool flag: --flag, --flag=true or --flag=false (the stdlib
// flag package's bool forms). An unparsable inline value is a parse error
// ("<name>: invalid boolean value %q"). Like the canonical cmd/kern setBool
// it assigns the error unconditionally, so a later garbage bool overwrites an
// earlier int error (verbatim fidelity with the unified parser).
func (p *Parser) SetBool(dst *bool, name, inline string, hasInline bool) {
	if hasInline {
		b, err := strconv.ParseBool(inline)
		if err != nil {
			p.err = fmt.Errorf("%s: invalid boolean value %q", name, inline)
			return
		}
		*dst = b
	} else {
		*dst = true
	}
}

// SetTimeout consumes a timeout flag's dual-form value: an integer is
// seconds (written to sec), a Go duration string (30m, 500ms) is kept
// verbatim in dur for commands that need sub-second or minute-scale bounds.
// Either form sets set=true so commands can distinguish "given" from
// default; a value that is neither is a sticky parse error ("<name>:
// invalid timeout %q (want integer seconds or a duration like 30m)").
func (p *Parser) SetTimeout(sec *int, dur *string, set *bool, name, inline string, hasInline bool) {
	if p.err != nil {
		return
	}
	val := func() (string, bool) {
		if hasInline {
			return inline, true
		}
		return p.Take()
	}
	v, ok := val()
	if !ok {
		return
	}
	if _, derr := time.ParseDuration(v); derr == nil {
		*dur = v
		*set = true
		return
	}
	if _, ierr := strconv.Atoi(v); ierr == nil {
		p.setInt(sec, v, name)
		*set = true
		return
	}
	p.err = fmt.Errorf("%s: invalid timeout %q (want integer seconds or a duration like 30m)", name, v)
}

// Unknown records the sticky unknown-flag error ("unknown flag: <name>").
// Like the canonical parser's default arm it assigns unconditionally (last
// unknown wins over earlier errors). Call it from the switch's default arm —
// Next only ever returns flag tokens, so no positional check is needed.
func (p *Parser) Unknown(name string) {
	p.err = fmt.Errorf("unknown flag: %s", name)
}
