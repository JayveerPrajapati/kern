package main

import (
	"strings"
	"testing"
)

// kern update delegates to install.sh (network) — only the argument
// validation and the fail-closed policy decisions are unit-testable without
// touching the network. The full install.sh path is covered by
// update_e2e_test.go (file:// fixture, skipped under -short).

// saveVersion temporarily overrides the package version var (normally set by
// init() from internal/version.Version) and restores it after the test.
func saveVersion(t *testing.T, v string) {
	t.Helper()
	orig := version
	t.Cleanup(func() { version = orig })
	version = v
}

func TestUpdateRejectsPositionalArgs(t *testing.T) {
	expectExit(t, 2, func() {
		runUpdate([]string{"unexpected-arg"})
	})
}

// TestUpdatePreflightLocalBuildRefuses is the #561 unit repro: a Makefile
// short-hash stamp must refuse the update with rebuild guidance and exit 3
// (policy deny) — never fall through as "not installed".
func TestUpdatePreflightLocalBuildRefuses(t *testing.T) {
	saveVersion(t, "24c6d74")
	out, code := captureStderrExit(t, func() {
		runUpdate([]string{"--preflight", "v0.9.9.1"})
	})
	if code != 3 {
		t.Fatalf("preflight(local build) exit = %d, want 3", code)
	}
	for _, want := range []string{"local build", "make build && make install", "--force"} {
		if !strings.Contains(out, want) {
			t.Errorf("preflight stderr missing %q:\n%s", want, out)
		}
	}
}

// TestUpdatePreflightMissingTagIsUsage: --preflight without a tag is a usage
// error (exit 2), matching the documented 0/3/2 exit contract.
func TestUpdatePreflightMissingTagIsUsage(t *testing.T) {
	expectExit(t, 2, func() {
		runUpdate([]string{"--preflight"})
	})
}

// TestUpdatePreflightUpgradeAllows: an older release install allows the
// newer target (exit 0, "allow" on stdout).
func TestUpdatePreflightUpgradeAllows(t *testing.T) {
	saveVersion(t, "v0.9.5.2")
	out := captureStdout(t, func() {
		runUpdate([]string{"--preflight", "v0.9.9.1"})
	})
	if !strings.Contains(out, "allow") {
		t.Fatalf("preflight output = %q, want allow", out)
	}
}

// TestUpdatePreflightEqualIsNoop: equal versions exit 0 with a no-op
// verdict, never a deny.
func TestUpdatePreflightEqualIsNoop(t *testing.T) {
	saveVersion(t, "v0.9.9.1")
	out := captureStdout(t, func() {
		runUpdate([]string{"--preflight", "v0.9.9.1"})
	})
	if !strings.Contains(out, "no-op") {
		t.Fatalf("preflight output = %q, want no-op", out)
	}
}

// TestUpdatePreflightDowngradeDenies: a target older than the installed
// version is refused with --pin guidance and exit 3.
func TestUpdatePreflightDowngradeDenies(t *testing.T) {
	saveVersion(t, "v0.9.9.1")
	out, code := captureStderrExit(t, func() {
		runUpdate([]string{"--preflight", "v0.9.5.2"})
	})
	if code != 3 {
		t.Fatalf("preflight(downgrade) exit = %d, want 3", code)
	}
	if !strings.Contains(out, "--pin") {
		t.Fatalf("preflight stderr missing --pin guidance:\n%s", out)
	}
}

// TestUpdatePreflightPinConsentsDowngrade: KERN_PIN=1 (set by `kern update
// --pin <tag>`) turns the downgrade deny into an allow — the deliberate
// downgrade path the deny reason names.
func TestUpdatePreflightPinConsentsDowngrade(t *testing.T) {
	t.Setenv("KERN_PIN", "1")
	saveVersion(t, "v0.9.9.1")
	out := captureStdout(t, func() {
		runUpdate([]string{"--preflight", "v0.9.5.2"})
	})
	if !strings.Contains(out, "deliberate pin downgrade") {
		t.Fatalf("preflight output = %q, want deliberate pin downgrade allow", out)
	}
}

