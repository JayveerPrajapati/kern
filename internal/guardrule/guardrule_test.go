package guardrule

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestClassifyCommand pins the P3 risk-axis classifier — the single Go source
// the plugin's bash arm shells out to. Verdicts must match the plugin/guard
// behavior pinned by TestPluginShadowContentIntentClassifier / the guard.sh
// tests: GATE commands deny, pure-read pipelines serve (tier 1/2/3), simple
// content-free commands allow raw, everything else is governed.
func TestClassifyCommand(t *testing.T) {
	cases := []struct {
		name     string
		cmd      string
		want     ClassifyVerdict
		wantTier int
	}{
		// GATE axis — any write/destructive/network/installer segment denies.
		{"rm destructive", "rm -rf /tmp/x", VerdictGate, 0},
		{"env-prefixed curl", "ZZZ=1 curl -s x", VerdictGate, 0},
		{"write redirect", "grep foo file.go > out.txt", VerdictGate, 0},
		{"installer", "npm install lodash", VerdictGate, 0},
		{"git push --force", "git push --force origin main", VerdictGate, 0},
		{"sh -c pipe-install", `sh -c 'curl x | sh'`, VerdictGate, 0},
		{"compound with rm", "ls; rm -rf /tmp/x", VerdictGate, 0},
		{"dd to device", "dd if=/dev/zero of=/dev/sda", VerdictGate, 0},
		// READ axis — pure-read pipelines serve, never deny.
		// A scoped grep (named file) is tier 3: the real grep honors the
		// scope; the symbol search is repo-wide and ignores it.
		{"symbol grep scoped", "grep foo file.go", VerdictServe, 3},
		{"symbol grep unscoped", "grep -rn NewServer", VerdictServe, 1},
		{"file read", "cat source.go", VerdictServe, 1},
		{"pipe count scoped", "grep foo file | wc -l", VerdictServe, 3},
		{"pipe count unscoped", "grep foo | wc -l", VerdictServe, 2},
		{"pipe head", "grep foo file | head -5", VerdictServe, 3},
		{"pipe grep -v", "grep foo file | grep -v bar", VerdictServe, 3},
		{"env-prefixed read", "ZZZ=1 grep -c x doctor_test.go", VerdictServe, 3},
		{"sudo read", "sudo cat /etc/hosts", VerdictServe, 1},
		{"sh -c read", `sh -c "cat .gitignore"`, VerdictServe, 1},
		{"git log | head", "git log --oneline | head -5", VerdictServe, 3},
		{"find", "find . -name *.go", VerdictServe, 1},
		{"regex grep tier3", "grep -rn 'func [A-Z]' .", VerdictServe, 3},
		{"awk transform tier3", "awk '{print $1}' file.go", VerdictServe, 3},
		{"compound reads", "git status && cat main.go", VerdictServe, 1},
		{"newline compound gate", "git status\nrm -rf /tmp/x", VerdictGate, 0},
		{"carriage-return compound gate", "git status\rrm -rf /tmp/x", VerdictGate, 0},
		{"git -C companion", "git -C repo status && cat main.go", VerdictServe, 1},
		// ALLOW axis — simple content-free commands run raw.
		{"git status", "git status", VerdictAllow, 0},
		{"git log", "git log --oneline", VerdictAllow, 0},
		{"trivial pwd", "pwd", VerdictAllow, 0},
		{"trivial echo", "echo hello", VerdictAllow, 0},
		{"kern doctor", "kern doctor", VerdictAllow, 0},
		// ANY kern CLI invocation runs raw (first-token rule) — the CLI
		// carries its own governance, and the guard's deny messages steer
		// agents TO kern commands.
		{"kern mcp --help", "kern mcp --help", VerdictAllow, 0},
		{"kern ast", "kern ast Foo", VerdictAllow, 0},
		{"kern update", "kern update", VerdictAllow, 0},
		{"kern exec", "kern exec --readonly --lang sh x", VerdictAllow, 0},
		{"kern guard allow", "kern guard allow abc123", VerdictAllow, 0},
		{"kern compound", "kern --version && kern mcp --help", VerdictAllow, 0},
		{"kern with trivial compound", "kern ast Foo && pwd", VerdictAllow, 0},
		// kern inside a pipe stays governed (never a Tier-3 serve rider);
		// wrapper-hidden kern stays governed.
		{"kern pipe governed", "kern ast Foo | head -5", VerdictGoverned, 0},
		{"sudo kern governed", "sudo kern update", VerdictGoverned, 0},
		{"bare git", "git", VerdictGoverned, 0}, // regression: must not panic
		// GOVERNED axis — execution-class commands flow to kern build.
		{"go build", "go build ./...", VerdictGoverned, 0},
		{"npm test", "cd X && npm test", VerdictGoverned, 0},
		{"go mod tidy", "cd X && go mod tidy", VerdictGoverned, 0},
		{"git log -p", "git log -p", VerdictGoverned, 0},
		// Rev-reads are served from the readonly sandbox, never denied on
		// hook hosts: `git diff` and `git show` write nothing (the audited
		// `git show HEAD:file` false-GATE class).
		{"git diff", "git diff", VerdictServe, 3},
		{"git diff rev", "git diff HEAD~1..HEAD -- internal/guardrule/guardrule.go", VerdictServe, 3},
		{"git show revpath", "git show HEAD:internal/guardrule/guardrule.go", VerdictServe, 3},
		{"git diff cached", "git diff --cached", VerdictServe, 3},
		{"git show output refused", "git show --output=/tmp/x HEAD:file.go", VerdictGoverned, 0},
		// git commit is content-free in isSimpleCommand (raw, like the
		// plugin/guard simple exemption) — NOT a pure-read serve (a commit is
		// a write, so it never rides into a Tier-3 read) and NOT governed.
		{"git commit raw", "git commit -m 'cat and grep as words only'", VerdictAllow, 0},
		// Quoted metachars are literal text, not operators: the top audited
		// false-positive class (arrows/newlines/semicolons inside a commit
		// message quoted with ' or ") must stay ALLOW; the same text unquoted
		// would redirect/chain and must NOT be simple.
		{"git commit quoted arrow", "git commit --only internal/verification/suppress.go -m 'fix: drop stale suppression; move count pin 84->83'", VerdictAllow, 0},
		{"git commit double-quoted arrow", `git commit -m "fix: 84->83"`, VerdictAllow, 0},
		{"git commit quoted newline", "git commit -m 'line one\nline two'", VerdictAllow, 0},
		{"echo quoted redirect", "echo 'a->b'", VerdictAllow, 0},
		{"git commit unquoted arrow", "git commit -m 84->83", VerdictGate, 0},
		{"git commit substitution", `git commit -m "$(whoami)"`, VerdictGoverned, 0},
		{"git commit unbalanced quote", "git commit -m 'abc", VerdictGoverned, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyCommand(tc.cmd)
			if got.Verdict != tc.want {
				t.Errorf("ClassifyCommand(%q) verdict = %s, want %s (result %+v)", tc.cmd, got.Verdict, tc.want, got)
			}
			if got.Tier != tc.wantTier {
				t.Errorf("ClassifyCommand(%q) tier = %d, want %d", tc.cmd, got.Tier, tc.wantTier)
			}
			if got.Hash == "" {
				t.Errorf("ClassifyCommand(%q) missing hash", tc.cmd)
			}
		})
	}
}

