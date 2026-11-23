package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdopt(t *testing.T) {
	// Save and restore the package-level Version var so tests are
	// independent of build-time ldflags stamping.
	orig := Version
	defer func() { Version = orig }()

	tests := []struct {
		name       string
		sharedVer  string
		compiledIn string
		want       string
	}{
		{
			name:       "dev source checkout, no stamps",
			sharedVer:  "dev",
			compiledIn: "dev",
			want:       "dev",
		},
		{
			name:       "dev compiledIn falls back to shared stamped version",
			sharedVer:  "v2.0.0",
			compiledIn: "dev",
			want:       "v2.0.0",
		},
		{
			name:       "legacy main.version stamp wins",
			sharedVer:  "dev",
			compiledIn: "v1.9.3",
			want:       "v1.9.3",
		},
		{
			name:       "legacy stamp beats shared stamp",
			sharedVer:  "v2.0.0",
			compiledIn: "v1.9.3",
			want:       "v1.9.3",
		},
		{
			name:       "empty string treated as stamped (passes through)",
			sharedVer:  "v2.0.0",
			compiledIn: "",
			want:       "",
		},
		{
			name:       "case-sensitive: Dev is not the dev sentinel",
			sharedVer:  "dev",
			compiledIn: "Dev",
			want:       "Dev",
		},
		{
			name:       "whitespace-padded string passes through",
			sharedVer:  "dev",
			compiledIn: " dev ",
			want:       " dev ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.sharedVer
			if got := Adopt(tt.compiledIn); got != tt.want {
				t.Errorf("Adopt(%q) with Version=%q = %q, want %q",
					tt.compiledIn, tt.sharedVer, got, tt.want)
			}
		})
	}
}

// TestAdoptFingerprint covers the fingerprint behavior: the same compiled-in
// input always resolves to the same effective version, and distinct inputs
// resolve to distinct versions (deterministic mapping).
func TestAdoptFingerprint(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()
	Version = "dev"

	// Same input -> same output.
	if got, want := Adopt("dev"), Adopt("dev"); got != want {
		t.Errorf("Adopt(\"dev\") not deterministic: %q != %q", got, want)
	}
	if got, want := Adopt("v1.2.3"), Adopt("v1.2.3"); got != want {
		t.Errorf("Adopt(\"v1.2.3\") not deterministic: %q != %q", got, want)
	}

	// Different inputs -> different outputs.
	seen := map[string]string{}
	inputs := []string{"dev", "", "v1.2.3", "v2.0.0", "Dev"}
	for _, in := range inputs {
		out := Adopt(in)
		if prev, ok := seen[out]; ok {
			t.Errorf("Adopt(%q) collides with Adopt(%q): both resolve to %q", prev, in, out)
		}
		seen[out] = in
	}
}

// TestDescribeStampedPassthrough (F-B1): a stamped version must pass
// through Describe unchanged — release binaries report exactly their tag.
func TestDescribeStampedPassthrough(t *testing.T) {
	for _, v := range []string{"v1.2.3", "9693283", "0.9.9.1", "Dev", " dev "} {
		if got := Describe(v); got != v {
			t.Errorf("Describe(%q) = %q, want it unchanged", v, got)
		}
	}
}

// TestDescribeDevCarriesCommitWhenKnown (F-B1): a still-"dev" version is
// decorated with the build's commit when one is derivable (toolchain
// buildinfo or a checkout's .git/HEAD), and stays "dev" only when neither
// exists. Never a bare "dev" next to a derivable commit.
func TestDescribeDevCarriesCommitWhenKnown(t *testing.T) {
	got := Describe("dev")
	if got == "" {
		t.Fatal("Describe(\"dev\") must never be empty")
	}
	if got == "dev" {
		if buildRevision() != "" || headShortHash() != "" {
			t.Fatalf("Describe(\"dev\") dropped a derivable commit: %q (buildinfo=%q git=%q)",
				got, buildRevision(), headShortHash())
		}
		return // git-less environment: bare "dev" is correct
	}
	if !strings.HasSuffix(got, " (dev)") {
		t.Errorf("described dev version should end with \" (dev)\", got %q", got)
	}
}

// TestReadHeadHash covers the .git reader behind Describe's fallback: a
// symbolic HEAD with a loose ref file, a packed ref, and a detached HEAD.
func TestReadHeadHash(t *testing.T) {
	full := strings.Repeat("a", 40)
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Symbolic HEAD with a loose ref file.
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "refs", "heads", "main"), []byte(full+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readHeadHash(gitDir); got != full[:7] {
		t.Errorf("loose ref: readHeadHash = %q, want %q", got, full[:7])
	}

	// Symbolic HEAD resolved through packed-refs (no loose file).
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/packed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "packed-refs"), []byte(full+" refs/heads/packed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readHeadHash(gitDir); got != full[:7] {
		t.Errorf("packed ref: readHeadHash = %q, want %q", got, full[:7])
	}

	// Detached HEAD: the hash itself.
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readHeadHash(gitDir); got != full[:7] {
		t.Errorf("detached: readHeadHash = %q, want %q", got, full[:7])
	}

	// Not a git dir at all.
	if got := readHeadHash(filepath.Join(dir, "nope")); got != "" {
		t.Errorf("missing dir: readHeadHash = %q, want empty", got)
	}
}

// TestShortRevision pins the 40-char -> 7-char shortening.
func TestShortRevision(t *testing.T) {
	full := strings.Repeat("0123456789", 4)
	if got := shortRevision(full); got != full[:7] {
		t.Errorf("shortRevision(40 chars) = %q, want %q", got, full[:7])
	}
	if got := shortRevision("abc"); got != "abc" {
		t.Errorf("shortRevision(\"abc\") = %q, want it unchanged", got)
	}
}
