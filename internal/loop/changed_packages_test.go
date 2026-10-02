package loop

import "testing"

func TestChangedGoPackages(t *testing.T) {
	cases := []struct {
		name string
		diff string
		want []string
	}{
		{name: "empty diff", diff: "", want: nil},
		{
			name: "single go file in nested package",
			diff: "diff --git a/internal/loop/loop.go b/internal/loop/loop.go\n--- a/internal/loop/loop.go\n+++ b/internal/loop/loop.go\n@@ -1 +1 @@\n-old\n+new\n",
			want: []string{"./internal/loop"},
		},
		{
			name: "two files same package dedupe + non-go ignored",
			diff: "+++ b/cmd/kern/dispatch.go\n+++ b/cmd/kern/helpers.go\n+++ b/README.md\n+++ b/cmd/kern/dispatch_test.go\n",
			want: []string{"./cmd/kern"},
		},
		{
			name: "root package file maps to dot",
			diff: "+++ b/main.go\n",
			want: []string{"."},
		},
		{
			name: "no go files at all",
			diff: "+++ b/docs/README.md\n+++ b/.kern/config.json\n",
			want: nil,
		},
		{
			name: "git tab-suffix stripped",
			diff: "+++ b/internal/validate/checks.go\t2026-10-01 10:00:00.000000000 +0000\n",
			want: []string{"./internal/validate"},
		},
		{
			name: "multiple packages keep order",
			diff: "+++ b/internal/a/a.go\n+++ b/internal/b/b.go\n",
			want: []string{"./internal/a", "./internal/b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := changedGoPackages(tc.diff)
			if len(got) != len(tc.want) {
				t.Fatalf("changedGoPackages(%q) = %v, want %v", tc.diff, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("changedGoPackages(%q)[%d] = %q, want %q", tc.diff, i, got[i], tc.want[i])
				}
			}
		})
	}
}