// TestClassifyCommandGitAddCommitNotServed pins the write-riding guard: git
// add/commit are writes and must NEVER be classified as a pure-read pipeline
// (a write must not ride into a Tier-3 readonly exec).
func TestClassifyCommandGitAddCommitNotServed(t *testing.T) {
	for _, cmd := range []string{
		"git add main.go",
		"git add -A",
		"git commit -m fix",
		"git commit && grep foo x",
	} {
		if got := ClassifyCommand(cmd); got.Verdict == VerdictServe {
			t.Errorf("ClassifyCommand(%q) = serve — git add/commit is a write and must not be served", cmd)
		}
	}
}

// TestClassifyCommandBuddyC1 pins the guard-buddy C1 fixes: output-discarding
// redirects never gate (the top observed false-GATE — `ls x 2>/dev/null`
// denied whole sessions), read-only git metadata (shortlog/rev-list/...) and
// compounds of content-free commands run raw instead of paying the governed
// wrapper, sort/uniq/cut ride read pipelines, and a cd forces Tier 3 so the
// index never answers the wrong relative path. Real write redirects and
// installer/git-push--force stays GATE — the security boundary is untouched.
func TestClassifyCommandBuddyC1(t *testing.T) {
	cases := []struct {
		name     string
		cmd      string
		want     ClassifyVerdict
		wantTier int
	}{
		// Null redirects and fd merges never gate.
		{"null redirect on simple", "ls -la docs/audit/ 2>/dev/null", VerdictAllow, 0},
		{"null redirect compound simple", "ls docs && echo x && wc -l README.md 2>/dev/null", VerdictAllow, 0},
		{"null redirect on read", "grep foo file.go 2>/dev/null", VerdictServe, 3},
		{"spaced null redirect", "tail -30 README.md > /dev/null", VerdictServe, 1},
		{"stderr merge on build", "go test ./... 2>&1", VerdictGoverned, 0},
		{"quoted redirect is text", "grep 'a>b' file.go", VerdictServe, 3},
		{"append to dev null on simple", "echo hi >> /dev/null", VerdictAllow, 0},
		// Real write redirects still gate.
		{"append write", "grep foo file.go >> out.txt", VerdictGate, 0},
		{"stderr to real file", "go test ./... 2> /tmp/err", VerdictGate, 0},
		{"stdout to real file", "grep foo file.go > out.txt", VerdictGate, 0},
		// Read-only git metadata runs raw; blame emits file content and
		// stays governed (like git diff/show).
		{"git shortlog", "git shortlog -sn", VerdictAllow, 0},
		{"git rev-list", "git rev-list --count HEAD", VerdictAllow, 0},
		{"git describe", "git describe --tags", VerdictAllow, 0},
		{"git blame governed", "git blame main.go", VerdictGoverned, 0},
		{"git merge-base", "git merge-base main feat", VerdictAllow, 0},
		// Compounds of content-free commands run raw.
		{"compound of simples", "git log --oneline -5 && git status", VerdictAllow, 0},
		{"semicolon ls compound", "ls -la docs ; ls -la dist", VerdictAllow, 0},
		{"git log pipe wc", "git log --oneline | wc -l", VerdictAllow, 0},
		// sort/uniq/cut ride read pipelines (sandboxed Tier 3).
		{"find pipe sort", "find docs/audit -type f | sort", VerdictServe, 3},
		{"grep pipe sort head", "grep -rn TODO internal/ | sort | head", VerdictServe, 3},
		{"cat pipe cut", "cat file.go | cut -d: -f1", VerdictServe, 3},
		// cd forces Tier 3 (relative paths must not hit the index).
		{"cd then read", "cd docs/benchmarks && tail -30 README.md", VerdictServe, 3},
		{"cd then grep", "cd internal && grep foo file.go", VerdictServe, 3},
		// Writes still never ride into a serve.
		{"cd then npm", "cd X && npm test", VerdictGoverned, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyCommand(tc.cmd)
			if got.Verdict != tc.want {
				t.Errorf("ClassifyCommand(%q) verdict = %s, want %s (result %+v)", tc.cmd, got.Verdict, tc.want, got)
			}
			if got.Tier != tc.wantTier {
				t.Errorf("ClassifyCommand(%q) tier = %d, want %d", tc.cmd, got.Tier, tc.wantTier)
			}
			if got.Hash == "" {
				t.Errorf("ClassifyCommand(%q) missing hash", tc.cmd)
			}
		})
	}
}