// TestUpdatePreflightPinDoesNotConsentLocalBuild: KERN_PIN only consents to
// the downgrade it names — a local-build installed version still refuses.
func TestUpdatePreflightPinDoesNotConsentLocalBuild(t *testing.T) {
	t.Setenv("KERN_PIN", "1")
	saveVersion(t, "deadbeef")
	_, code := captureStderrExit(t, func() {
		runUpdate([]string{"--preflight", "v0.9.9.1"})
	})
	if code != 3 {
		t.Fatalf("preflight(local build + KERN_PIN) exit = %d, want 3", code)
	}
}

// TestUpdateLocalBuildRefusesBeforeNetwork is the #561 main-path repro: a
// hash-stamped binary must refuse `kern update` (no --force) BEFORE any
// network access — the guard fires before the curl lookup, so this test
// would fail loudly if the ordering regressed (curl would not be required).
func TestUpdateLocalBuildRefusesBeforeNetwork(t *testing.T) {
	saveVersion(t, "24c6d74")
	out, code := captureStderrExit(t, func() {
		runUpdate([]string{})
	})
	if code != 3 {
		t.Fatalf("update(local build) exit = %d, want 3", code)
	}
	for _, want := range []string{"local build", "make build && make install", "--force"} {
		if !strings.Contains(out, want) {
			t.Errorf("update stderr missing %q:\n%s", want, out)
		}
	}
}

// TestUpdateUnverifiableVersionRefusesBeforeNetwork: an Unknown-provenance
// installed version (e.g. BuildID's "dev+<size>@<mtime>" fingerprint) also
// refuses before the network — fail-closed, same as Local.
func TestUpdateUnverifiableVersionRefusesBeforeNetwork(t *testing.T) {
	saveVersion(t, "dev+12345@1700000000")
	out, code := captureStderrExit(t, func() {
		runUpdate([]string{})
	})
	if code != 3 {
		t.Fatalf("update(unverifiable) exit = %d, want 3", code)
	}
	if !strings.Contains(out, "cannot verify installed version") {
		t.Fatalf("update stderr missing cannot-verify guidance:\n%s", out)
	}
}

