package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/diffgate"
)

func fakeDocCatalog() []diffgate.ToolInfo {
	return []diffgate.ToolInfo{{Name: "kern_alpha", Phase: "explore", RiskLevel: "low", Description: "docs never drift"}}
}

func prepareDocRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	diffgate.SetToolInfos(fakeDocCatalog())
	if err := os.MkdirAll(filepath.Join(root, "docs", "mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(root, "docs", "tool-catalog.md")
	if err := os.WriteFile(catalog, diffgate.GenerateCatalogDoc(fakeDocCatalog()), 0o644); err != nil {
		t.Fatal(err)
	}
	contracts := filepath.Join(root, "docs", "mcp", "tool-contracts.md")
	if err := os.WriteFile(contracts, diffgate.GenerateContractsDoc(fakeDocCatalog()), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGenDocsCheckFreshCatalog(t *testing.T) {
	root := prepareDocRepo(t)
	catalog := filepath.Join(root, "docs", "tool-catalog.md")
	before, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		runGenDocsImpl([]string{"--root", root, "--doc", "catalog", "--check"}, "gen-docs", "", true)
	})
	if !strings.Contains(out, "fresh") {
		t.Fatalf("expected fresh output, got %q", out)
	}
	after, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("--check must not rewrite a fresh file")
	}
}

func TestGenDocsCheckStaleCatalogDoesNotRewrite(t *testing.T) {
	root := prepareDocRepo(t)
	catalog := filepath.Join(root, "docs", "tool-catalog.md")
	stale := []byte("stale doc")
	if err := os.WriteFile(catalog, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	expectExit(t, 1, func() {
		runGenDocsImpl([]string{"--root", root, "--doc", "catalog", "--check"}, "gen-docs", "", true)
	})
	after, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(stale) {
		t.Fatal("--check must not rewrite a stale file")
	}
}

func TestGenCatalogCheckFreshAndStale(t *testing.T) {
	root := prepareDocRepo(t)
	catalog := filepath.Join(root, "docs", "tool-catalog.md")
	out := captureStdout(t, func() {
		runGenDocsImpl([]string{"--root", root, "--check"}, "gen-catalog", "catalog", false)
	})
	if !strings.Contains(out, "fresh") {
		t.Fatalf("expected fresh output, got %q", out)
	}
	stale := []byte("stale catalog")
	if err := os.WriteFile(catalog, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	expectExit(t, 1, func() {
		runGenDocsImpl([]string{"--root", root, "--check"}, "gen-catalog", "catalog", false)
	})
}

func TestGenContractsCheckFreshAndStale(t *testing.T) {
	root := prepareDocRepo(t)
	contracts := filepath.Join(root, "docs", "mcp", "tool-contracts.md")
	out := captureStdout(t, func() {
		runGenDocsImpl([]string{"--root", root, "--check"}, "gen-contracts", "contracts", false)
	})
	if !strings.Contains(out, "fresh") {
		t.Fatalf("expected fresh output, got %q", out)
	}
	stale := []byte("stale contracts")
	if err := os.WriteFile(contracts, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	expectExit(t, 1, func() {
		runGenDocsImpl([]string{"--root", root, "--check"}, "gen-contracts", "contracts", false)
	})
}

func TestGenDocsCheckSchemasFreshAndStale(t *testing.T) {
	root := prepareDocRepo(t)
	schemas := filepath.Join(root, "docs", "mcp", "tool-schemas.json")
	expected, err := diffgate.GenerateSchemaBaseline(fakeDocCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemas, expected, 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		runGenDocsImpl([]string{"--root", root, "--doc", "schemas", "--check"}, "gen-docs", "", true)
	})
	if !strings.Contains(out, "fresh") {
		t.Fatalf("expected fresh output, got %q", out)
	}
	stale := []byte(`{"schema_version":1,"tools":[]}`)
	if err := os.WriteFile(schemas, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	expectExit(t, 1, func() {
		runGenDocsImpl([]string{"--root", root, "--doc", "schemas", "--check"}, "gen-docs", "", true)
	})
	after, err := os.ReadFile(schemas)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(stale) {
		t.Fatal("schemas --check must not rewrite a stale file")
	}
}

func TestGenDocsCheckSiteFreshAndStale(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, "docs", "index.md")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte("intro\n"+siteIndexStart+"\n- [a](a.md)\n"+siteIndexEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, expected, err := generatedDoc(root, "site")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, expected, 0o644); err != nil {
		t.Fatal(err)
	}
	runGenDocsImpl([]string{"--root", root, "--doc", "site", "--check"}, "gen-docs", "", true)
	stale := []byte("intro\n" + siteIndexStart + "\n- [missing](missing.md)\n" + siteIndexEnd + "\n")
	if err := os.WriteFile(indexPath, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	expectExit(t, 1, func() {
		runGenDocsImpl([]string{"--root", root, "--doc", "site", "--check"}, "gen-docs", "", true)
	})
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(stale) {
		t.Fatal("site --check must not rewrite a stale file")
	}
}
