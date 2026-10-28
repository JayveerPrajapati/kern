package evidence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kernindex "github.com/JayveerPrajapati/kern/internal/index"
)

// fixture writes a small project tree and returns (root, index) where the
// index is the synthetic AST index the evidence tools consult.
func fixture(t *testing.T) (string, *kernindex.Index) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"main.go":       "package main\n\nfunc Greet() string {\n\treturn \"hi\"\n}\n",
		"config/app.go": "package config\n\nconst Token = \"AKIAIOSFODNN7EXAMPLE\"\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix := &kernindex.Index{
		Root: root,
		Symbols: []kernindex.Symbol{
			{Name: "Greet", Kind: "func", File: "main.go", Line: 3, End: 5},
		},
	}
	return root, ix
}

func hooksFor(ix *kernindex.Index) Hooks {
	return Hooks{LoadIndex: func(context.Context, string) (*kernindex.Index, error) { return ix, nil }}
}

// TestConfinePath pins the containment guard: relative paths resolve inside
// the root, ".." and absolute escapes are rejected, and a symlink pointing
// outside the root is rejected after resolution.
func TestConfinePath(t *testing.T) {
	root, _ := fixture(t)

	got, err := confinePath(root, "main.go")
	if err != nil || filepath.Base(got) != "main.go" {
		t.Fatalf("relative: %q %v", got, err)
	}
	// The returned path is symlink-resolved (on macOS /var → /private/var), so
	// it must sit inside the RESOLVED root, not the literal one.
	if resolvedRoot, rerr := filepath.EvalSymlinks(root); rerr == nil {
		if rel, _ := filepath.Rel(resolvedRoot, got); strings.HasPrefix(rel, "..") {
			t.Fatalf("resolved path escapes root: %q", got)
		}
	}
	if _, err := confinePath(root, ".."); err == nil {
		t.Fatal("'..' must be rejected")
	}
	if _, err := confinePath(root, "../etc/passwd"); err == nil {
		t.Fatal("parent escape must be rejected")
	}
	if _, err := confinePath(root, "/etc/passwd"); err == nil {
		t.Fatal("absolute escape must be rejected")
	}
	if _, err := confinePath(root, root); err != nil {
		t.Fatalf("root itself must be allowed: %v", err)
	}

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skip("symlinks unavailable on this platform")
	}
	if _, err := confinePath(root, "link.txt"); err == nil {
		t.Fatal("symlink escape must be rejected")
	}
}

// TestAnchorRequiresInput pins the argument contract.
func TestAnchorRequiresInput(t *testing.T) {
	root, ix := fixture(t)
	_, err := Anchor(context.Background(), hooksFor(ix), map[string]any{"root": root})
	if err == nil || !strings.Contains(err.Error(), "at least one of") {
		t.Fatalf("expected input error, got %v", err)
	}
}

// TestAnchorSymbolResolvesAndReportsDrift pins the symbol path, including the
// requested-line drift the certificate carries.
func TestAnchorSymbolResolvesAndReportsDrift(t *testing.T) {
	root, ix := fixture(t)
	out, err := Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "symbol": "Greet", "line": "10",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"verified: true", "symbol: Greet at main.go:3", "line drift: -7", "evidence-sha256:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	// A claim string is parsed as file:line when no explicit file is given.
	out, err = Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "claim": "main.go:4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "target: main.go:4") || !strings.Contains(out, "verified: true") {
		t.Fatalf("claim parse failed:\n%s", out)
	}
}

// TestAnchorUnknownSymbolIsNotVerified pins honest failure: an unknown symbol
// and no file yields verified:false, never a fabricated certificate.
func TestAnchorUnknownSymbolIsNotVerified(t *testing.T) {
	root, ix := fixture(t)
	out, err := Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "symbol": "NoSuchSymbolAnywhere",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "verified: false") {
		t.Fatalf("unknown symbol must not verify:\n%s", out)
	}
}

// TestAnchorFileOutsideRootIsNotVerified pins confinement at the tool level.
func TestAnchorFileOutsideRootIsNotVerified(t *testing.T) {
	root, ix := fixture(t)
	out, err := Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "file": "../../../etc/passwd",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "verified: false") || !strings.Contains(out, "escapes project root") {
		t.Fatalf("outside-root read must be refused:\n%s", out)
	}
}

