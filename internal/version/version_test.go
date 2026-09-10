package version

import "testing"

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