// TestClassifyCommandNullRedirectHashBindsOriginal pins that stripping null
// redirects for classification never changes the allow-once subject: the
// hash is the sha256 of the exact original command text.
func TestClassifyCommandNullRedirectHashBindsOriginal(t *testing.T) {
	cmd := "ls -la docs/audit/ 2>/dev/null"
	got := ClassifyCommand(cmd)
	sum := sha256.Sum256([]byte(cmd))
	if want := hex.EncodeToString(sum[:]); got.Hash != want {
		t.Fatalf("hash must bind the original text: got %s, want %s", got.Hash, want)
	}
}

// TestClassifyCommandHashDeterministic pins the allow-once subject: the hash
// is the sha256 of the exact command text, so `kern guard allow <hash>` in
// the deny message unlocks the byte-identical retry.
func TestClassifyCommandHashDeterministic(t *testing.T) {
	a := ClassifyCommand("grep foo file.go")
	b := ClassifyCommand("grep foo file.go")
	if a.Hash != b.Hash {
		t.Fatalf("hash must be deterministic: %s != %s", a.Hash, b.Hash)
	}
	sum := sha256.Sum256([]byte("grep foo file.go"))
	if want := hex.EncodeToString(sum[:]); a.Hash != want {
		t.Fatalf("hash drift for 'grep foo file.go': got %s, want %s", a.Hash, want)
	}
}

