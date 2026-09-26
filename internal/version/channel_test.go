package version

import (
	"strings"
	"testing"
)

// TestResolveChannel pins the channel semantics: ""/latest = highest
// overall (4-component hotfixes included), stable = highest 3-component tag
// (hotfixes excluded), any other value = regex over tag names with a
// fail-closed no-match/invalid-regex error. Every comparison goes through
// Compare, never x/semver.
func TestResolveChannel(t *testing.T) {
	mixed := []string{"v0.9.5.2", "v0.9.9", "v0.9.9.1", "v1.0.0-rc1", "v1.0.0"}
	cases := []struct {
		name    string
		tags    []string
		channel string
		want    string // resolved tag
		wantErr string // error substring; "" = must succeed
	}{
		{
			name: "empty channel is latest",
			tags: mixed, channel: "",
			want: "v1.0.0",
		},
		{
			name: "latest explicit",
			tags: mixed, channel: "latest",
			want: "v1.0.0",
		},
		{
			name: "latest includes 4-component hotfix",
			tags: []string{"v0.9.9", "v0.9.9.1"}, channel: "latest",
			want: "v0.9.9.1",
		},
		{
			name: "stable excludes 4-component hotfix",
			tags: []string{"v0.9.9", "v0.9.9.1"}, channel: "stable",
			want: "v0.9.9",
		},
		{
			name: "stable picks max 3-component",
			tags: []string{"v0.9.5.2", "v0.9.9.1", "v0.9.9"}, channel: "stable",
			want: "v0.9.9",
		},
		{
			name: "stable max across version lines",
			tags: []string{"v0.9.9.1", "v0.10.0", "v0.9.9"}, channel: "stable",
			want: "v0.10.0",
		},
		{
			name: "stable prefers release over its prerelease",
			tags: []string{"v1.0.0-rc1", "v1.0.0"}, channel: "stable",
			want: "v1.0.0",
		},
		{
			name: "stable with only a prerelease resolves it",
			tags: []string{"v1.0.0-rc1"}, channel: "stable",
			want: "v1.0.0-rc1",
		},
		{
			name: "regex matches hotfix line",
			tags: mixed, channel: `^v0\.9\.`,
			want: "v0.9.9.1",
		},
		{
			name: "regex anchored to 3 components",
			tags: mixed, channel: `^v0\.9\.[0-9]+$`,
			want: "v0.9.9",
		},
		{
			name: "regex over major line",
			tags: mixed, channel: `^v1\.`,
			want: "v1.0.0",
		},
		{
			name: "regex no match errors",
			tags: mixed, channel: `^v2\.`,
			wantErr: "no release tags match",
		},
		{
			name: "invalid regex errors",
			tags: mixed, channel: `[`,
			wantErr: "invalid channel regex",
		},
		{
			name: "empty tags errors",
			tags: nil, channel: "latest",
			wantErr: "no release tags available",
		},
		{
			name: "stable with only hotfixes errors",
			tags: []string{"v0.9.9.1", "v0.9.9.2"}, channel: "stable",
			wantErr: "no 3-component",
		},
		{
			name: "unparseable tags are skipped",
			tags: []string{"v0.9.9", "not-a-tag", "v0.9.9.1"}, channel: "stable",
			want: "v0.9.9",
		},
		{
			name: "all unparseable tags error",
			tags: []string{"foo", "bar"}, channel: "latest",
			wantErr: "no tags parse as release versions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveChannel(tc.tags, tc.channel)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveChannel(%v, %q) = %q, want error containing %q", tc.tags, tc.channel, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ResolveChannel(%v, %q) error = %q, want substring %q", tc.tags, tc.channel, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveChannel(%v, %q) error: %v", tc.tags, tc.channel, err)
			}
			if got != tc.want {
				t.Errorf("ResolveChannel(%v, %q) = %q, want %q", tc.tags, tc.channel, got, tc.want)
			}
		})
	}
}

// TestResolveChannelConsistentWithCompare: the resolver's "highest" must be
// the same tag Compare would rank above every other candidate — a sanity
// cross-check that the max scan and the comparison agree.
func TestResolveChannelConsistentWithCompare(t *testing.T) {
	tags := []string{"v0.9.5.2", "v0.9.9", "v0.9.9.1", "v1.0.0-rc1", "v1.0.0"}
	got, err := ResolveChannel(tags, "")
	if err != nil {
		t.Fatal(err)
	}
	want, err := Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range tags {
		v, err := Parse(tag)
		if err != nil {
			continue
		}
		if Compare(v, want) > 0 {
			t.Errorf("%s ranks above the resolved %s — the max scan and Compare disagree", tag, got)
		}
	}
}

// TestResolveChannelRejectsRE2OnlySyntax covers the finding-9 ERE gate: a
// channel regex using RE2-only constructs (\d, \w, (?i), ...) is rejected
// with the ERE spelling recommended, because install.sh's grep -E would
// resolve the same pattern to a different (or empty) release set. ERE-safe
// spellings ([0-9], [A-Za-z0-9_]) keep working.
func TestResolveChannelRejectsRE2OnlySyntax(t *testing.T) {
	tags := []string{"v0.9.9", "v1.0.0"}
	for _, ch := range []string{`^v\d\.`, `^v\w+$`, `(?i)^V1`, `^v1\.(?:\d)`} {
		if _, err := ResolveChannel(tags, ch); err == nil {
			t.Errorf("ResolveChannel(%q) must reject the RE2-only construct", ch)
		} else if !strings.Contains(err.Error(), "ERE") {
			t.Errorf("ResolveChannel(%q) error must recommend the ERE spelling, got: %v", ch, err)
		}
	}
	// ERE-safe spellings still resolve.
	if got, err := ResolveChannel(tags, `^v[0-9]\.`); err != nil || got != "v1.0.0" {
		t.Errorf("ERE-safe channel ^v[0-9]\\. = %q, %v; want v1.0.0", got, err)
	}
}
