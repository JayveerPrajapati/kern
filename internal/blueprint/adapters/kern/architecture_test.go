package kern

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
)

// freshStatusJSON is the canned `kern index --status --json` payload returned
// for the `index status` probe by fakeRunner/recordingRunner: a FRESH index
// (verdict "fresh"), so ArchitectureCheck proceeds straight to the guard check
// without rebuilding.
const freshStatusJSON = `{
	"schema_version": "2",
	"built": true,
	"stale": false,
	"freshness_proof": {
		"verdict": "fresh",
		"recorded": {"tree_oid": "c29d4b1", "content_root": "ff02913c", "built_at": "2026-08-29T19:01:58Z"},
		"current": {"tree_oid": "c29d4b1", "content_root": "", "built_at": "2026-08-29T19:02:33Z"},
		"checked_at": "2026-08-29T19:02:33Z"
	},
	"index_identity": {"tree_oid": "c29d4b1", "content_root": "ff02913c", "built_at": "2026-08-29T19:01:58Z"}
}`

// staleStatusJSON is the canned payload for a STALE index: the rebuild path
// (and, when it stays stale, the stale-non-converging ERROR path).
const staleStatusJSON = `{
	"schema_version": "2",
	"built": true,
	"stale": true,
	"freshness_proof": {
		"verdict": "stale",
		"recorded": {"tree_oid": "c29d4b1", "content_root": "ff02913c", "built_at": "2026-08-29T19:01:58Z"},
		"current": {"tree_oid": "9a2e7c4", "content_root": "b41d07e2", "built_at": "2026-08-29T19:02:33Z"},
		"checked_at": "2026-08-29T19:02:33Z"
	},
	"index_identity": {"tree_oid": "c29d4b1", "content_root": "ff02913c", "built_at": "2026-08-29T19:01:58Z"}
}`

// noIndexStatusJSON is the canned payload for built:false (no index): both
// freshness_proof and index_identity are omitted, so the verdict is "unknown"
// and the check treats the index as stale (fail-closed).
const noIndexStatusJSON = `{"schema_version": "2", "built": false, "stale": true}`

// fakeRunner builds a commandRunner that returns canned output for guard/sec
// calls, success (exit 0) for `kern index .` build calls, and a FRESH status
// payload for `kern index status --json` probes. The index success path is
// required because ArchitectureCheck refreshes the index before guarding
// (Phase 2 new-change principle); without it, the canned exit code would
// error the index call before the guard runs.
func fakeRunner(stdout, stderr string, exitCode int, err error) commandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "index" {
			if len(args) > 1 && args[1] == "--status" {
				return freshStatusJSON, "", 0, nil
			}
			return "", "", 0, nil
		}
		return stdout, stderr, exitCode, err
	}
}

// recordingRunner behaves like fakeRunner but additionally records the first
// argument (command name, e.g. "index", "guard") of every invocation into
// cmds, so tests can assert whether an index BUILD was issued. The always-on
// `index status` probe is NOT recorded: it is a read, and recording it would
// drown out the signal tests assert on (was a rebuild issued / did guard run).
func recordingRunner(stdout, stderr string, exitCode int, err error, cmds *[]string) commandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 {
			if !(args[0] == "index" && len(args) > 1 && args[1] == "--status") {
				*cmds = append(*cmds, args[0])
			}
		}
		if len(args) > 0 && args[0] == "index" {
			if len(args) > 1 && args[1] == "--status" {
				return freshStatusJSON, "", 0, nil
			}
			return "", "", 0, nil
		}
		return stdout, stderr, exitCode, err
	}
}

// repoRoot returns a temp directory containing a .kern directory, standing in
// for an indexed repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatalf("mkdir .kern: %v", err)
	}
	return root
}

func TestNewKernClientWithBinary(t *testing.T) {
	c, err := NewKernClient(WithBinary("/fake/kern"))
	if err != nil {
		t.Fatalf("NewKernClient: %v", err)
	}
	if c.binaryPath != "/fake/kern" {
		t.Errorf("binaryPath = %q, want /fake/kern", c.binaryPath)
	}
}

func TestExecCommandCapturesOutputAndExitCode(t *testing.T) {
	stdout, stderr, exitCode, err := execCommand(context.Background(), "/bin/sh", []string{"-c", "echo out; echo err >&2; exit 2"}, t.TempDir())
	if err != nil {
		t.Fatalf("execCommand returned error: %v", err)
	}
	if stdout != "out\n" {
		t.Errorf("stdout = %q, want %q", stdout, "out\n")
	}
	if stderr != "err\n" {
		t.Errorf("stderr = %q, want %q", stderr, "err\n")
	}
	if exitCode != 2 {
		t.Errorf("exitCode = %d, want 2", exitCode)
	}
}

