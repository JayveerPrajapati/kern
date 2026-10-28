package destructive

import (
	"strings"
	"testing"
)

// TestPredicateForwarding pins the read-tool-carries-a-write family. These are
// the forms the exec firewall must gate; a silent miss turns `find` or `xargs`
// into an ungoverned delete.
func TestPredicateForwarding(t *testing.T) {
	cases := []struct {
		name    string
		command string
		rest    []string
		wantHit bool
		wantWhy string
	}{
		{"find delete", "find", []string{".", "-name", "*.tmp", "-delete"}, true, "removes matched entries"},
		{"find exec rm", "find", []string{".", "-exec", "rm", "{}", ";"}, true, "removes/overwrites entries"},
		{"find exec grep", "find", []string{".", "-exec", "grep", "x", "{}", ";"}, false, ""},
		{"find fprint", "find", []string{".", "-fprint", "out.txt"}, true, "writes files"},
		{"find plain", "find", []string{".", "-name", "*.go"}, false, ""},
		{"xargs rm", "xargs", []string{"rm", "-f"}, true, "removes/overwrites entries"},
		{"xargs grep", "xargs", []string{"grep", "needle"}, false, ""},
		{"tee file", "tee", []string{"out.txt"}, true, "overwrites out.txt"},
		{"tee dev null", "tee", []string{"/dev/null"}, false, ""},
		{"shred", "shred", []string{"-u", "key"}, true, "overwrites files"},
		{"dd of file", "dd", []string{"if=a", "of=disk.img"}, true, "writes disk.img"},
		{"dd of dev null", "dd", []string{"if=a", "of=/dev/null"}, false, ""},
		{"git clean fd", "git", []string{"clean", "-fd"}, true, "removes untracked files"},
		{"git reset hard", "git", []string{"reset", "--hard"}, true, "discards working-tree changes"},
		{"git status", "git", []string{"status"}, false, ""},
		{"plain ls", "ls", []string{"-la"}, false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, why := PredicateForwarding(tc.command, tc.rest)
			if hit != tc.wantHit {
				t.Fatalf("PredicateForwarding(%q, %v) hit = %v (%q), want %v", tc.command, tc.rest, hit, why, tc.wantHit)
			}
			if tc.wantHit && tc.wantWhy != "" && !strings.Contains(why, tc.wantWhy) {
				t.Fatalf("why = %q, want substring %q", why, tc.wantWhy)
			}
		})
	}
}

func TestIsDestructiveExecutable(t *testing.T) {
	for _, name := range []string{"rm", "shred", "truncate", "dd", "mv", "tee", "/bin/rm", "/usr/bin/truncate"} {
		if !IsDestructiveExecutable(name) {
			t.Errorf("IsDestructiveExecutable(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"grep", "ls", "cat", "git"} {
		if IsDestructiveExecutable(name) {
			t.Errorf("IsDestructiveExecutable(%q) = true, want false", name)
		}
	}
}
