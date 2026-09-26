package version

import (
	"strings"
	"testing"
)

// TestParseValid pins the accept set: 3- and 4-component tags, optional v,
// prerelease and build suffixes, and boundary values.
func TestParseValid(t *testing.T) {
	cases := []struct {
		in         string
		wantMajor  int
		wantMinor  int
		wantPatch  int
		wantFourth int
		wantPre    string
		wantBuild  string
	}{
		{"v0.9.9.1", 0, 9, 9, 1, "", ""},
		{"0.9.5.2", 0, 9, 5, 2, "", ""},
		{"v1.2.3", 1, 2, 3, 0, "", ""},
		{"1.2.3", 1, 2, 3, 0, "", ""},
		{"v1.2.3-rc1", 1, 2, 3, 0, "rc1", ""},
		{"v1.2.3+build.7", 1, 2, 3, 0, "", "build.7"},
		{"v1.2.3-rc1+build", 1, 2, 3, 0, "rc1", "build"},
		{"v0.0.0", 0, 0, 0, 0, "", ""},
		{"v999999.1.1", 999999, 1, 1, 0, "", ""}, // upper bound is exclusive: 999999 < 1e6
		{"v1.2.3.4-rc.1+meta", 1, 2, 3, 4, "rc.1", "meta"},
	}
	for _, tc := range cases {
		v, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.in, err)
			continue
		}
		if v.Major != tc.wantMajor || v.Minor != tc.wantMinor || v.Patch != tc.wantPatch || v.Fourth != tc.wantFourth {
			t.Errorf("Parse(%q) = %d.%d.%d.%d, want %d.%d.%d.%d",
				tc.in, v.Major, v.Minor, v.Patch, v.Fourth,
				tc.wantMajor, tc.wantMinor, tc.wantPatch, tc.wantFourth)
		}
		if v.Prerelease != tc.wantPre || v.Build != tc.wantBuild {
			t.Errorf("Parse(%q) prerelease=%q build=%q, want %q/%q", tc.in, v.Prerelease, v.Build, tc.wantPre, tc.wantBuild)
		}
		if v.Raw != tc.in {
			t.Errorf("Parse(%q).Raw = %q, want the exact input", tc.in, v.Raw)
		}
	}
}

// TestParseRejectsHostileInputs pins the fail-closed reject set: empty,
// overlong, overflow-class, multi-component, signed, leading-zero and
// bad-charset inputs must ALL error — never parse partially.
func TestParseRejectsHostileInputs(t *testing.T) {
	long := "v1.2.3-" + strings.Repeat("a", 60) // 68 chars total
	overlong := "v" + strings.Repeat("9", 63)   // 65 chars total
	cases := []string{
		"",
		"999999999999",       // 12 digits — far beyond the 1e6 bound
		"v1000000.1.1",       // exactly at the exclusive bound → reject
		"v1.1000000.1",       // bound applies to every component
		"v007.1.1",           // leading zeros
		"v1.007.1",           // leading zeros, minor
		"v1.1.007",           // leading zeros, patch
		"v1.1.1.007",         // leading zeros, 4th
		"v1.2.3.4.5",         // five components
		"v1.2.3.4.5.6",       // six components
		"v+1.2.3",            // Atoi would accept "+1" — the regex rejects it
		"v1.+2.3",            // sign inside a field
		"v1.2.-3",            // negative field
		"v1..3",              // empty component
		"v1.2.3-",            // empty prerelease
		"v1.2.3+",            // empty build metadata
		"v1.2.3-rc 1",        // space in prerelease (charset violation)
		"v1.2.3-rc_1",        // underscore in prerelease (charset violation)
		"v1.2.3-rc@1",        // @ in prerelease (charset violation)
		"v1.2.3+meta@x",      // @ in build metadata
		"v1.2.3\n",           // trailing newline
		"  v1.2.3",           // leading whitespace
		"v1.2.3 ",            // trailing whitespace
		"version v1.2.3",     // prose prefix
		"v1.2.3-rc1+build+2", // two '+'
		long,                 // exceeds the 64-char cap
		overlong,             // numeric-only overlong
		"v1.2",               // only two components
		"v1.2.3.",            // trailing dot
		"1.2.3.4.5",          // five components, no v
	}
	for _, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", in)
		}
	}
}