func TestExecCommandLaunchFailure(t *testing.T) {
	_, _, exitCode, err := execCommand(context.Background(), "/definitely/not/a/real/binary", nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if exitCode != -1 {
		t.Errorf("exitCode = %d, want -1", exitCode)
	}
}

func TestArchitectureCheckClean(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations": null}`, "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Name != "architecture:guard" {
		t.Errorf("Name = %q, want architecture:guard", cr.Name)
	}
	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

func TestArchitectureCheckViolations(t *testing.T) {
	const out = `{"schema_version":2,"violations": [
		{"caller_file": "web/web.go", "callee_file": "db/db.go", "symbol": "Query", "line": 2, "rule_from": "web", "rule_to": "db"},
		{"caller_file": "api/handler.go", "callee_file": "infra/redis.go", "symbol": "NewClient", "line": 41, "rule_from": "api", "rule_to": "infra"}
	]}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 2, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 2 {
		t.Fatalf("Findings = %d, want 2", len(cr.Findings))
	}

	f := cr.Findings[0]
	if f.RuleID != "architecture:boundary-violation" {
		t.Errorf("RuleID = %q, want architecture:boundary-violation", f.RuleID)
	}
	if f.Severity != domain.SeverityBlock {
		t.Errorf("Severity = %q, want %q", f.Severity, domain.SeverityBlock)
	}
	if f.Category != domain.CategoryArchitecture {
		t.Errorf("Category = %q, want %q", f.Category, domain.CategoryArchitecture)
	}
	if f.File != "web/web.go" {
		t.Errorf("File = %q, want web/web.go", f.File)
	}
	if f.Line != 2 {
		t.Errorf("Line = %d, want 2", f.Line)
	}
	wantMsg := "web/web.go (Query) calls into db/db.go: forbidden by boundary rule web -> db"
	if f.Message != wantMsg {
		t.Errorf("Message = %q, want %q", f.Message, wantMsg)
	}
	if f.Explanation == "" {
		t.Error("Explanation is empty")
	}
	if len(f.Evidence) != 1 {
		t.Fatalf("Evidence = %d, want 1", len(f.Evidence))
	}
	if f.Evidence[0].Kind != "import-edge" {
		t.Errorf("Evidence.Kind = %q, want import-edge", f.Evidence[0].Kind)
	}
	if f.Evidence[0].Location != "web/web.go:2" {
		t.Errorf("Evidence.Location = %q, want web/web.go:2", f.Evidence[0].Location)
	}
}

func TestArchitectureCheckRunsInRepoRoot(t *testing.T) {
	root := repoRoot(t)
	var gotWorkdir string
	var gotArgs []string
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			gotWorkdir = workdir
			gotArgs = append([]string(nil), args...)
			if len(args) > 0 && args[0] == "index" && len(args) > 1 && args[1] == "--status" {
				return freshStatusJSON, "", 0, nil
			}
			return `{"schema_version":2,"violations": null}`, "", 0, nil
		},
	}
	chk := NewArchitectureCheck(client)
	if _, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gotWorkdir != root {
		t.Errorf("workdir = %q, want %q", gotWorkdir, root)
	}
	wantArgs := "guard check --file web/web.go --json"
	if strings.Join(gotArgs, " ") != wantArgs {
		t.Errorf("args = %v, want [%s]", gotArgs, wantArgs)
	}
}

func TestArchitectureCheckToolError(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "kern: unable to load boundaries", 1, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "exit 1") {
		t.Errorf("Error = %q, want mention of exit 1", cr.Error)
	}
}

func TestArchitectureCheckLaunchFailure(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "", -1, errors.New("executable not found"))}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "executable not found") {
		t.Errorf("Error = %q, want mention of launch failure", cr.Error)
	}
}

func TestArchitectureCheckRequiresRepositoryRoot(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if cr.Error != "repository root required" {
		t.Errorf("Error = %q, want repository root required", cr.Error)
	}
}

func TestArchitectureCheckMissingIndex(t *testing.T) {
	root := t.TempDir() // no .kern directory
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// No .kern directory means no boundaries declared, so enforcement is
	// impossible. The check must say so loudly (WARN) rather than skip
	// silently — "failure is never a silent pass". The absence of boundaries
	// must be visible to the user.
	if cr.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusWarn)
	}
	if cr.Skipped {
		t.Errorf("Skipped = true, want false (WARN is not a skip)")
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	f := cr.Findings[0]
	if f.RuleID != "architecture:not-enforced" {
		t.Errorf("RuleID = %q, want architecture:not-enforced", f.RuleID)
	}
	if f.Severity != domain.SeverityWarn {
		t.Errorf("Severity = %q, want %q", f.Severity, domain.SeverityWarn)
	}
	if f.Category != domain.CategoryArchitecture {
		t.Errorf("Category = %q, want %q", f.Category, domain.CategoryArchitecture)
	}
	if f.File != "" {
		t.Errorf("File = %q, want empty (repo-level finding)", f.File)
	}
	if f.Message == "" {
		t.Error("Message is empty")
	}
}

// TestArchitectureCheckMissingIndexEmptyChange: an empty change (no files to
// check) must not surface the not-enforced warning — there is no signal to
// warn about, so the check keeps the old clean SKIP. (service.Validate
// returns PASS for empty changes before checks run, so this only matters when
// Run is invoked directly.)
func TestArchitectureCheckMissingIndexEmptyChange(t *testing.T) {
	root := t.TempDir() // no .kern directory
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: root})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusSkip {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusSkip)
	}
	if !cr.Skipped {
		t.Errorf("Skipped = false, want true")
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

// TestArchitectureCheckMissingIndexModeOff: the check always emits the
// not-enforced WARN; policy mode "off" owns downgrading it to SKIP. The mode
// handling lives in the policy engine, not the check, so the check must not
// special-case it.
func TestArchitectureCheckMissingIndexModeOff(t *testing.T) {
	root := t.TempDir() // no .kern directory
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Fatalf("check Status = %q, want %q (the check must emit the WARN regardless of policy)", cr.Status, domain.StatusWarn)
	}
	// mode:off => the policy engine downgrades every result to SKIP.
	engine := policy.NewEngine(policy.Policy{Mode: "off"})
	status, _ := engine.Evaluate(cr)
	if status != domain.StatusSkip {
		t.Errorf("Evaluate(mode:off) status = %q, want %q", status, domain.StatusSkip)
	}
}

// TestGuardCheckFilesEmptyStagedSetNoWorkingTreeScan: an all-deletions staged
// change leaves the staged set empty (stagedFilePaths drops OpDelete files),
// so there is nothing new to enforce. GuardCheckFiles with an empty list
// would fall back to GuardCheck, which scans the whole working tree (staged
// AND unstaged) and would surface pre-existing unstaged violations as new.
// The check must skip — never run a working-tree scan on an empty staged set.
func TestGuardCheckFilesEmptyStagedSetNoWorkingTreeScan(t *testing.T) {
	root := repoRoot(t)
	var cmds []string
	client := &KernClient{
		binaryPath: "kern",
		// If the fallback ran, this canned output would surface a violation
		// from an unrelated unstaged file that is not part of the change.
		runner: recordingRunner(`{"schema_version":2,"violations": [
			{"caller_file": "unstaged/unstaged.go", "callee_file": "db/db.go", "symbol": "Query", "line": 1, "rule_from": "unstaged", "rule_to": "db"}
		]}`, "", 2, nil, &cmds),
	}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{
			{Path: "legacy/deleted.go", Op: domain.OpDelete},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusSkip {
		t.Errorf("Status = %q, want %q (empty staged set must not surface unstaged working-tree violations)", cr.Status, domain.StatusSkip)
	}
	if !cr.Skipped {
		t.Errorf("Skipped = false, want true")
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0 (unstaged violations must not leak)", len(cr.Findings))
	}
	for _, c := range cmds {
		if c == "guard" {
			t.Errorf("guard check ran despite empty staged set; commands: %v", cmds)
		}
	}
}

// writeIndexJSON writes a minimal .kern/index.json with the given java
// package import state, standing in for what `kern index` would produce.
func writeIndexJSON(t *testing.T, root string, javaImports bool) {
	t.Helper()
	imports := []string(nil)
	if javaImports {
		imports = []string{"com.example.commons.vault"}
	}
	idx := map[string]any{
		"packages": map[string]any{
			"src/main/java/com/example/config": map[string]any{
				"name":    "config",
				"path":    "src/main/java/com/example/config",
				"lang":    "java",
				"imports": imports,
			},
		},
	}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "index.json"), data, 0o644); err != nil {
		t.Fatalf("write index.json: %v", err)
	}
}

// writeBoundariesJSON writes a boundaries.json with a single forbid rule.
func writeBoundariesJSON(t *testing.T, root string) {
	t.Helper()
	data := []byte(`{"rules":[{"from":"config","to":"vault","action":"forbid"}]}`)
	if err := os.WriteFile(filepath.Join(root, ".kern", "boundaries.json"), data, 0o644); err != nil {
		t.Fatalf("write boundaries.json: %v", err)
	}
}

// TestArchitectureCheckJavaCoverageWarning: when staged changes include Java
// files and the kern index carries no Java imports, the check must surface a
// WARN (never a silent PASS and never a BLOCK).
func TestArchitectureCheckJavaCoverageWarning(t *testing.T) {
	root := repoRoot(t)
	writeBoundariesJSON(t, root)
	writeIndexJSON(t, root, false)
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations": null}`, "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{
			{Path: "src/main/java/com/example/config/AppConfig.java", Op: domain.OpWrite},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusWarn)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1 (the java-coverage warning)", len(cr.Findings))
	}
	f := cr.Findings[0]
	if f.RuleID != "architecture:java-import-boundaries-not-enforced" {
		t.Errorf("RuleID = %q, want architecture:java-import-boundaries-not-enforced", f.RuleID)
	}
	if f.Severity != domain.SeverityWarn {
		t.Errorf("Severity = %q, want %q", f.Severity, domain.SeverityWarn)
	}
}

