package version

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input     string
		wantMajor int
		wantMinor int
		wantPatch int
		wantErr   bool
	}{
		{"v0.9.0", 0, 9, 0, false},
		{"0.9.0", 0, 9, 0, false},
		{"v1.2.3", 1, 2, 3, false},
		{"v0.7.5", 0, 7, 5, false},
		{"dev", 0, 0, 0, true},
		{"v0.9", 0, 0, 0, true},
		{"v0.9.7-local", 0, 9, 7, false},
		{"v0.9.7-dirty", 0, 9, 7, false},
		{"1.0.0-rc1", 1, 0, 0, false},
		{"0.9.7+build.1", 0, 9, 7, false},
		{"v1.2.3-rc1+meta", 1, 2, 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			major, minor, patch, err := ParseVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseVersion(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if major != tt.wantMajor || minor != tt.wantMinor || patch != tt.wantPatch {
					t.Errorf("ParseVersion(%q) = %d.%d.%d, want %d.%d.%d",
						tt.input, major, minor, patch, tt.wantMajor, tt.wantMinor, tt.wantPatch)
				}
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		installed string
		required  string
		want      bool
	}{
		{"v0.9.0", "v0.9.0", true},
		{"v0.9.1", "v0.9.0", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v0.9.0", true},
		{"v0.8.0", "v0.9.0", false},
		{"v0.7.5", "v0.9.0", false},
		{"dev", "v0.9.0", true},
		{"v0.9.0", "dev", false},
		{"v0.9.7-local", "v0.9.0", true},
		{"v0.9.7-local", "v0.9.7", true},
		{"v0.8.9-local", "v0.9.0", false},
		{"v1.0.0-rc1", "v0.9.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.installed+"_vs_"+tt.required, func(t *testing.T) {
			got := VersionAtLeast(tt.installed, tt.required)
			if got != tt.want {
				t.Errorf("VersionAtLeast(%q, %q) = %v, want %v", tt.installed, tt.required, got, tt.want)
			}
		})
	}
}

func TestVersionAtLeastCommitHashStamp(t *testing.T) {
	// Default `make build` stamps the HEAD short hash; checkout builds must
	// satisfy the minimum like "dev" (not report "too old").
	for _, v := range []string{"ccdaae1", "a1b2c3d", "0123456789abcdef0123456789abcdef01234567", "ccdaae1 (dev)", "a1b2c3d (dev)", "0123456789abcdef0123456789abcdef01234567 (dev)"} {
		if !VersionAtLeast(v, MinKernVersion) {
			t.Errorf("commit-hash stamp %q must satisfy minimum %s", v, MinKernVersion)
		}
	}
	// Release tags still compare normally.
	if VersionAtLeast("v0.8.0", MinKernVersion) {
		t.Error("v0.8.0 must be below v0.9.0")
	}
}

// TestVersionAtLeastFourComponentTags is the Stage B regression: this repo
// ships 4-component release tags (v0.9.9.1), and the lenient ParseVersion
// (first three components) must still satisfy a v0.9.0 minimum — the
// classification dedupe onto kversion.Provenance must not disturb the
// release-tag comparison path.
func TestVersionAtLeastFourComponentTags(t *testing.T) {
	cases := []struct {
		installed string
		required  string
		want      bool
	}{
		{"v0.9.9.1", "v0.9.0", true},
		{"v0.9.9.1", "v0.9.9", true},
		{"v0.9.9.1", "v0.9.9.1", true},
		{"v0.9.9", "v0.9.9.1", true}, // lenient: 0.9.9 >= 0.9.9
		{"v0.9.9.1", "v0.10.0", false},
		{"v0.9.0.1", "v0.9.0", true},
		{"v0.8.9.1", "v0.9.0", false},
	}
	for _, tt := range cases {
		t.Run(tt.installed+"_vs_"+tt.required, func(t *testing.T) {
			if got := VersionAtLeast(tt.installed, tt.required); got != tt.want {
				t.Errorf("VersionAtLeast(%q, %q) = %v, want %v", tt.installed, tt.required, got, tt.want)
			}
		})
	}
}

// TestVersionAtLeastLocalAndChannelSuffix pins the deduped classification:
// "dev", bare hashes and channel-suffixed "<hash> (dev)" reports satisfy any
// minimum via kversion.Provenance == Local — identical semantics to the
// private commitHashRe the dedupe replaced.
func TestVersionAtLeastLocalAndChannelSuffix(t *testing.T) {
	for _, v := range []string{"dev", "deadbeef", "deadbeef (dev)", "abc1234 (dev)", "0123456789abcdef0123456789abcdef01234567 (dev)"} {
		if !VersionAtLeast(v, MinKernVersion) {
			t.Errorf("local stamp %q must satisfy minimum %s", v, MinKernVersion)
		}
	}
	// A channel-suffixed RELEASE shape is still compared normally ("v0.8.9
	// (dev)" parses to 0.8.9 and fails the 0.9.0 minimum).
	if VersionAtLeast("v0.8.9 (dev)", MinKernVersion) {
		t.Error("v0.8.9 (dev) must be below v0.9.0")
	}
}
