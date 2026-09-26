package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestServeToolServerRootBound asserts the finding-1 root-binding: the web
// console's in-process tool server (built by the ROOT-AWARE factory wired in
// cmd_serve.go's init — web.SetToolServerFactory(func(root string) ...))
// confines every /v1/tools/{name} call to the App's OWN project root. A root
// arg inside App A's tree is served (200); a root outside it is refused
// (403) even though the test process chdirs elsewhere — an unrooted
// cwd-fallback server would have allowed the foreign root, which is exactly
// the enterprise-mode cross-project leak this test pins.
func TestServeToolServerRootBound(t *testing.T) {
	root := serveFixture(t)
	foreign := t.TempDir() // inside App A's tree → allowed; outside → denied
	t.Chdir(foreign)       // process cwd must NOT be the served root
	h, mode, err := buildServeHandler([]string{"--root", root, "--addr", ":0"})
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	if mode != "single-project" {
		t.Fatalf("expected mode %q, got %q", "single-project", mode)
	}
	app, ok := h.(*web.App)
	if !ok {
		t.Fatalf("expected *web.App handler, got %T", h)
	}

	// A root INSIDE the App's project root is confined-in and served.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/tools/kern_mask_pii", strings.NewReader(`{"text":"token=sk-x","root":"`+root+`"}`))
	req.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("in-root tool call = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "masked") {
		t.Errorf("in-root tool output missing masked result: %s", rec.Body.String())
	}

	// A root OUTSIDE App A's project root is refused (403): the server is
	// bound to App A's root, never to the process cwd.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/tools/kern_mask_pii", strings.NewReader(`{"text":"x","root":"`+foreign+`"}`))
	req.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("out-of-root tool call = %d, want 403 (body %s)", rec.Code, rec.Body.String())
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

// TestServeHelpPrintsUsageAndBareServeStarts asserts `serve --help` prints
// usage and never constructs a handler, while a bare `serve` (zero args)
// starts the server in single-project mode on the default address (or
// KERN_ADDR when set) instead of printing usage.
func TestServeHelpPrintsUsageAndBareServeStarts(t *testing.T) {
	h, mode, err := buildServeHandler([]string{"--help"})
	if err != nil {
		t.Fatalf("buildServeHandler(--help): %v", err)
	}
	if h != nil || mode != "" {
		t.Fatalf("--help must not construct a handler; got handler=%v mode=%q", h, mode)
	}
	// Bare `kern serve` (zero args): single-project mode on the default
	// address. Chdir to a fixture so web.New(".") has a tiny module to index.
	t.Chdir(serveFixture(t))
	h, mode, err = buildServeHandler(nil)
	if err != nil {
		t.Fatalf("buildServeHandler(no args): %v", err)
	}
	if h == nil {
		t.Fatal("bare serve must construct a handler (single-project mode)")
	}
	if mode != "single-project" {
		t.Fatalf("expected mode %q, got %q", "single-project", mode)
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