// TestArchitectureCheckNoJavaWarningWhenImportsIndexed: a kern build that
// extracts Java imports must not produce the warning.
func TestArchitectureCheckNoJavaWarningWhenImportsIndexed(t *testing.T) {
	root := repoRoot(t)
	writeBoundariesJSON(t, root)
	writeIndexJSON(t, root, true)
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations": null}`, "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{
			{Path: "src/main/java/com/example/config/AppConfig.java", Op: domain.OpWrite},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

// TestArchitectureCheckNoJavaWarningWithoutJavaFiles: non-Java changes never
// trigger the warning even on an imports-less index.
func TestArchitectureCheckNoJavaWarningWithoutJavaFiles(t *testing.T) {
	root := repoRoot(t)
	writeBoundariesJSON(t, root)
	writeIndexJSON(t, root, false)
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations": null}`, "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{
			{Path: "cmd/blueprint/main.go", Op: domain.OpWrite},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

// TestArchitectureCheckBlockBeatsWarning: a real block finding keeps BLOCK
// status even when the java-coverage warning would also fire.
func TestArchitectureCheckBlockBeatsWarning(t *testing.T) {
	root := repoRoot(t)
	writeBoundariesJSON(t, root)
	writeIndexJSON(t, root, false)
	const out = `{"schema_version":2,"violations": [
		{"caller_file": "src/main/java/com/example/config/AppConfig.java", "callee_file": "src/main/java/com/example/commons/vault/", "symbol": "", "line": 0, "rule_from": "config", "rule_to": "vault"}
	]}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 2, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{
			{Path: "src/main/java/com/example/config/AppConfig.java", Op: domain.OpWrite},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 2 {
		t.Fatalf("Findings = %d, want 2 (block + warning)", len(cr.Findings))
	}
}

// TestArchitectureCheckSkipsRebuildWhenFresh: with a fresh index (kern verdict
// "fresh"), Run must NOT rebuild the index.
func TestArchitectureCheckSkipsRebuildWhenFresh(t *testing.T) {
	root := repoRoot(t)
	var cmds []string
	client := &KernClient{binaryPath: "kern", runner: recordingRunner(`{"schema_version":2,"violations": null}`, "", 0, nil, &cmds)}
	chk := NewArchitectureCheck(client)
	req := domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	}
	if _, err := chk.Run(context.Background(), req); err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	if _, err := chk.Run(context.Background(), req); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	for _, c := range cmds {
		if c == "index" {
			t.Fatalf("index build issued despite fresh index; commands: %v", cmds)
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("expected exactly 2 guard commands, got %v", cmds)
	}
}

// statusRunner builds a commandRunner for ArchitectureCheck tests. The FIRST
// `index status` probe returns firstStatus ("" => freshStatusJSON); every later
// probe returns freshStatusJSON. `kern index .` builds succeed and are
// recorded into builds (nil disables recording). All other calls (guard/sec)
// return canned stdout with exitCode. It models an index whose staleness is
// decided by kern's verdict and, when stale, converges after one rebuild.
func statusRunner(firstStatus, stdout string, exitCode int, builds *[]string) commandRunner {
	statusCalls := 0
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "index" {
			if len(args) > 1 && args[1] == "--status" {
				statusCalls++
				if statusCalls == 1 && firstStatus != "" {
					return firstStatus, "", 0, nil
				}
				return freshStatusJSON, "", 0, nil
			}
			if builds != nil {
				*builds = append(*builds, "index")
			}
			return "", "", 0, nil
		}
		return stdout, "", exitCode, nil
	}
}

// TestArchitectureCheckRebuildsWhenStale: when kern reports the index stale
// (verdict "stale"), Run issues exactly one `kern index .` build, re-verifies,
// and passes once the index converges.
func TestArchitectureCheckRebuildsWhenStale(t *testing.T) {
	t.Setenv("BLUEPRINT_ALLOW_STALE_REBUILD", "")
	root := repoRoot(t)
	var builds []string
	client := &KernClient{binaryPath: "kern", runner: statusRunner(staleStatusJSON, `{"schema_version":2,"violations": null}`, 0, &builds)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q (converged rebuild passes)", cr.Status, domain.StatusPass)
	}
	if len(builds) != 1 {
		t.Fatalf("index builds = %d, want exactly 1 (stale -> rebuild -> fresh); commands: %v", len(builds), builds)
	}
}

// TestArchitectureCheckRebuildsWhenNoIndex: kern reports built:false (no
// index), so freshness_proof is omitted and the verdict is "unknown" — the
// check treats that as stale (fail-closed) and must build the index before
// the guard check.
func TestArchitectureCheckRebuildsWhenNoIndex(t *testing.T) {
	t.Setenv("BLUEPRINT_ALLOW_STALE_REBUILD", "")
	root := repoRoot(t)
	var builds []string
	client := &KernClient{binaryPath: "kern", runner: statusRunner(noIndexStatusJSON, `{"schema_version":2,"violations": null}`, 0, &builds)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q (built:false -> rebuild -> fresh)", cr.Status, domain.StatusPass)
	}
	if len(builds) != 1 {
		t.Fatalf("index builds = %d, want exactly 1; commands: %v", len(builds), builds)
	}
}

// TestGuardCheckFilesChunksLargeSets: a large file set (130 files, over two
// guardBatchSize boundaries) must be split into multiple `kern guard check
// --file` execs with at most guardBatchSize files per --file arg (ARG_MAX
// hardening), and all violations from every batch must be merged.
func TestGuardCheckFilesChunksLargeSets(t *testing.T) {
	files := make([]string, 130)
	for i := range files {
		files[i] = fmt.Sprintf("pkg%03d/file%03d.go", i, i)
	}
	var fileArgs []string
	var guardCalls int
	const oneViolation = `{"schema_version":2,"violations": [{"caller_file":"a.go","callee_file":"b.go","symbol":"F","line":1,"rule_from":"a","rule_to":"b"}]}`
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			if len(args) > 0 && args[0] == "guard" {
				guardCalls++
				for i := 0; i+1 < len(args); i++ {
					if args[i] == "--file" {
						fileArgs = append(fileArgs, args[i+1])
					}
				}
			}
			return oneViolation, "", 2, nil
		},
	}

	violations, stdout, code, err := client.GuardCheckFiles(context.Background(), repoRoot(t), files)
	if err != nil {
		t.Fatalf("GuardCheckFiles returned error: %v", err)
	}
	// 130 files / 64 per batch => 3 batches (64+64+2).
	if guardCalls < 3 {
		t.Errorf("guard exec calls = %d, want >=3 (130 files chunked at 64)", guardCalls)
	}
	if len(fileArgs) != guardCalls {
		t.Fatalf("recorded --file args = %d, want %d", len(fileArgs), guardCalls)
	}
	for i, fa := range fileArgs {
		if n := strings.Count(fa, ",") + 1; n > guardBatchSize {
			t.Errorf("batch %d carries %d files in one --file arg, want <= %d", i, n, guardBatchSize)
		}
	}
	if len(violations) != guardCalls {
		t.Errorf("merged violations = %d, want %d (one per batch)", len(violations), guardCalls)
	}
	if code != 2 {
		t.Errorf("exitCode = %d, want 2 (last batch reported violations)", code)
	}
	if !strings.Contains(stdout, `"violations"`) {
		t.Errorf("stdout should contain the joined batch output, got: %q", stdout)
	}
}

// TestGuardCheckFilesBatchErrorIsContextual: an error in one batch must be
// returned with batch context, and the empty-set fallback to GuardCheck must
// remain intact.
func TestGuardCheckFilesBatchErrorIsContextual(t *testing.T) {
	// Empty files falls back to plain GuardCheck (no --file, no chunking).
	var guardCalls []string
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			guardCalls = append(guardCalls, strings.Join(args, " "))
			return `{"schema_version":2,"violations": null}`, "", 0, nil
		},
	}
	if _, _, _, err := client.GuardCheckFiles(context.Background(), repoRoot(t), nil); err != nil {
		t.Fatalf("empty files should fall back to GuardCheck, got error: %v", err)
	}
	if len(guardCalls) != 1 || strings.Contains(guardCalls[0], "--file") {
		t.Fatalf("empty files fallback = %q, want a single plain guard check", guardCalls)
	}

	// A failing batch carries batch context in the error.
	files := make([]string, guardBatchSize+1)
	for i := range files {
		files[i] = fmt.Sprintf("f%03d.go", i)
	}
	client2 := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			return "", "kern: boom", 3, nil
		},
	}
	_, _, _, err := client2.GuardCheckFiles(context.Background(), repoRoot(t), files)
	if err == nil {
		t.Fatal("expected error from failing batch")
	}
	if !strings.Contains(err.Error(), "exit 3") || !strings.Contains(err.Error(), "batch") {
		t.Errorf("error = %q, want exit code and batch context", err)
	}
}