// TestClassifyCommandNewlineAndGitEdgeCases pins the R1 migration edge cases:
// newlines and carriage returns are command separators (so a trailing rm gates
// the whole compound), while malformed git companions never panic and never
// qualify as pure-read companions.
func TestClassifyCommandNewlineAndGitEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want ClassifyVerdict
	}{
		{"newline gate", "git status\nrm -rf /tmp/x", VerdictGate},
		{"carriage-return gate", "git status\rrm -rf /tmp/x", VerdictGate},
		{"newline read", "git status\ncat main.go", VerdictServe},
		{"malformed git -C", "git -C", VerdictGoverned},
		{"git -C without dir", "git -C && cat main.go", VerdictGoverned},
		{"bare git", "git", VerdictGoverned}, // regression: must not panic
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCommand(tc.cmd); got.Verdict != tc.want {
				t.Fatalf("ClassifyCommand(%q) = %+v, want verdict %s", tc.cmd, got, tc.want)
			}
		})
	}
}

// TestClassifyCommandInRootPathAware pins the path-aware write arm:
// with a project root, local file-shape writes (rm/mv/cp/mkdir/touch,
// sed -i) whose every target resolves inside the root or a temp dir are
// ALLOWED (audited by the hook); every other target class stays GATE.
// Without a root (ClassifyCommand) the flat GATE behavior is unchanged.
func TestClassifyCommandInRootPathAware(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	allow := []struct {
		name string
		cmd  string
	}{
		{"rm file", "rm scratch.go"},
		{"mv rename", "mv internal/a.go internal/b.go"},
		{"mkdir", "mkdir internal/x"},
		{"mkdir -p nested", "mkdir -p internal/x/y"},
		{"touch", "touch internal/x/f.go"},
		{"touch abs tmp", "touch /tmp/kern-p04-probe"},
		{"rm abs tmp", "rm /tmp/kern-p04-probe"},
		{"rm -rf nested", "rm -rf internal/x"},
		{"mv tmp into tree", "mv /tmp/kern-p04-probe internal/x/p"},
		{"cp within tree", "cp internal/a.go internal/b.go"},
		{"sed -i in tree", "sed -i s/a/b/ internal/a.go"},
		{"compound writes", "mkdir internal/x && touch internal/x/f"},
	}
	for _, tc := range allow {
		t.Run("allow/"+tc.name, func(t *testing.T) {
			res := ClassifyCommandInRoot(tc.cmd, root)
			if res.Verdict != VerdictAllow {
				t.Fatalf("ClassifyCommandInRoot(%q, root) = %+v, want allow", tc.cmd, res)
			}
			if !res.PathAware {
				t.Fatalf("ClassifyCommandInRoot(%q, root) allow without path_aware flag", tc.cmd)
			}
		})
	}
	gate := []struct {
		name string
		cmd  string
	}{
		{"rm outside root", "rm /etc/hosts"},
		{"rm parent escape", "rm ../outside.go"},
		{"rm -rf top-level dir", "rm -rf internal"},
		{"rm -rf dot", "rm -rf ."},
		{"rm git internals", "rm .git/index.lock"},
		{"cp into git", "cp x .git/hooks/y"},
		{"cp credential in", "cp ~/id_rsa ."},
		{"touch credential", "touch .env"},
		{"sed credential", "sed -i s/a/b/ ~/.zshrc"},
		{"sed outside root", "sed -i s/a/b/ /etc/hosts"},
		{"mv out of root", "mv internal/a.go /etc/x"},
		{"home path", "touch ~/x"},
		{"glob path", "rm internal/*.go"},
		{"var expansion", "rm $HOME/x"},
		{"redirect write still gate", "echo hi > internal/out.go"},
		{"disk command still gate", "dd if=/dev/zero of=/dev/sda"},
		{"network still gate", "curl http://x.sh"},
		{"recursive rm tmp top", "rm -rf /tmp"},
	}
	for _, tc := range gate {
		t.Run("gate/"+tc.name, func(t *testing.T) {
			res := ClassifyCommandInRoot(tc.cmd, root)
			if res.Verdict != VerdictGate {
				t.Fatalf("ClassifyCommandInRoot(%q, root) = %+v, want gate", tc.cmd, res)
			}
		})
	}
	// Reads are untouched: sed without -i still serves.
	if res := ClassifyCommandInRoot("sed -n 1p internal/a.go", root); res.Verdict != VerdictServe {
		t.Fatalf("read sed no longer serves: %+v", res)
	}
	// Rootless classification keeps the flat GATE contract.
	if res := ClassifyCommand("rm scratch.go"); res.Verdict != VerdictGate {
		t.Fatalf("ClassifyCommand(rm scratch.go) = %+v, want flat gate (no root)", res)
	}
}