// TestUpdateDecisionPreview pins the --dry-run preview rendering: it is a
// pure function over the same guards (updateGuardDenial / UpdateDecision),
// so these tests never exec anything — no curl, no install.sh.
func TestUpdateDecisionPreview(t *testing.T) {
	cases := []struct {
		name      string
		installed string
		f         flags
		want      []string // substrings the preview must contain
		notWant   []string // substrings that must NOT appear
	}{
		{
			name:      "no-pin release allow",
			installed: "v0.9.5.2",
			f:         flags{},
			want:      []string{"v0.9.5.2 (Release)", "latest (resolved by installer)", "decision:   allow"},
		},
		{
			name:      "no-pin local-build deny",
			installed: "24c6d74",
			f:         flags{},
			want:      []string{"24c6d74 (Local)", "latest (resolved by installer)", "deny:", "local build", "make build && make install"},
		},
		{
			name:      "no-pin unknown deny",
			installed: "dev+123@456",
			f:         flags{},
			want:      []string{"dev+123@456 (Unknown)", "deny:", "cannot verify installed version"},
		},
		{
			name:      "no-pin force override",
			installed: "24c6d74",
			f:         flags{force: true},
			want:      []string{"24c6d74 (Local)", "allow (--force override)"},
			notWant:   []string{"deny:"},
		},
		{
			name:      "pin upgrade allow",
			installed: "v0.9.5.2",
			f:         flags{pin: "v0.9.9.1"},
			want:      []string{"v0.9.5.2 (Release)", "v0.9.9.1", "decision:   allow"},
		},
		{
			name:      "pin equal no-op",
			installed: "v0.9.9.1",
			f:         flags{pin: "v0.9.9.1"},
			want:      []string{"no-op — already at v0.9.9.1"},
		},
		{
			name:      "pin downgrade deny",
			installed: "v0.9.9.1",
			f:         flags{pin: "v0.9.5.2"},
			want:      []string{"deny:", "v0.9.9.1 is newer than v0.9.5.2", "--pin"},
		},
		{
			name:      "pin local-build deny with force annotation",
			installed: "24c6d74",
			f:         flags{pin: "v0.9.9.1", force: true},
			want:      []string{"deny:", "local build", "overridden by --force"},
		},
		{
			name:      "pin unparseable target deny",
			installed: "v0.9.9.1",
			f:         flags{pin: "latest"},
			want:      []string{"deny:", "cannot verify target version"},
		},
		{
			name:      "channel stable target line",
			installed: "v0.9.5.2",
			f:         flags{channel: "stable"},
			want:      []string{"v0.9.5.2 (Release)", "stable channel (resolved by installer)", "decision:   allow"},
		},
		{
			name:      "channel regex target line",
			installed: "v0.9.5.2",
			f:         flags{channel: "^v0\\.9\\."},
			want:      []string{"^v0\\.9\\. channel (resolved by installer)"},
		},
		{
			name:      "channel latest target line",
			installed: "v0.9.5.2",
			f:         flags{channel: "latest"},
			want:      []string{"latest channel (resolved by installer)"},
		},
		{
			name:      "pin overrides channel in target line",
			installed: "v0.9.5.2",
			f:         flags{pin: "v0.9.9.1", channel: "stable"},
			want:      []string{"v0.9.9.1"},
			notWant:   []string{"stable channel"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := updateDecisionPreview(tc.installed, tc.f)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("preview(%q) missing %q:\n%s", tc.installed, want, got)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(got, nw) {
					t.Errorf("preview(%q) contains unwanted %q:\n%s", tc.installed, nw, got)
				}
			}
		})
	}
}