// --- P2-4 (G25): Kern 2.0 Evidence provenance stamping ---

// TestArchitectureCheckRuleVersionConfidenceFreshness verifies architecture
// findings carry rule_version "1", confidence 1.0, scope "file", and
// index_freshness "fresh" when the index was already current (no rebuild —
// the canned status probe reports verdict "fresh").
func TestArchitectureCheckRuleVersionConfidenceFreshness(t *testing.T) {
	const out = `{"schema_version":2,"violations": [
		{"caller_file": "web/web.go", "callee_file": "db/db.go", "symbol": "Query", "line": 2, "rule_from": "web", "rule_to": "db"}
	]}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 2, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(cr.Findings) == 0 {
		t.Fatalf("Findings = 0, want >= 1")
	}
	f := cr.Findings[0]
	if f.RuleVersion != "1" {
		t.Errorf("RuleVersion = %q, want \"1\"", f.RuleVersion)
	}
	if f.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", f.Confidence)
	}
	if f.Scope != "file" {
		t.Errorf("Scope = %q, want \"file\"", f.Scope)
	}
	if f.IndexFreshness != "fresh" {
		t.Errorf("IndexFreshness = %q, want \"fresh\" (index was rebuilt)", f.IndexFreshness)
	}
}

// TestArchitectureCheck_StaleIndexErrors (P0.2 DoD): when kern reports the
// index stale and a rebuild does NOT converge (verdict stays "stale" on both
// probes), the check must ERROR with an architecture:index-stale finding
// carrying IndexFreshness "stale" — it must never silently pass on a
// potentially-misleading index.
func TestArchitectureCheck_StaleIndexErrors(t *testing.T) {
	t.Setenv("BLUEPRINT_ALLOW_STALE_REBUILD", "")
	root := repoRoot(t)
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			if len(args) > 0 && args[0] == "index" {
				if len(args) > 1 && args[1] == "--status" {
					// Both the pre-rebuild probe AND the post-rebuild
					// re-verification report stale: the rebuild never converges.
					return staleStatusJSON, "", 0, nil
				}
				return "", "", 0, nil // `kern index .` build succeeds
			}
			return `{"schema_version":2,"violations": null}`, "", 0, nil
		},
	}
	chk := NewArchitectureCheck(client)
	res, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q (stale-non-converging index must ERROR, not pass)", res.Status, domain.StatusError)
	}
	var stale *domain.Finding
	for i := range res.Findings {
		if res.Findings[i].RuleID == "architecture:index-stale" {
			stale = &res.Findings[i]
		}
	}
	if stale == nil {
		t.Fatalf("no architecture:index-stale finding; got: %+v", res.Findings)
	}
	if stale.IndexFreshness != "stale" {
		t.Errorf("IndexFreshness = %q, want \"stale\"", stale.IndexFreshness)
	}
	if stale.Severity != domain.SeverityError {
		t.Errorf("Severity = %q, want %q", stale.Severity, domain.SeverityError)
	}
}

// TestArchitectureCheck_StaleIndexRebuildsThenPasses (P0.2 happy path): a
// stale index that converges after one rebuild passes. The freshness label
// ("rebuilt") only materializes on findings, so after the clean pass the test
// re-runs with a boundary violation to observe the stamp.
func TestArchitectureCheck_StaleIndexRebuildsThenPasses(t *testing.T) {
	t.Setenv("BLUEPRINT_ALLOW_STALE_REBUILD", "")
	root := repoRoot(t)
	guardOut := `{"schema_version":2,"violations": null}`
	guardExit := 0
	statusCalls := 0
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			if len(args) > 0 && args[0] == "index" {
				if len(args) > 1 && args[1] == "--status" {
					// Probes alternate stale/fresh: each Run's first probe is
					// stale, the post-rebuild re-verification is fresh.
					statusCalls++
					if statusCalls%2 == 1 {
						return staleStatusJSON, "", 0, nil
					}
					return freshStatusJSON, "", 0, nil
				}
				return "", "", 0, nil
			}
			return guardOut, "", guardExit, nil
		},
	}
	chk := NewArchitectureCheck(client)
	req := domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	}

	// Run 1: GuardCheckFiles returns no violations -> the rebuilt index passes.
	res, err := chk.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q (converged rebuild must pass)", res.Status, domain.StatusPass)
	}

	// Run 2: a violation surfaces a finding so the "rebuilt" stamp is observable.
	guardOut = `{"schema_version":2,"violations": [
		{"caller_file": "web/web.go", "callee_file": "db/db.go", "symbol": "Query", "line": 2, "rule_from": "web", "rule_to": "db"}
	]}`
	guardExit = 2
	res, err = chk.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(res.Findings) == 0 {
		t.Fatalf("Findings = 0, want >= 1")
	}
	if f := res.Findings[0]; f.IndexFreshness != "rebuilt" {
		t.Errorf("IndexFreshness = %q, want \"rebuilt\" (stale -> rebuilt -> fresh)", f.IndexFreshness)
	}
}

// --- B6: projected (pre-write) import check ---

// projectedFixture builds a repo root with .kern, a web->db forbid boundary,
// and an on-disk local source tree (db/db.go, web/web.go) so the projected
// check has target directories to match imports against.
func projectedFixture(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	if err := os.WriteFile(filepath.Join(root, ".kern", "boundaries.json"),
		[]byte(`{"rules":[{"from":"web","to":"db","action":"forbid"}]}`), 0o644); err != nil {
		t.Fatalf("write boundaries.json: %v", err)
	}
	for _, f := range []struct{ path, content string }{
		{"db/db.go", "package db\n\nfunc Query() {}\n"},
		{"web/web.go", "package web\n\nfunc Handle() {}\n"},
	} {
		full := filepath.Join(root, f.path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(f.path), err)
		}
		if err := os.WriteFile(full, []byte(f.content), 0o644); err != nil {
			t.Fatalf("write %s: %v", f.path, err)
		}
	}
	return root
}

// TestArchitectureCheckProjectedImportViolation proposes a NEW file (not on
// disk, content in the request) that imports a forbidden package: the guard
// check cannot see it, so the projected import check must emit
// architecture:projected-import-violation and the check must BLOCK.
func TestArchitectureCheckProjectedImportViolation(t *testing.T) {
	root := projectedFixture(t)

	const evilContent = "package web\n\nimport \"example.com/repo/db\"\n\nfunc Extra() { db.Query() }\n"

	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations": null}`, "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/evil.go", Op: domain.OpWrite, Content: evilContent}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q (projected violation blocks like the on-disk guard)", cr.Status, domain.StatusBlock)
	}

	var pf *domain.Finding
	for i := range cr.Findings {
		if cr.Findings[i].RuleID == "architecture:projected-import-violation" {
			pf = &cr.Findings[i]
			break
		}
	}
	if pf == nil {
		t.Fatalf("no projected-import-violation finding; got: %+v", cr.Findings)
	}
	if pf.Severity != domain.SeverityBlock {
		t.Errorf("Severity = %q, want %q", pf.Severity, domain.SeverityBlock)
	}
	if pf.Category != domain.CategoryArchitecture {
		t.Errorf("Category = %q, want %q", pf.Category, domain.CategoryArchitecture)
	}
	if pf.File != "web/evil.go" {
		t.Errorf("File = %q, want web/evil.go", pf.File)
	}
	if !strings.Contains(pf.Message, "example.com/repo/db") || !strings.Contains(pf.Message, "web -> db") {
		t.Errorf("Message = %q, want forbidden import + rule", pf.Message)
	}
	if pf.SuggestedFix == "" {
		t.Error("SuggestedFix is empty")
	}
	if len(pf.Evidence) != 1 || pf.Evidence[0].Kind != "projected-import-edge" {
		t.Errorf("Evidence = %+v, want one projected-import-edge", pf.Evidence)
	}
}

