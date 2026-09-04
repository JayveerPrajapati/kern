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