// TestCompareNumericTuple pins the first-class 4-component tuple comparison
// (missing 4th = 0) that golang.org/x/semver cannot express.
func TestCompareNumericTuple(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.9.5.2", "v0.9.9.1", -1},
		{"v0.9.9.1", "v0.9.5.2", 1},
		{"v0.9.9", "v0.9.9.1", -1}, // missing 4th = 0 < 1
		{"v0.9.9.1", "v0.9.9", 1},
		{"v0.9.9.1", "v0.9.9.1", 0},
		{"v0.9.9.1", "0.9.9.1", 0}, // v prefix is cosmetic
		{"v1.0.0", "v0.9.9.9", 1},  // major dominates
		{"v0.10.0", "v0.9.9.9", 1}, // minor dominates
		{"v0.9.10", "v0.9.9.9", 1}, // patch dominates
		{"v0.9.9.10", "v0.9.9.9", 1},
		{"v1.2.3.0", "v1.2.3", 0}, // explicit 0 == missing
		{"v0.0.0", "v0.0.0.0", 0},
		{"v999999.0.0", "v999998.999999.999999", 1},
	}
	for _, tc := range cases {
		a, errA := Parse(tc.a)
		b, errB := Parse(tc.b)
		if errA != nil || errB != nil {
			t.Errorf("Parse(%q/%q): %v / %v", tc.a, tc.b, errA, errB)
			continue
		}
		got := Compare(a, b)
		if got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		// Antisymmetry: Compare(b, a) must be the negated result.
		if rev := Compare(b, a); rev != -tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d (antisymmetry)", tc.b, tc.a, rev, -tc.want)
		}
	}
}

// TestComparePrerelease pins semver prerelease precedence: prerelease sorts
// below its release, numeric identifiers below alphanumeric ones, numeric
// identifiers compare numerically (ignoring leading zeros), and more
// identifiers win when shared ones are equal.
func TestComparePrerelease(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0-rc1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc1", 1},
		{"v1.0.0-alpha", "v1.0.0-beta", -1},
		{"v1.0.0-rc1", "v1.0.0-rc2", -1},
		{"v1.0.0-2", "v1.0.0-10", -1},    // numeric identifiers compare numerically
		{"v1.0.0-rc2", "v1.0.0-rc10", 1}, // alphanumeric identifiers compare ASCII: "rc10" < "rc2"
		{"v1.0.0-1", "v1.0.0-alpha", -1}, // numeric < alphanumeric
		{"v1.0.0-alpha", "v1.0.0-1", 1},
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1}, // more identifiers wins
		{"v1.0.0-alpha.1", "v1.0.0-alpha", 1},
		{"v1.0.0-1.0", "v1.0.0-1", 1},
		{"v1.0.0-01", "v1.0.0-1", 0}, // leading zeros ignored numerically
		{"v1.0.0-rc1", "v1.0.0-rc1", 0},
		{"v1.0.0-rc1+meta", "v1.0.0-rc1+other", 0}, // build metadata is ignored
		{"v1.0.0+meta", "v1.0.0", 0},
		{"v1.0.0-a.b.c", "v1.0.0-a.b", 1},
		{"v1.0.0-RC1", "v1.0.0-rc1", -1}, // ASCII order: 'R' < 'r'
	}
	for _, tc := range cases {
		a, errA := Parse(tc.a)
		b, errB := Parse(tc.b)
		if errA != nil || errB != nil {
			t.Errorf("Parse(%q/%q): %v / %v", tc.a, tc.b, errA, errB)
			continue
		}
		got := Compare(a, b)
		if got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if rev := Compare(b, a); rev != -tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d (antisymmetry)", tc.b, tc.a, rev, -tc.want)
		}
	}
}