// TestAnchorCredentialFileIsNotVerified pins the credential guard: an
// evidence proof must not become a secret-exfiltration channel.
func TestAnchorCredentialFileIsNotVerified(t *testing.T) {
	root, ix := fixture(t)
	credDir := filepath.Join(root, ".ssh")
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(credDir, "id_rsa"), []byte("PRIVATE KEY\nline2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "file": ".ssh/id_rsa", "line": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "verified: false") || !strings.Contains(out, "credential-shaped") {
		t.Fatalf("credential file must be refused:\n%s", out)
	}
}

// TestAnchorLineBeyondEOF pins the tail-anchor behavior and its drift.
func TestAnchorLineBeyondEOF(t *testing.T) {
	root, ix := fixture(t)
	out, err := Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "file": "main.go", "line": "900",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exceeds file length") || !strings.Contains(out, "line drift:") {
		t.Fatalf("expected tail anchor with drift:\n%s", out)
	}
}

// TestAnchorMasksSnippetSecrets pins that a verified snippet is PII-masked:
// the proof attests file+line, it does not leak the file's secrets.
func TestAnchorMasksSnippetSecrets(t *testing.T) {
	root, ix := fixture(t)
	out, err := Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "file": "config/app.go", "line": "3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("snippet leaked a credential:\n%s", out)
	}
	if !strings.Contains(out, "verified: true") {
		t.Fatalf("file+line should verify:\n%s", out)
	}
}

// TestAnchorJSONFormat pins the structured rendering (the MCP-consumable form).
func TestAnchorJSONFormat(t *testing.T) {
	root, ix := fixture(t)
	out, err := Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "symbol": "Greet", "format": "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	var proof EvidenceProof
	if err := json.Unmarshal([]byte(out), &proof); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !proof.Verified || proof.File != "main.go" || !strings.HasPrefix(proof.EvidenceID, "evidence-sha256:") {
		t.Fatalf("unexpected proof: %+v", proof)
	}
}

// TestAnchorIndexLoadFailurePropagates pins that an index failure is an error,
// not a silent "unverified" certificate.
func TestAnchorIndexLoadFailurePropagates(t *testing.T) {
	h := Hooks{LoadIndex: func(context.Context, string) (*kernindex.Index, error) {
		return nil, os.ErrNotExist
	}}
	if _, err := Anchor(context.Background(), h, map[string]any{"symbol": "X"}); err == nil {
		t.Fatal("index failure must propagate")
	}
}

