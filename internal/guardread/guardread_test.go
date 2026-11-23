package guardread

import "testing"

// TestReadPipelineTiers pins the serve-plan decisions the guard hook and the
// kern guard classify CLI consume: tier 1 = index-answerable single read,
// tier 2 = read head + bounded tails, tier 3 = sandboxed verbatim read.
func TestReadPipelineTiers(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		tier    int
		head    string
		pattern string
		scope   string
		file    string
	}{
		{"unscoped symbol grep", "grep -rn NewServer", 1, "grep", "NewServer", "", ""},
		{"scoped symbol grep", "grep foo file.go", 1, "grep", "foo", "file.go", ""},
		{"bare cat", "cat source.go", 1, "cat", "", "", "source.go"},
		{"cat with flag", "cat -n source.go", 3, "", "", "", ""},
		{"find", "find . -name *.go", 1, "find", "", "", ""},
		{"pipe count", "grep foo file | wc -l", 2, "grep", "foo", "file", ""},
		{"cd forces tier 3", "cd internal && tail -5 README.md", 3, "", "", "", ""},
		{"windowed read", "head -3 README.md", 3, "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := ReadPipeline(tc.cmd)
			if res.Verdict != VerdictServe || res.Tier != tc.tier {
				t.Fatalf("ReadPipeline(%q) = %+v, want serve tier %d", tc.cmd, res, tc.tier)
			}
			if res.Head != tc.head || res.Pattern != tc.pattern || res.Scope != tc.scope || res.File != tc.file {
				t.Fatalf("ReadPipeline(%q) plan = %+v, want head=%q pattern=%q scope=%q file=%q",
					tc.cmd, res, tc.head, tc.pattern, tc.scope, tc.file)
			}
		})
	}
}

// TestReadOnlyPipelineRefusesWrites pins the write-riding guard at the unit
// seam: an in-place sed edit and a write is never a pure-read pipeline, while
// a wrapper-unwrapped read is.
func TestReadOnlyPipelineRefusesWrites(t *testing.T) {
	for _, cmd := range []string{
		"sed -i s/a/b/ internal/x.go",
		"echo hi > out.txt",
		"git add main.go",
		"git commit -m x",
		"kern ast Foo | head -5",
		"cat $(touch /tmp/kern-guard-pwned)",
		"cat `touch /tmp/kern-guard-pwned`",
		"awk 'BEGIN{system(\"touch /tmp/kern-guard-pwned\")}' /dev/null",
		"sed 'e touch /tmp/kern-guard-pwned' /dev/null",
	} {
		if ReadOnlyPipeline(cmd) {
			t.Errorf("ReadOnlyPipeline(%q) = true — writes must never ride the read serve", cmd)
		}
	}
	for _, cmd := range []string{
		"cat file.go | head -5",
		`sh -c "cat .gitignore"`,
		"git status && cat main.go",
		"find docs -type f | sort",
	} {
		if !ReadOnlyPipeline(cmd) {
			t.Errorf("ReadOnlyPipeline(%q) = false, want true", cmd)
		}
	}
}

// TestSplitStagesQuoteAwareness pins the separator semantics both halves rely
// on: separators are live only outside quotes, newlines are separators, and a
// quoted operator stays inside its stage.
func TestSplitStagesQuoteAwareness(t *testing.T) {
	got := SplitStages("grep -rn 'a|b' . && git status\ncat x")
	want := []string{"grep -rn 'a|b' .", "git status", "cat x"}
	if len(got) != len(want) {
		t.Fatalf("SplitStages = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SplitStages[%d] = %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
}

// TestIsSimpleCommandGitForms pins the read-only git whitelist that guards the
// raw-run path (read-only git runs raw; pushes and patch-emitting forms do
// not).
func TestIsSimpleCommandGitForms(t *testing.T) {
	simple := []string{"git status", "git log --oneline", "git diff --stat", "kern doctor", "pwd"}
	for _, cmd := range simple {
		if !IsSimpleCommand(cmd) {
			t.Errorf("IsSimpleCommand(%q) = false, want true", cmd)
		}
	}
	notSimple := []string{"git log -p", "git push origin main", "git diff HEAD~1", "rm -rf x", "curl http://x"}
	for _, cmd := range notSimple {
		if IsSimpleCommand(cmd) {
			t.Errorf("IsSimpleCommand(%q) = true, want false", cmd)
		}
	}
}

// TestIsGitShowDiffRead pins the audited rev-read class and its refusals
// (--output writes a file, --ext-diff/--textconv execute external programs).
func TestIsGitShowDiffRead(t *testing.T) {
	read := []string{"git show HEAD:file.go", "git diff HEAD~1", "git -C repo diff"}
	for _, cmd := range read {
		if !IsGitShowDiffRead(cmd) {
			t.Errorf("IsGitShowDiffRead(%q) = false, want true", cmd)
		}
	}
	notRead := []string{"git show --output=/tmp/x HEAD:file", "git diff --ext-diff", "git status", "ls"}
	for _, cmd := range notRead {
		if IsGitShowDiffRead(cmd) {
			t.Errorf("IsGitShowDiffRead(%q) = true, want false", cmd)
		}
	}
}
