package cliflags

import (
	"strings"
	"testing"
	"time"
)

// testFlags is a local flag family exercising every Parser setter kind.
type testFlags struct {
	root       string
	lines      int
	lim        bool
	timeout    int
	timeoutDur string
	timeoutSet bool
}

// parseTest converts the local family through the shared Parser exactly the
// way the extracted CLI families do (switch over Next + typed setters).
func parseTest(args []string) (testFlags, []string, error) {
	var f testFlags
	p := NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--lines":
			p.SetInt(&f.lines, "--lines", inline, hasInline)
		case "--lim":
			p.SetBool(&f.lim, "--lim", inline, hasInline)
		case "--timeout":
			p.SetTimeout(&f.timeout, &f.timeoutDur, &f.timeoutSet, "--timeout", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// TestParserUnifiedSemantics pins the unified cmd/kern parser semantics the
// extracted families rely on: value forms, single-dash normalization,
// accept-and-default, positionals, and the sticky error contract.
func TestParserUnifiedSemantics(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    testFlags
		rest    []string
		wantErr string
	}{
		{"flag value form", []string{"--root", "x"}, testFlags{root: "x"}, nil, ""},
		{"inline form", []string{"--root=y"}, testFlags{root: "y"}, nil, ""},
		{"single-dash long form normalizes", []string{"-root", "x"}, testFlags{root: "x"}, nil, ""},
		{"trailing value-less flag accepts default", []string{"--root"}, testFlags{}, nil, ""},
		{"int value", []string{"--lines", "7"}, testFlags{lines: 7}, nil, ""},
		{"int inline", []string{"--lines=9"}, testFlags{lines: 9}, nil, ""},
		{"int invalid", []string{"--lines", "x"}, testFlags{}, nil, `--lines: invalid integer "x"`},
		{"bool bare", []string{"--lim"}, testFlags{lim: true}, nil, ""},
		{"bool inline false", []string{"--lim=false"}, testFlags{lim: false}, nil, ""},
		{"bool inline true", []string{"--lim=true"}, testFlags{lim: true}, nil, ""},
		{"bool inline garbage", []string{"--lim=bogus"}, testFlags{}, nil, `--lim: invalid boolean value "bogus"`},
		{"timeout int seconds", []string{"--timeout", "30"}, testFlags{timeout: 30, timeoutSet: true}, nil, ""},
		{"timeout duration kept verbatim", []string{"--timeout", "30m"}, testFlags{timeoutDur: "30m", timeoutSet: true}, nil, ""},
		{"timeout garbage", []string{"--timeout", "x"}, testFlags{}, nil, `--timeout: invalid timeout "x" (want integer seconds or a duration like 30m)`},
		{"unknown flag", []string{"--nope"}, testFlags{}, nil, "unknown flag: --nope"},
		{"unknown single-dash reports normalized", []string{"-nope"}, testFlags{}, nil, "unknown flag: --nope"},
		{"positionals collect in order", []string{"a", "--root", "x", "b"}, testFlags{root: "x"}, []string{"a", "b"}, ""},
		{"negative number stays positional", []string{"-1", "--"}, testFlags{}, []string{"-1", "--"}, ""},
		{"flag-looking value consumed as value", []string{"--root", "--lines"}, testFlags{root: "--lines"}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, rest, err := parseTest(tc.args)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if f != tc.want {
				t.Fatalf("flags = %+v, want %+v", f, tc.want)
			}
			if len(rest) != len(tc.rest) {
				t.Fatalf("rest = %v, want %v", rest, tc.rest)
			}
			for i := range rest {
				if rest[i] != tc.rest[i] {
					t.Fatalf("rest = %v, want %v", rest, tc.rest)
				}
			}
		})
	}
}

// TestParserHelpForms pins that -h is NOT normalized to --h and reaches the
// family switch untouched, matching the canonical parser.
func TestParserHelpForms(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		p := NewParser([]string{arg})
		name, _, _, ok := p.Next()
		if !ok {
			t.Fatalf("Next(%q) not ok", arg)
		}
		if name != arg {
			t.Fatalf("Next(%q) = %q, want %q", arg, name, arg)
		}
	}
}

// TestTimeoutSeconds pins the shared cliTimeout fold (the execution, sandbox
// and heal families used to carry byte-identical copies): the 120s default
// for an unset --timeout, zero for an explicit "--timeout 0", and the given
// value otherwise.
func TestTimeoutSeconds(t *testing.T) {
	cases := []struct {
		name       string
		timeoutSet bool
		timeout    int
		want       time.Duration
	}{
		{"unset defaults to 120s", false, 0, 120 * time.Second},
		{"unset with negative value defaults", false, -5, 120 * time.Second},
		{"explicit zero means no limit", true, 0, 0},
		{"explicit value in seconds", true, 30, 30 * time.Second},
		{"explicit value wins over default", true, 5, 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TimeoutSeconds(tc.timeoutSet, tc.timeout); got != tc.want {
				t.Fatalf("TimeoutSeconds(%v, %d) = %v, want %v", tc.timeoutSet, tc.timeout, got, tc.want)
			}
		})
	}
}

// TestParserStickyIntErrorNotOverwrittenByLaterInt pins the sticky behavior:
// once an int error is set, later int values are not parsed (the canonical
// setInt returns early), matching the unified parser.
func TestParserStickyIntErrorNotOverwrittenByLaterInt(t *testing.T) {
	_, _, err := parseTest([]string{"--lines", "x", "--lines", "5"})
	if err == nil || !strings.Contains(err.Error(), "invalid integer") {
		t.Fatalf("err = %v, want invalid integer", err)
	}
}

// TestSplitRange pins the git-range parser shared by the review/guard/churn
// commands (consolidation tranche 8): "a..b" splits on the FIRST "..", a
// single ref keeps "to" empty (git's "compare to working tree" convention),
// and the empty input is the empty pair.
func TestSplitRange(t *testing.T) {
	cases := []struct {
		name     string
		r        string
		wantFrom string
		wantTo   string
	}{
		{"empty", "", "", ""},
		{"single ref", "main", "main", ""},
		{"two-dot range", "a..b", "a", "b"},
		{"first dot wins", "a..b..c", "a", "b..c"},
		{"refs with dots", "HEAD~1..HEAD", "HEAD~1", "HEAD"},
		{"bare dots", "..", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			from, to := SplitRange(tc.r)
			if from != tc.wantFrom || to != tc.wantTo {
				t.Fatalf("SplitRange(%q) = (%q, %q), want (%q, %q)", tc.r, from, to, tc.wantFrom, tc.wantTo)
			}
		})
	}
}