// TestArchitectureCheckProjectedImportClean proposes a NEW file whose import
// is legal (web -> api is allowed): no projected finding, check passes.
func TestArchitectureCheckProjectedImportClean(t *testing.T) {
	root := projectedFixture(t)
	// Add an api layer so web -> api is a real local target.
	full := filepath.Join(root, "api", "api.go")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	if err := os.WriteFile(full, []byte("package api\n\nfunc Handle() {}\n"), 0o644); err != nil {
		t.Fatalf("write api.go: %v", err)
	}

	const content = "package web\n\nimport \"example.com/repo/api\"\n\nfunc Extra() { api.Handle() }\n"

	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations": null}`, "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/extra.go", Op: domain.OpWrite, Content: content}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q; findings: %+v", cr.Status, domain.StatusPass, cr.Findings)
	}
	for _, f := range cr.Findings {
		if f.RuleID == "architecture:projected-import-violation" {
			t.Fatalf("unexpected projected violation for legal import: %+v", f)
		}
	}
}

// TestArchitectureCheckProjectedSkipsOnDiskFiles proves the projected check
// is ADDITIVE and does not change behavior for files already on disk: a file
// that exists (even carrying proposed content in the request) is left to
// `kern guard` — the projected check emits nothing for it.
func TestArchitectureCheckProjectedSkipsOnDiskFiles(t *testing.T) {
	root := projectedFixture(t)
	// The proposed file ALREADY exists on disk with the same content.
	full := filepath.Join(root, "web", "web.go")
	if err := os.WriteFile(full, []byte("package web\n\nimport \"example.com/repo/db\"\n\nfunc Extra() { db.Query() }\n"), 0o644); err != nil {
		t.Fatalf("write web.go: %v", err)
	}

	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations": null}`, "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite, Content: "package web\n\nimport \"example.com/repo/db\"\n\nfunc Extra() { db.Query() }\n"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	for _, f := range cr.Findings {
		if f.RuleID == "architecture:projected-import-violation" {
			t.Fatalf("on-disk file must not produce a projected finding; got: %+v", f)
		}
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q (guard saw no violations)", cr.Status, domain.StatusPass)
	}
}

