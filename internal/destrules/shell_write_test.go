package destrules

import (
	"strings"
	"testing"
)

// TestClassifyShellWrite pins the write-by-side-effect family: a redirect or
// truncate destroys content exactly like rm. The null-sink and quoted forms
// must stay exempt or routine commands would be gated.
func TestClassifyShellWrite(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		wantHit bool
		wantWhy string
	}{
		{"redirect new file", "echo hi > out.txt", true, "out.txt"},
		{"redirect append", "echo hi >> log.txt", true, "log.txt"},
		{"redirect with fd", "make 2> err.log", true, "err.log"},
		{"redirect to /dev/null", "make >/dev/null", false, ""},
		{"redirect stderr to /dev/null", "make 2>/dev/null", false, ""},
		{"fd duplication", "make > out.log 2>&1", true, "out.log"},
		{"quoted comparison", `python3 -c "if a > b: print(a)"`, false, ""},
		{"single-quoted redirect", `printf '%s' 'a > b'`, false, ""},
		{"truncate", "truncate -s0 data.db", true, "truncate destroys file content"},
		{"shred", "shred -u secret.key", true, "shred destroys file content"},
		{"mv home path", "mv ~/work/notes.md /tmp/", true, "relocates"},
		{"mv system path", "mv /etc/hosts /tmp/hosts.bak", true, "relocates"},
		{"mv relative path", "mv a.go b.go", false, ""},
		{"mv temp path", "mv /tmp/a /tmp/b", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := strings.Fields(tc.cmd)
			hit, why := ClassifyShellWrite(tokens[0], tc.cmd, tokens)
			if hit != tc.wantHit {
				t.Fatalf("ClassifyShellWrite(%q) hit = %v (%q), want %v", tc.cmd, hit, why, tc.wantHit)
			}
			if tc.wantHit && tc.wantWhy != "" && !strings.Contains(why, tc.wantWhy) {
				t.Fatalf("ClassifyShellWrite(%q) why = %q, want substring %q", tc.cmd, why, tc.wantWhy)
			}
			if !tc.wantHit && why != "" {
				t.Fatalf("ClassifyShellWrite(%q) returned a reason for a non-hit: %q", tc.cmd, why)
			}
		})
	}
}