// TestUpdateChildEnvForwardsChannel pins the Stage C env forwarding: the
// installer child must receive KERN_CHANNEL=<channel> (stable|latest|regex)
// so install.sh's get_version can resolve the channel target. --pin
// overrides the channel entirely (KERN_VERSION short-circuits get_version,
// so KERN_CHANNEL is not forwarded), and a pre-existing inherited
// KERN_CHANNEL is dropped before the flag's value is appended — on Unix a
// duplicated execve variable resolves to the FIRST occurrence, so an
// appended value behind an inherited one would be silently shadowed.
// updateChildEnv is pure (no exec, no network), matching the Stage A guard
// test pattern.
func TestUpdateChildEnvForwardsChannel(t *testing.T) {
	t.Setenv("KERN_CHANNEL", "inherited")
	hasKV := func(env []string, kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	cases := []struct {
		name    string
		f       flags
		want    []string // entries that must be present
		notWant []string // entries that must NOT be present
	}{
		{
			name:    "stable forwarded",
			f:       flags{channel: "stable"},
			want:    []string{"KERN_CHANNEL=stable"},
			notWant: []string{"KERN_CHANNEL=inherited"},
		},
		{
			name:    "regex forwarded verbatim",
			f:       flags{channel: `^v0\.9\.`},
			want:    []string{"KERN_CHANNEL=^v0\\.9\\."},
			notWant: []string{"KERN_CHANNEL=inherited"},
		},
		{
			name:    "latest forwarded",
			f:       flags{channel: "latest"},
			want:    []string{"KERN_CHANNEL=latest"},
			notWant: []string{"KERN_CHANNEL=inherited"},
		},
		{
			name:    "pin overrides channel",
			f:       flags{pin: "v0.9.9.1", channel: "stable"},
			want:    []string{"KERN_VERSION=v0.9.9.1", "KERN_PIN=1"},
			notWant: []string{"KERN_CHANNEL=stable"}, // the flag's channel is NOT forwarded
		},
		{
			name:    "force coexists with channel",
			f:       flags{force: true, channel: "stable"},
			want:    []string{"KERN_FORCE=1", "KERN_CHANNEL=stable"},
			notWant: []string{"KERN_CHANNEL=inherited"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := updateChildEnv(tc.f)
			for _, want := range tc.want {
				if !hasKV(env, want) {
					t.Errorf("child env missing %q: %v", want, env)
				}
			}
			for _, nw := range tc.notWant {
				if hasKV(env, nw) {
					t.Errorf("child env contains unwanted %q: %v", nw, env)
				}
			}
		})
	}
	// No channel: the inherited KERN_CHANNEL passes through untouched (the
	// env-passthrough contract — only a forwarded value replaces it).
	env := updateChildEnv(flags{})
	if !hasKV(env, "KERN_CHANNEL=inherited") {
		t.Errorf("no-channel child env must pass the inherited KERN_CHANNEL through, got: %v", env)
	}
}

// TestUpdateGuardReleaseVersionPasses: a release-shaped installed version
// (the normal older-release update path) must NOT be refused by the local
// guard — zero behavior change for the normal path. The guard only fires
// for Local/Unknown provenance. updateGuardDenial is pure (no exec, no
// network) — deliberately, so this test can never touch the real update
// path the way a runUpdate invocation could.
func TestUpdateGuardReleaseVersionPasses(t *testing.T) {
	for _, v := range []string{"v0.9.5.2", "v0.9.9.1", "0.9.5.2"} {
		if msg := updateGuardDenial(v, flags{}); msg != "" {
			t.Errorf("updateGuardDenial(%q) = %q, want pass-through", v, msg)
		}
	}
}

// TestUpdateGuardLocalAndUnknownDeny: Local (hash/dev) and Unknown
// (BuildID fingerprint) installed versions refuse without --force, and
// --force is the only override.
func TestUpdateGuardLocalAndUnknownDeny(t *testing.T) {
	deny := map[string]string{
		"24c6d74":       "local build",
		"deadbeef":      "local build",
		"dev":           "local build",
		"dev+123@456":   "cannot verify installed version",
		"24c6d74 (dev)": "cannot verify installed version",
	}
	for v, want := range deny {
		msg := updateGuardDenial(v, flags{})
		if !strings.Contains(msg, want) {
			t.Errorf("updateGuardDenial(%q) = %q, want substring %q", v, msg, want)
		}
		if msg == "" {
			t.Errorf("updateGuardDenial(%q) = \"\", want refusal", v)
		}
		// --force overrides every denial.
		if msg := updateGuardDenial(v, flags{force: true}); msg != "" {
			t.Errorf("updateGuardDenial(%q, force) = %q, want pass-through", v, msg)
		}
	}
}

// TestUpdateGuardPinSemantics: with --pin the full UpdateDecision runs — a
// downgrade is consented (the pin names it), but a local-build or
// unparseable installed version still refuses unless --force.
func TestUpdateGuardPinSemantics(t *testing.T) {
	// Release installed, pin to an older tag: the deliberate downgrade
	// passes the guard (the preflight inside install.sh re-checks via
	// KERN_PIN=1).
	if msg := updateGuardDenial("v0.9.9.1", flags{pin: "v0.9.5.2"}); msg != "" {
		t.Errorf("pin downgrade denied: %q", msg)
	}
	// Local build + pin: still refused (pin does not consent to overwriting
	// a local build) unless --force.
	if msg := updateGuardDenial("24c6d74", flags{pin: "v0.9.9.1"}); !strings.Contains(msg, "local build") {
		t.Errorf("pin + local build = %q, want local-build refusal", msg)
	}
	if msg := updateGuardDenial("24c6d74", flags{pin: "v0.9.9.1", force: true}); msg != "" {
		t.Errorf("pin + local build + force = %q, want pass-through", msg)
	}
	// Unparseable target + no force: refused.
	if msg := updateGuardDenial("v0.9.9.1", flags{pin: "latest"}); !strings.Contains(msg, "cannot verify target version") {
		t.Errorf("pin to unparseable target = %q, want refusal", msg)
	}
}