// TestArchitectureCheckProjectedNoBoundaries is the degenerate case: no
// boundaries declared -> no projected findings, and the on-disk guard result
// is unchanged.
func TestArchitectureCheckProjectedNoBoundaries(t *testing.T) {
	root := repoRoot(t) // .kern exists but no boundaries.json
	full := filepath.Join(root, "db", "db.go")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir db: %v", err)
	}
	if err := os.WriteFile(full, []byte("package db\n\nfunc Query() {}\n"), 0o644); err != nil {
		t.Fatalf("write db.go: %v", err)
	}

	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations": null}`, "", 0, nil)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "web/evil.go", Op: domain.OpWrite, Content: "package web\n\nimport \"example.com/repo/db\"\n\nfunc Extra() { db.Query() }\n"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusPass)
	}
	for _, f := range cr.Findings {
		if f.RuleID == "architecture:projected-import-violation" {
			t.Fatalf("no boundaries -> no projected finding; got: %+v", f)
		}
	}
}

// TestExtractImports exercises the language-aware import extractor used by the
// projected check.
func TestExtractImports(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		content string
		want    []string
	}{
		{
			name:    "go single import",
			path:    "web/w.go",
			content: "package web\n\nimport \"example.com/repo/db\"\n",
			want:    []string{"example.com/repo/db"},
		},
		{
			name:    "go import block",
			path:    "web/w.go",
			content: "package web\n\nimport (\n\t\"example.com/repo/db\"\n\t\"net/http\"\n)\n",
			want:    []string{"example.com/repo/db", "net/http"},
		},
		{
			name:    "go aliased import",
			path:    "web/w.go",
			content: "package web\n\nimport dbpkg \"example.com/repo/db\"\n",
			want:    []string{"example.com/repo/db"},
		},
		{
			name:    "java import",
			path:    "web/W.java",
			content: "package web;\nimport com.example.db.DbClient;\nimport static org.junit.Assert.*;\n",
			want:    []string{"com/example/db/DbClient", "org/junit/Assert"},
		},
		{
			name:    "python import and from",
			path:    "web/w.py",
			content: "import os\nfrom db import query\n",
			want:    []string{"os", "db"},
		},
		{
			name:    "typescript from",
			path:    "web/w.ts",
			content: "import { Handler } from '../db/handler';\nimport * as api from './api';\n",
			want:    []string{"../db/handler", "./api"},
		},
		{
			name:    "javascript require",
			path:    "web/w.js",
			content: "const db = require('../db');\nimport './side-effect';\n",
			want:    []string{"../db", "./side-effect"},
		},
		{
			name:    "ruby require",
			path:    "web/w.rb",
			content: "require 'db'\nrequire \"../api\"\n",
			want:    []string{"db", "../api"},
		},
		{
			name:    "unsupported extension",
			path:    "web/README.md",
			content: "import \"example.com/repo/db\"\n",
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractImports(tc.path, tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("extractImports() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("import[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestArchitectureCheck_NilClient_Degraded: a nil client (kern binary
// missing → degraded mode) must not panic. Run reports a WARN finding
// (kern:unavailable) so the blocking architecture leg stays visible as
// "ran but degraded" — never a silent SKIP, never a pipeline failure.
func TestArchitectureCheck_NilClient_Degraded(t *testing.T) {
	c := NewArchitectureCheck(nil)
	res, err := c.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Source:         domain.SourceHuman,
		Operation:      domain.OpCommit,
	})
	if err != nil {
		t.Fatalf("Run with nil client: unexpected error: %v", err)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("status=%s want WARN (degraded must be visible, not SKIP)", res.Status)
	}
	if res.Skipped {
		t.Fatalf("degraded result must not be marked Skipped (leg must be visible)")
	}
	var found bool
	for _, f := range res.Findings {
		if f.RuleID != "kern:unavailable" {
			continue
		}
		found = true
		if f.Severity != domain.SeverityWarn {
			t.Errorf("severity=%s want warn", f.Severity)
		}
		if f.Category != domain.CategoryPolicy {
			t.Errorf("category=%s want policy", f.Category)
		}
	}
	if !found {
		t.Fatalf("no kern:unavailable finding in %+v", res.Findings)
	}
}

// --- P0.4 authz gate tests ---
//
// The architecture check consumes kern's authz verdict BEFORE the boundary
// check when the change carries an agent identity: a denied verdict blocks
// (authz:unauthorized, boundary skipped); allowed/nil verdicts proceed;
// a probe failure degrades to a visible WARN (authz:verdict-error) without
// gating the boundary check.

// authzRunner answers the always-on `index status` probe with a FRESH index
// and dispatches guard invocations by whether they carry --agent-id: authz
// probes (with --agent-id) return authzOut/authzExit, boundary checks
// (without) return guardOut and increment *guardCalls. This lets tests assert
// both the authz verdict path AND whether the boundary check ran at all.
func authzRunner(authzOut, guardOut string, authzExit int, guardCalls *int) commandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "index" {
			if len(args) > 1 && args[1] == "--status" {
				return freshStatusJSON, "", 0, nil
			}
			return "", "", 0, nil
		}
		if len(args) > 0 && args[0] == "guard" {
			for _, a := range args {
				if a == "--agent-id" {
					return authzOut, "", authzExit, nil
				}
			}
			if guardCalls != nil {
				*guardCalls++
			}
			return guardOut, "", 0, nil
		}
		return guardOut, "", 0, nil
	}
}

const authzDeniedOut = `{"schema_version":2,"violations":[],"authz_verdict":{
	"schema_version":1,"agent_id":"agent-1","task":"test","decision":"denied",
	"policy_source":"default-scoped","denied_files":["web/web.go","db/db.go"],
	"fingerprint":"sha256:denied","decided_at":"2026-08-31T10:00:00Z"}}`

const authzAllowedOut = `{"schema_version":2,"violations":[],"authz_verdict":{
	"schema_version":1,"agent_id":"agent-1","task":"test","decision":"allowed",
	"policy_source":"task-scope","denied_files":[],
	"fingerprint":"sha256:allowed","decided_at":"2026-08-31T10:00:00Z"}}`

const cleanGuardOut = `{"schema_version":2,"violations": null}`

func TestArchitectureCheck_AuthzDenied_Blocks(t *testing.T) {
	guardCalls := 0
	client := &KernClient{binaryPath: "kern", runner: authzRunner(authzDeniedOut, cleanGuardOut, 2, &guardCalls)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		AgentID:        "agent-1",
		Metadata:       map[string]string{"task": "test"},
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want BLOCK", cr.Status)
	}
	if guardCalls != 0 {
		t.Errorf("boundary check ran %d time(s); want 0 (denied authz skips the boundary check)", guardCalls)
	}
	var found bool
	for _, f := range cr.Findings {
		if f.RuleID != "authz:unauthorized" {
			continue
		}
		found = true
		if f.Severity != domain.SeverityBlock {
			t.Errorf("severity = %s, want block", f.Severity)
		}
		if f.Category != domain.CategoryPolicy {
			t.Errorf("category = %s, want policy", f.Category)
		}
		if f.Confidence != 1.0 {
			t.Errorf("confidence = %v, want 1.0", f.Confidence)
		}
		if f.Scope != "repo" {
			t.Errorf("scope = %q, want repo", f.Scope)
		}
		if len(f.Evidence) != 2 || f.Evidence[0].Location != "web/web.go" {
			t.Errorf("evidence = %+v, want the two denied files as evidence", f.Evidence)
		}
	}
	if !found {
		t.Fatalf("no authz:unauthorized finding in %+v", cr.Findings)
	}
}

func TestArchitectureCheck_AuthzAllowed_Proceeds(t *testing.T) {
	guardCalls := 0
	client := &KernClient{binaryPath: "kern", runner: authzRunner(authzAllowedOut, cleanGuardOut, 0, &guardCalls)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		AgentID:        "agent-1",
		Metadata:       map[string]string{"task": "test"},
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want PASS (allowed verdict proceeds to the boundary check)", cr.Status)
	}
	if guardCalls != 1 {
		t.Errorf("boundary check ran %d time(s); want 1", guardCalls)
	}
	for _, f := range cr.Findings {
		if f.RuleID == "authz:unauthorized" {
			t.Errorf("unexpected authz:unauthorized finding: %+v", f)
		}
	}
}

func TestArchitectureCheck_AuthzNil_Proceeds(t *testing.T) {
	// No agent identity: no authz call at all, boundary check runs (backward
	// compat with every pre-P0.4 flow).
	guardCalls := 0
	client := &KernClient{binaryPath: "kern", runner: authzRunner(authzDeniedOut, cleanGuardOut, 2, &guardCalls)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want PASS (no AgentID -> no authz, boundary check runs)", cr.Status)
	}
	if guardCalls != 1 {
		t.Errorf("boundary check ran %d time(s); want 1", guardCalls)
	}
}

func TestArchitectureCheck_AuthzError_Warns(t *testing.T) {
	// A failing authz probe (exit 3 = tool failure) degrades to a visible
	// WARN; the boundary check still runs.
	guardCalls := 0
	client := &KernClient{binaryPath: "kern", runner: authzRunner("", cleanGuardOut, 3, &guardCalls)}
	chk := NewArchitectureCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		AgentID:        "agent-1",
		Metadata:       map[string]string{"task": "test"},
		Files:          []domain.FileChange{{Path: "web/web.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want WARN (authz probe error is visible, never a gate)", cr.Status)
	}
	if guardCalls != 1 {
		t.Errorf("boundary check ran %d time(s); want 1 (authz error must not skip the boundary check)", guardCalls)
	}
	var found bool
	for _, f := range cr.Findings {
		if f.RuleID != "authz:verdict-error" {
			continue
		}
		found = true
		if f.Severity != domain.SeverityWarn {
			t.Errorf("severity = %s, want warn", f.Severity)
		}
	}
	if !found {
		t.Fatalf("no authz:verdict-error WARN finding in %+v", cr.Findings)
	}
}
