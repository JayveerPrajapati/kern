package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/enterprise"
	"github.com/JayveerPrajapati/kern/internal/web"
)

// serveFixture writes a tiny Go module (go.mod + a helper func) so web.New can
// index it — mirrors internal/web's fixtureRoot.
func serveFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module servefixture\n\ngo 1.20\n",
		"main.go": "package main\n\nfunc helper() string { return \"h\" }\n\nfunc main() { _ = helper() }\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("abs %s: %v", p, err)
	}
	return abs
}

// TestServeSingleProject asserts `kern serve --root DIR --addr :0` builds a
// *web.App handler (single-project mode) that responds without binding a port.
func TestServeSingleProject(t *testing.T) {
	root := serveFixture(t)
	h, mode, err := buildServeHandler([]string{"--root", root, "--addr", ":0"})
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	if h == nil {
		t.Fatal("expected a handler for single-project mode")
	}
	if mode != "single-project" {
		t.Fatalf("expected mode %q, got %q", "single-project", mode)
	}
	app, ok := h.(*web.App)
	if !ok {
		t.Fatalf("expected *web.App handler, got %T", h)
	}
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, httptest.NewRequest("GET", "/api/health", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/health = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
}

// TestServeEnterprise asserts `kern serve --enterprise --project api=DIR1
// --project web=DIR2` builds an *enterprise.Server with both projects
// registered (absolute roots).
func TestServeEnterprise(t *testing.T) {
	root1 := serveFixture(t)
	root2 := serveFixture(t)
	h, mode, err := buildServeHandler([]string{
		"--enterprise",
		"--project", "api=" + root1,
		"--project", "web=" + root2,
	})
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	if h == nil {
		t.Fatal("expected a handler for enterprise mode")
	}
	if mode != "enterprise" {
		t.Fatalf("expected mode %q, got %q", "enterprise", mode)
	}
	srv, ok := h.(*enterprise.Server)
	if !ok {
		t.Fatalf("expected *enterprise.Server handler, got %T", h)
	}
	projects := srv.Projects()
	if len(projects) != 2 {
		t.Fatalf("expected 2 registered projects, got %d", len(projects))
	}
	if projects[0].Name != "api" || projects[1].Name != "web" {
		t.Fatalf("unexpected project names: %v", projects)
	}
	if projects[0].Root != mustAbs(t, root1) || projects[1].Root != mustAbs(t, root2) {
		t.Fatalf("project roots not registered absolute: %v", projects)
	}
}

// TestServeEnterpriseDefaultsToRoot asserts `kern serve --enterprise --root DIR`
// with no --project flags registers DIR as a single project named after its
// base directory.
func TestServeEnterpriseDefaultsToRoot(t *testing.T) {
	root := serveFixture(t)
	h, mode, err := buildServeHandler([]string{"--enterprise", "--root", root})
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	if h == nil {
		t.Fatal("expected a handler for enterprise mode")
	}
	if mode != "enterprise" {
		t.Fatalf("expected mode %q, got %q", "enterprise", mode)
	}
	srv, ok := h.(*enterprise.Server)
	if !ok {
		t.Fatalf("expected *enterprise.Server handler, got %T", h)
	}
	projects := srv.Projects()
	if len(projects) != 1 {
		t.Fatalf("expected 1 registered project (--root fallback), got %d", len(projects))
	}
	if want := filepath.Base(root); projects[0].Name != want {
		t.Fatalf("expected project named %q (base dir), got %q", want, projects[0].Name)
	}
	if projects[0].Root != mustAbs(t, root) {
		t.Fatalf("expected root %s, got %s", mustAbs(t, root), projects[0].Root)
	}
}

// TestServeHelpOrUsage asserts `serve --help` and bare `serve` print usage and
// never construct a handler (so no server starts).
func TestServeHelpOrUsage(t *testing.T) {
	h, mode, err := buildServeHandler([]string{"--help"})
	if err != nil {
		t.Fatalf("buildServeHandler(--help): %v", err)
	}
	if h != nil || mode != "" {
		t.Fatalf("--help must not construct a handler; got handler=%v mode=%q", h, mode)
	}
	h, mode, err = buildServeHandler(nil)
	if err != nil {
		t.Fatalf("buildServeHandler(no args): %v", err)
	}
	if h != nil || mode != "" {
		t.Fatalf("no args must not construct a handler; got handler=%v mode=%q", h, mode)
	}
}

// TestServeBadProjectFlag asserts malformed --project NAME=PATH registrations
// fail closed.
func TestServeBadProjectFlag(t *testing.T) {
	if _, _, err := buildServeHandler([]string{"--enterprise", "--project", "noequals"}); err == nil {
		t.Fatal("expected error for --project without NAME=PATH")
	}
	if _, _, err := buildServeHandler([]string{"--enterprise", "--project", "=root"}); err == nil {
		t.Fatal("expected error for --project with empty name")
	}
}