// TestProvenance pins the three-way classifier, including the BuildID
// fingerprint "dev+<size>@<mtime>" which MUST classify Unknown — never
// Local (it is not "dev") and never Release (it is not version-shaped).
func TestProvenance(t *testing.T) {
	cases := []struct {
		in   string
		want ProvenanceKind
	}{
		{"v0.9.9.1", ProvenanceRelease},
		{"0.9.5.2", ProvenanceRelease},
		{"v1.2.3-rc1", ProvenanceRelease},
		{"v1.2.3+build", ProvenanceRelease},
		{"24c6d74", ProvenanceLocal},
		{"abcdef0", ProvenanceLocal},
		{"0123456789abcdef0123456789abcdef01234567", ProvenanceLocal}, // 40-char full hash
		{"dev", ProvenanceLocal},
		{"dev+12345@1700000000", ProvenanceUnknown}, // BuildID fingerprint
		{"24c6d74 (dev)", ProvenanceUnknown},        // unstamped go-build output
		{"24c6d74 (dev)", ProvenanceUnknown},
		{"v007.1.1", ProvenanceRelease}, // release-SHAPED; Parse still rejects it
		{"v1.2.3.4.5", ProvenanceUnknown},
		{"", ProvenanceUnknown},
		{"latest", ProvenanceUnknown},
		{"v", ProvenanceUnknown},
		{"DeV", ProvenanceUnknown}, // case-sensitive
	}
	for _, tc := range cases {
		if got := Provenance(tc.in); got != tc.want {
			t.Errorf("Provenance(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestUpdateDecision pins the full policy table: local-build deny, both
// unparseable sides, upgrade allow, downgrade deny, equal no-op.
func TestUpdateDecision(t *testing.T) {
	cases := []struct {
		name       string
		cur, tgt   string
		wantAllow  bool
		wantNoop   bool
		wantDeny   bool
		wantKind   DenyKind
		wantReason string // substring the reason must contain
	}{
		{
			name: "local build refuses outright",
			cur:  "24c6d74", tgt: "v0.9.9.1",
			wantDeny: true, wantKind: DenyLocalBuild,
			wantReason: "local build",
		},
		{
			name: "dev refuses as local build",
			cur:  "dev", tgt: "v0.9.9.1",
			wantDeny: true, wantKind: DenyLocalBuild,
			wantReason: "local build",
		},
		{
			name: "unparseable current refuses",
			cur:  "dev+123@456", tgt: "v0.9.9.1",
			wantDeny: true, wantKind: DenyUnparseableCurrent,
			wantReason: "cannot verify installed version",
		},
		{
			name: "unparseable target refuses",
			cur:  "v0.9.9.1", tgt: "latest",
			wantDeny: true, wantKind: DenyUnparseableTarget,
			wantReason: "cannot verify target version",
		},
		{
			name: "hostile target refuses",
			cur:  "v0.9.9.1", tgt: "v1.2.3.4.5",
			wantDeny: true, wantKind: DenyUnparseableTarget,
			wantReason: "cannot verify target version",
		},
		{
			name: "newer target allows",
			cur:  "v0.9.5.2", tgt: "v0.9.9.1",
			wantAllow: true,
		},
		{
			name: "newer 4th-component target allows",
			cur:  "v0.9.9", tgt: "v0.9.9.1",
			wantAllow: true,
		},
		{
			name: "older target refuses as downgrade",
			cur:  "v0.9.9.1", tgt: "v0.9.5.2",
			wantDeny: true, wantKind: DenyDowngrade,
			wantReason: "--pin",
		},
		{
			name: "equal versions are a no-op",
			cur:  "v0.9.9.1", tgt: "v0.9.9.1",
			wantNoop: true,
		},
		{
			name: "equal with different v prefix is a no-op",
			cur:  "v0.9.9.1", tgt: "0.9.9.1",
			wantNoop: true,
		},
		{
			name: "prerelease target below installed release is a downgrade",
			cur:  "v0.9.9", tgt: "v0.9.9-rc1", // 0.9.9-rc1 < 0.9.9 (prerelease precedence)
			wantDeny: true, wantKind: DenyDowngrade,
			wantReason: "--pin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := UpdateDecision(tc.cur, tc.tgt)
			if d.Allow != tc.wantAllow {
				t.Errorf("UpdateDecision(%q,%q).Allow = %v, want %v", tc.cur, tc.tgt, d.Allow, tc.wantAllow)
			}
			if d.Noop != tc.wantNoop {
				t.Errorf("UpdateDecision(%q,%q).Noop = %v, want %v", tc.cur, tc.tgt, d.Noop, tc.wantNoop)
			}
			if d.Deny != tc.wantDeny {
				t.Errorf("UpdateDecision(%q,%q).Deny = %v, want %v", tc.cur, tc.tgt, d.Deny, tc.wantDeny)
			}
			if tc.wantDeny {
				if d.Kind != tc.wantKind {
					t.Errorf("UpdateDecision(%q,%q).Kind = %v, want %v", tc.cur, tc.tgt, d.Kind, tc.wantKind)
				}
				if !strings.Contains(d.Reason, tc.wantReason) {
					t.Errorf("UpdateDecision(%q,%q).Reason = %q, want substring %q", tc.cur, tc.tgt, d.Reason, tc.wantReason)
				}
			}
			// Exactly one outcome is set.
			n := 0
			for _, b := range []bool{d.Allow, d.Noop, d.Deny} {
				if b {
					n++
				}
			}
			if n != 1 {
				t.Errorf("UpdateDecision(%q,%q) sets %d outcomes, want exactly 1 (allow/noop/deny)", tc.cur, tc.tgt, n)
			}
		})
	}
}
