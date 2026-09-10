package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFieldReceiverCallResolvedSameFile pins extract-time resolution: when
// the struct is declared in the same file as the call, a receiver-field
// chain ("a.taskSvc.Deploy") resolves fully to the field's type's method,
// and the caller lands in the canonical Callers map.
func TestFieldReceiverCallResolvedSameFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(`package lib

type TaskService struct{}

func (t *TaskService) Deploy() {}

type App struct {
	taskSvc *TaskService
}

func Use(a *App) {
	a.taskSvc.Deploy()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["TaskService.Deploy"]
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[TaskService.Deploy] = %v, want [Use] (same-file field chain resolved)", got)
	}
}

// TestFieldReceiverCallResolvedCrossFile pins the merge-time rewrite: the
// struct is declared in a different file than the call, so extract-time
// resolution stalls at the first segment ("App.taskSvc.Deploy") and the
// package-merged StructFields map completes it.
func TestFieldReceiverCallResolvedCrossFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(`package lib

type TaskService struct{}

func (t *TaskService) Deploy() {}

type App struct {
	taskSvc TaskService
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "use.go"), []byte(`package lib

func Use(a *App) {
	a.taskSvc.Deploy()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["TaskService.Deploy"]
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[TaskService.Deploy] = %v, want [Use] (cross-file field chain resolved at merge)", got)
	}
	// The stalled intermediate form must not leak into the canonical map.
	if got := ix.Callers["App.taskSvc.Deploy"]; len(got) != 0 {
		t.Fatalf("stale intermediate callee App.taskSvc.Deploy still has callers %v", got)
	}
}

// TestFieldReceiverDeepChain pins multi-level chains: "a.svc.client.M"
// resolves through two field hops.
func TestFieldReceiverDeepChain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(`package lib

type Client struct{}

func (c *Client) Send() {}

type Service struct {
	client *Client
}

type App struct {
	svc *Service
}

func Use(a *App) {
	a.svc.client.Send()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["Client.Send"]
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[Client.Send] = %v, want [Use] (deep field chain resolved)", got)
	}
}

// TestFieldReceiverEmbeddedField pins embedded struct fields: the field's Go
// name is the embedded type's name, so "a.TaskService.Deploy" resolves.
func TestFieldReceiverEmbeddedField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(`package lib

type TaskService struct{}

func (t *TaskService) Deploy() {}

type App struct {
	TaskService
}

func Use(a *App) {
	a.TaskService.Deploy()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["TaskService.Deploy"]
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[TaskService.Deploy] = %v, want [Use] (embedded field resolved)", got)
	}
}

// TestFieldReceiverForeignTypeStaysAliasOnly pins the conservative guard:
// a field whose type is NOT declared in the project must never be forged
// into a canonical caller — the chain stays alias-only, exactly as before
// the fix, so the delete gate still sees it but no local symbol inherits it.
func TestFieldReceiverForeignTypeStaysAliasOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(`package lib

type App struct {
	taskSvc *ExternalThing
}

func Use(a *App) {
	a.taskSvc.Deploy()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// ExternalThing is not declared in the project: the field rewrite must
	// not fire (types[] guard), so the foreign type's name must never appear
	// as a canonical key.
	if got := ix.Callers["ExternalThing.Deploy"]; len(got) != 0 {
		t.Fatalf("canonical Callers[ExternalThing.Deploy] = %v, want empty (foreign type must not be forged)", got)
	}
	// The conservative terminal form is the first-segment-resolved chain:
	// the caller is recorded there, invisible to any local symbol.
	if got := ix.Callers["App.taskSvc.Deploy"]; len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[App.taskSvc.Deploy] = %v, want [Use] (conservative terminal form)", got)
	}
	// Alias view still surfaces the caller, so DeleteCheck stays conservative.
	got := ix.CallersIncludingAliases("ExternalThing.Deploy")
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("CallersIncludingAliases(ExternalThing.Deploy) = %v, want [Use] (alias fallback preserved)", got)
	}
}

// TestFieldReceiverInterfaceField pins interface-typed fields: the field
// type resolves through the same map (interfaces are declared types).
func TestFieldReceiverInterfaceField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(`package lib

type Deployer interface {
	Deploy()
}

type TaskService struct{}

func (t *TaskService) Deploy() {}

type App struct {
	d Deployer
}

func Use(a *App) {
	a.d.Deploy()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["Deployer.Deploy"]
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[Deployer.Deploy] = %v, want [Use] (interface field resolved)", got)
	}
}

// TestVarInitializerCallsRecorded pins the package-level initializer gap:
// calls in var initializers ("var jsKw = kwSet(...)") run at init time and
// were invisible to per-function call walks — the deletion gate reported
// such symbols SAFE despite live production callers. They now land in the
// canonical Callers map under the declared name.
func TestVarInitializerCallsRecorded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kw.go"), []byte(`package lib

func kwSet(kws ...string) map[string]bool { return map[string]bool{} }

var jsKw = kwSet("if", "for", "while")

var props = map[string]string{
	"prompt": kwSet("x")["prompt"],
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["kwSet"]
	if len(got) != 2 {
		t.Fatalf("canonical Callers[kwSet] = %v, want [jsKw props] (var initializer calls recorded)", got)
	}
	for _, want := range []string{"jsKw", "props"} {
		found := false
		for _, c := range got {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Callers[kwSet] = %v, missing %s", got, want)
		}
	}
}

// TestVarInitializerMethodCallRecorded pins method calls in var
// initializers: they are recorded under the declared name and stay visible
// through the alias view (the canonical name is unresolvable without a
// function scope, exactly like the existing receiver-call fallback).
func TestVarInitializerMethodCallRecorded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "init.go"), []byte(`package lib

type TaskService struct{}

func (t *TaskService) Deploy() {}

func newTS() *TaskService { return &TaskService{} }

var defaultSvc = newTS().Deploy()
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The chain "newTS().Deploy" cannot be resolved at package scope; the
	// alias view must still surface the initializer as a caller.
	got := ix.CallersIncludingAliases("TaskService.Deploy")
	found := false
	for _, c := range got {
		if c == "defaultSvc" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("CallersIncludingAliases(TaskService.Deploy) = %v, want defaultSvc among callers", got)
	}
}
