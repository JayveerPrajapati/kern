package mutation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deadPID returns the PID of a process that has verifiably exited. Stale-
// journal fixtures must use a provably-dead PID: the R2 liveness check in
// recoverStaleJournals correctly skips journals whose owner is alive, so a
// hardcoded PID (e.g. 999) could be a live process on some machines.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot spawn subprocess for a dead PID: %v", err)
	}
	return cmd.Process.Pid
}

// writeMutantFixture writes a minimal Go module (calc.go + a test that kills
// mutants) into a fresh temp dir and returns the root and the original
// calc.go source.
func writeMutantFixture(t *testing.T) (root, code string) {
	t.Helper()
	dir := t.TempDir()
	goMod := "module example.com/calc\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	code = "package calc\n\nfunc Greater(a, b int) bool {\n\treturn a > b\n}\n"
	testCode := "package calc\n\nimport \"testing\"\n\nfunc TestGreater(t *testing.T) {\n\tif !Greater(5, 3) {\n\t\tt.Errorf(\"expected 5 > 3 to be true\")\n\t}\n\tif Greater(3, 5) {\n\t\tt.Errorf(\"expected 3 > 5 to be false\")\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc_test.go"), []byte(testCode), 0644); err != nil {
		t.Fatal(err)
	}
	return dir, code
}

// snapshotTree reads every non-.kern file under root into a map keyed by
// relative path.
func snapshotTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	got := map[string][]byte{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		if rel == ".kern" || strings.HasPrefix(rel, ".kern"+string(filepath.Separator)) {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatal(rerr)
		}
		got[rel] = b
		return nil
	})
	return got
}

// assertTreeIdentical fails the test unless the live tree matches want.
func assertTreeIdentical(t *testing.T, root string, want map[string][]byte) {
	t.Helper()
	after := snapshotTree(t, root)
	if len(want) != len(after) {
		t.Fatalf("file count changed: before=%d after=%d", len(want), len(after))
	}
	for rel, b := range want {
		if !bytes.Equal(b, after[rel]) {
			t.Errorf("file %s changed during run:\n%s", rel, after[rel])
		}
	}
}

// assertNoJournalLeft fails the test if any journal or backup artifact
// remains under <root>/.kern (audit C2: a clean run must leave nothing).
func assertNoJournalLeft(t *testing.T, root string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(root, ".kern", journalPrefix+"*"))
	if len(matches) > 0 {
		t.Errorf("journal left behind: %v", matches)
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", backupDirName)); !os.IsNotExist(err) {
		t.Errorf("backup dir left behind (stat err=%v)", err)
	}
}