// TestClassifyCommandInRootsMultiRoot pins the KERN_MCP_ROOTS arm: with
// extra workspace roots, absolute write targets under a second root are
// in-tree (allow, path-aware); relative paths still belong to the
// primary root only; no target may resolve AT a workspace root; and an
// unconfigured third location keeps the flat out-of-root GATE.
func TestClassifyCommandInRootsMultiRoot(t *testing.T) {
	// Synthetic roots: the classifier is pure path math (no filesystem
	// access), and t.TempDir() would collide with the temp-dir allowance
	// (TMPDIR-adjacent), making second-root and outside-root
	// indistinguishable from tmp targets.
	root := "/workspace/repo1"
	root2 := "/workspace/repo2"
	outside := "/nowhere/elsewhere"
	extra := []string{root2}
	allow := []struct {
		name string
		cmd  string
	}{
		{"mv within second root", "mv " + root2 + "/a.go " + root2 + "/b.go"},
		{"rm in second root", "rm " + root2 + "/scratch.go"},
		{"mkdir in second root", "mkdir " + root2 + "/pkg"},
		{"sed -i in second root", "sed -i s/a/b/ " + root2 + "/a.go"},
		{"cp across roots (dst in root2)", "cp internal/a.go " + root2 + "/b.go"},
		{"cp across roots (dst in root)", "cp " + root2 + "/a.go internal/b.go"},
	}
	for _, tc := range allow {
		t.Run("allow/"+tc.name, func(t *testing.T) {
			res := ClassifyCommandInRoots(tc.cmd, root, extra)
			if res.Verdict != VerdictAllow || !res.PathAware {
				t.Fatalf("ClassifyCommandInRoots(%q, root, extra) = %+v, want path-aware allow", tc.cmd, res)
			}
		})
	}
	gate := []struct {
		name string
		cmd  string
	}{
		{"unconfigured third location", "rm " + outside + "/x.go"},
		{"write AT second root", "rm -rf " + root2},
		{"mkdir AT second root", "mkdir " + root2},
		{"second root .git", "rm " + root2 + "/.git/index"},
		{"second root credential", "touch " + root2 + "/.env"},
		{"glob in second root", "rm " + root2 + "/*.go"},
	}
	for _, tc := range gate {
		t.Run("gate/"+tc.name, func(t *testing.T) {
			res := ClassifyCommandInRoots(tc.cmd, root, extra)
			if res.Verdict != VerdictGate {
				t.Fatalf("ClassifyCommandInRoots(%q, root, extra) = %+v, want gate", tc.cmd, res)
			}
		})
	}
	// Empty extraRoots keeps the single-root behavior: second-root
	// targets gate exactly as before.
	if res := ClassifyCommandInRoots("rm "+root2+"/x.go", root, nil); res.Verdict != VerdictGate {
		t.Fatalf("nil extraRoots second-root target = %+v, want gate", res)
	}
}