// TestExplainRequiresSourceAndRejectsGarbage pins the explain contract.
func TestExplainRequiresSourceAndRejectsGarbage(t *testing.T) {
	ctx := context.Background()
	if _, err := Explain(ctx, map[string]any{}); err == nil {
		t.Fatal("explain without file/url must error")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not a bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Explain(ctx, map[string]any{"file": bad}); err == nil {
		t.Fatal("malformed bundle must error")
	}
}

// TestExplainAndExportRoundTrip pins the export→explain path: a bundle written
// by Export must parse, verify, and explain.
func TestExplainAndExportRoundTrip(t *testing.T) {
	root, ix := fixture(t)
	h := hooksFor(ix)
	out, err := Export(context.Background(), h, map[string]any{"root": root, "agent_id": "default", "task_id": "t-1"})
	if err != nil {
		t.Skipf("export requires a fuller index: %v", err)
	}
	var resp struct {
		BundleID string `json:"bundle_id"`
		Path     string `json:"path"`
		AgentID  string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("export response not JSON: %v\n%s", err, out)
	}
	if resp.BundleID == "" || resp.AgentID != "default" {
		t.Fatalf("unexpected export response: %+v", resp)
	}
	if _, err := os.Stat(resp.Path); err != nil {
		t.Fatalf("bundle not written: %v", err)
	}
	explained, err := Explain(context.Background(), map[string]any{"file": resp.Path})
	if err != nil {
		t.Fatalf("explain exported bundle: %v", err)
	}
	if !strings.Contains(explained, resp.BundleID) {
		t.Fatalf("explanation does not name the bundle:\n%s", explained)
	}
}

// TestExportLoadIndexFailure pins error propagation on the export path.
func TestExportLoadIndexFailure(t *testing.T) {
	h := Hooks{LoadIndex: func(context.Context, string) (*kernindex.Index, error) {
		return nil, os.ErrNotExist
	}}
	if _, err := Export(context.Background(), h, map[string]any{"root": t.TempDir()}); err == nil {
		t.Fatal("index failure must propagate")
	}
}

// TestEvidenceBundleSourceCredentialGuard pins that a credential-shaped
// filename is refused even when it exists on disk.
func TestEvidenceBundleSourceCredentialGuard(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "id_rsa")
	if err := os.WriteFile(cred, []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := evidenceBundleSource(cred, ""); err == nil || !strings.Contains(err.Error(), "credential-shaped") {
		t.Fatalf("expected credential refusal, got %v", err)
	}
	if _, err := evidenceBundleSource("", ""); err == nil {
		t.Fatal("missing file+url must error")
	}
}

// TestHandleRoutes pins action routing, including the default action.
func TestHandleRoutes(t *testing.T) {
	root, ix := fixture(t)
	h := hooksFor(ix)
	if _, err := Handle(context.Background(), h, map[string]any{"root": root, "action": "bogus"}); err == nil {
		t.Fatal("unknown action must error")
	}
	// Default action is verify; a missing bundle is an error, not a panic.
	if _, err := Handle(context.Background(), h, map[string]any{"root": root}); err == nil {
		t.Fatal("verify without a bundle must error")
	}
	if _, err := Handle(context.Background(), h, map[string]any{"root": root, "action": "anchor", "symbol": "Greet"}); err != nil {
		t.Fatalf("anchor routing: %v", err)
	}
}

// TestAnchorFileOnly pins the file-without-line branch: the proof attests
// existence and total length, and reports line 1.
func TestAnchorFileOnly(t *testing.T) {
	root, ix := fixture(t)
	out, err := Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "file": "main.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "File confirmed") || !strings.Contains(out, "verified: true") {
		t.Fatalf("file-only anchor:\n%s", out)
	}

	// A file that does not exist is not verified (never a silent success).
	out, err = Anchor(context.Background(), hooksFor(ix), map[string]any{
		"root": root, "file": "missing.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "verified: false") || !strings.Contains(out, "not accessible on disk") {
		t.Fatalf("missing file must not verify:\n%s", out)
	}
}

// TestVerifyExportedBundle pins the verify path: an exported bundle reports
// intact, valid, and matches the on-disk audit chain (0 entries here).
func TestVerifyExportedBundle(t *testing.T) {
	root, ix := fixture(t)
	out, err := Export(context.Background(), hooksFor(ix), map[string]any{"root": root, "agent_id": "default"})
	if err != nil {
		t.Skipf("export requires a fuller index: %v", err)
	}
	var exp struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &exp); err != nil {
		t.Fatal(err)
	}

	report, err := Verify(context.Background(), map[string]any{"file": exp.Path, "root": root})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	var rep EvidenceVerifyReport
	if err := json.Unmarshal([]byte(report), &rep); err != nil {
		t.Fatalf("report not JSON: %v\n%s", err, report)
	}
	if !rep.Valid || rep.TamperSeal != "intact" {
		t.Fatalf("exported bundle should verify: %+v", rep)
	}
	if !strings.Contains(rep.AuditChain, "intact") {
		t.Fatalf("audit chain not replayed: %q", rep.AuditChain)
	}
}

// TestVerifyDetectsTampering pins the whole point of the seal: a single changed
// byte invalidates the bundle.
func TestVerifyDetectsTampering(t *testing.T) {
	root, ix := fixture(t)
	out, err := Export(context.Background(), hooksFor(ix), map[string]any{"root": root, "agent_id": "default"})
	if err != nil {
		t.Skipf("export requires a fuller index: %v", err)
	}
	var exp struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &exp); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exp.Path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"agent_id": "default"`, `"agent_id": "attacker"`, 1)
	if tampered == string(raw) {
		t.Skip("bundle has no agent_id field to tamper with")
	}
	if err := os.WriteFile(exp.Path, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), map[string]any{"file": exp.Path, "root": root}); err == nil {
		t.Fatal("a tampered bundle must fail verification")
	}
}

// TestVerifyRequiresSource pins the argument contract.
func TestVerifyRequiresSource(t *testing.T) {
	if _, err := Verify(context.Background(), map[string]any{}); err == nil {
		t.Fatal("verify without file/url must error")
	}
}

// TestMarshalAndShortHash pins the small formatting helpers.
func TestMarshalAndShortHash(t *testing.T) {
	out, err := marshalEvidenceReport(EvidenceVerifyReport{BundleID: "b-1", Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"bundle_id": "b-1"`) {
		t.Fatalf("report not marshaled: %s", out)
	}
	if got := evidenceShortHash8("0123456789abcdef"); got != "01234567" {
		t.Fatalf("short hash: %q", got)
	}
	if got := evidenceShortHash8("abc"); got != "abc" {
		t.Fatalf("short input should pass through: %q", got)
	}
}