// TestRunRestoresTreeByteIdentical is the required normal-run test: after a
// full mutation evaluation the working tree must be byte-identical and no
// journal/backup may remain.
func TestRunRestoresTreeByteIdentical(t *testing.T) {
	root, code := writeMutantFixture(t)
	before := snapshotTree(t, root)

	if _, err := Run(context.Background(), Options{
		Root:        root,
		Files:       []string{"calc.go"},
		MaxMutants:  10,
		TestCommand: "go test . -count=1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertTreeIdentical(t, root, before)
	if got := string(afterRead(t, root, "calc.go")); got != code {
		t.Errorf("calc.go not byte-identical after run:\n%s", got)
	}
	assertNoJournalLeft(t, root)
}

func afterRead(t *testing.T, root, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRunSelfHealsStaleJournal simulates an interrupted run (a killed process
// that left calc.go mutated plus a journal + backup on disk): the next Run
// must restore the original files FIRST, inform the user, and then proceed
// normally, leaving the tree byte-identical and no stale journal.
func TestRunSelfHealsStaleJournal(t *testing.T) {
	root, code := writeMutantFixture(t)

	// Leftover from a crashed run: calc.go is logic-inverted and the journal
	// records the original content in a backup.
	mutated := "package calc\n\nfunc Greater(a, b int) bool {\n\treturn a <= b\n}\n"
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(mutated), 0644); err != nil {
		t.Fatal(err)
	}
	kernDir := filepath.Join(root, mutationKernDir)
	pid := deadPID(t)
	backupDir := filepath.Join(kernDir, backupDirName, fmt.Sprintf("1234567890-%d", pid))
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "calc.go"), []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	stale := mutationJournal{
		PID:       pid,
		Started:   "2026-09-29T00:00:00Z",
		Root:      root,
		BackupDir: backupDir,
		Entries: []journalEntry{
			{AbsPath: filepath.Join(root, "calc.go"), Backup: filepath.Join(backupDir, "calc.go")},
		},
	}
	jp := filepath.Join(kernDir, fmt.Sprintf("%s%d%s", journalPrefix, pid, journalSuffix))
	data, _ := json.MarshalIndent(&stale, "", "  ")
	if err := os.WriteFile(jp, data, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), Options{
		Root:        root,
		Files:       []string{"calc.go"},
		MaxMutants:  10,
		TestCommand: "go test . -count=1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Self-heal must have restored the original before evaluating.
	if got := string(afterRead(t, root, "calc.go")); got != code {
		t.Errorf("calc.go not restored by self-heal:\n%s", got)
	}
	if _, err := os.Stat(jp); !os.IsNotExist(err) {
		t.Errorf("stale journal not removed (stat err=%v)", err)
	}
	assertNoJournalLeft(t, root)
}

// TestRecoverSkipsLiveJournal pins oracle-gate R2: self-heal must never
// touch a journal owned by a LIVE concurrent run — no restore (which would
// clobber its on-disk mutants mid-run), no cleanup (which would delete its
// backups and strand its mutants with no recovery path).
func TestRecoverSkipsLiveJournal(t *testing.T) {
	root, code := writeMutantFixture(t)

	// A "live" concurrent run's journal: this test process is definitively
	// alive, and its on-disk mutant is mid-evaluation.
	mutated := "package calc\n\nfunc Greater(a, b int) bool {\n\treturn a <= b\n}\n"
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(mutated), 0644); err != nil {
		t.Fatal(err)
	}
	kernDir := filepath.Join(root, mutationKernDir)
	backupDir := filepath.Join(kernDir, backupDirName, "1234567890-live")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "calc.go"), []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	live := os.Getpid()
	jp := filepath.Join(kernDir, fmt.Sprintf("%s%d%s", journalPrefix, live, journalSuffix))
	stale := mutationJournal{
		PID:       live,
		Started:   "2026-09-29T00:00:00Z",
		Root:      root,
		BackupDir: backupDir,
		Entries: []journalEntry{
			{AbsPath: filepath.Join(root, "calc.go"), Backup: filepath.Join(backupDir, "calc.go")},
		},
	}
	data, _ := json.MarshalIndent(&stale, "", "  ")
	if err := os.WriteFile(jp, data, 0644); err != nil {
		t.Fatal(err)
	}

	n, err := recoverStaleJournals(root)
	if err != nil {
		t.Fatalf("recoverStaleJournals: %v", err)
	}
	if n != 0 {
		t.Errorf("restored %d file(s) from a LIVE run's journal — must be skipped", n)
	}
	if _, serr := os.Stat(jp); os.IsNotExist(serr) {
		t.Error("live run's journal was deleted — must be left strictly alone")
	}
	if got := string(afterRead(t, root, "calc.go")); got != mutated {
		t.Errorf("live run's on-disk mutant was clobbered by self-heal:\n%s", got)
	}
}

// TestRunCanceledContextRestoresTree simulates an interrupted run in-process:
// a canceled context aborts every mutant test immediately, and the deferred
// restore must still leave the tree byte-identical.
func TestRunCanceledContextRestoresTree(t *testing.T) {
	root, _ := writeMutantFixture(t)
	before := snapshotTree(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled: each mutant's test fails instantly
	if _, err := Run(ctx, Options{
		Root:        root,
		Files:       []string{"calc.go"},
		MaxMutants:  10,
		TestCommand: "go test . -count=1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertTreeIdentical(t, root, before)
	assertNoJournalLeft(t, root)
}

// TestJournalPanicRestore exercises the defer-with-recover restore path that
// Run installs: a panic mid-run must restore all journaled files before the
// panic propagates.
func TestJournalPanicRestore(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.go")
	orig := []byte("package p\n\nvar X = 1\n")
	if err := os.WriteFile(target, orig, 0644); err != nil {
		t.Fatal(err)
	}

	j, err := newMutationJournal(root)
	if err != nil {
		t.Fatalf("newMutationJournal: %v", err)
	}
	if err := j.add(target, orig); err != nil {
		t.Fatalf("journal add: %v", err)
	}
	// A mutant is written, then the run panics.
	if err := os.WriteFile(target, []byte("package p\n\nvar X = 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, rerr := j.restore(); rerr != nil {
					t.Errorf("panic restore: %v", rerr)
				}
				j.cleanup()
				j.unregister()
				return
			}
			t.Error("expected panic to propagate")
		}()
		panic("simulated mid-run crash")
	}()

	if got := afterRead(t, root, "target.go"); !bytes.Equal(got, orig) {
		t.Errorf("target not restored after panic: %q", got)
	}
	if _, err := os.Stat(j.path); !os.IsNotExist(err) {
		t.Errorf("journal file left behind (stat err=%v)", err)
	}
}

// TestJournalPersistRoundTrip verifies the journal survives a write/load
// round trip (the crash-safe path a subsequent Run self-heals from).
func TestJournalPersistRoundTrip(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sub", "target.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := []byte("package p\n\nvar X = 1\n")
	if err := os.WriteFile(target, orig, 0644); err != nil {
		t.Fatal(err)
	}

	j, err := newMutationJournal(root)
	if err != nil {
		t.Fatalf("newMutationJournal: %v", err)
	}
	if err := j.add(target, orig); err != nil {
		t.Fatalf("journal add: %v", err)
	}
	if _, err := os.Stat(j.path); err != nil {
		t.Fatalf("journal file not persisted: %v", err)
	}

	loaded, err := loadMutationJournal(j.path)
	if err != nil {
		t.Fatalf("loadMutationJournal: %v", err)
	}
	if len(loaded.Entries) != 1 || loaded.Entries[0].AbsPath != target {
		t.Fatalf("loaded journal entries wrong: %+v", loaded.Entries)
	}
	// Backups must hold the original content.
	backed, err := os.ReadFile(loaded.Entries[0].Backup)
	if err != nil || !bytes.Equal(backed, orig) {
		t.Errorf("backup content mismatch (err=%v)", err)
	}
}
