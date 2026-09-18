package verification

import (
	"strings"
	"testing"
)

// hasFinding reports whether findings contains a string with substr.
func hasFinding(findings []string, substr string) bool {
	for _, f := range findings {
		if strings.Contains(f, substr) {
			return true
		}
	}
	return false
}

func TestManifestNodeDeps(t *testing.T) {
	// Clean: pinned versions in both sections.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"package.json": `{"name":"x","dependencies":{"express":"^4.0.0"},"devDependencies":{"typescript":"~5.0.0"}}`,
	})
	mc := checkManifestDeps(dir)
	if mc.skipped || !mc.ok {
		t.Fatalf("clean node manifest: skipped=%v ok=%v findings=%v", mc.skipped, mc.ok, mc.findings)
	}
	if mc.ecosystem != "node" {
		t.Errorf("ecosystem = %q, want node", mc.ecosystem)
	}
	if len(mc.findings) != 0 {
		t.Errorf("clean node manifest must have no findings, got %v", mc.findings)
	}

	// Unpinned: "*", "latest", empty.
	dir2 := t.TempDir()
	writeTree(t, dir2, map[string]string{
		"package.json": `{"dependencies":{"express":"*","lodash":"latest","left-pad":""}}`,
	})
	mc2 := checkManifestDeps(dir2)
	if len(mc2.findings) != 3 {
		t.Fatalf("expected 3 unpinned findings, got %v", mc2.findings)
	}
	for _, want := range []string{"unpinned dependency express", "unpinned dependency lodash", "unpinned dependency left-pad"} {
		if !hasFinding(mc2.findings, want) {
			t.Errorf("missing finding %q in %v", want, mc2.findings)
		}
	}

	// Duplicate across dependencies + devDependencies.
	dir3 := t.TempDir()
	writeTree(t, dir3, map[string]string{
		"package.json": `{"dependencies":{"a":"1.0.0"},"devDependencies":{"a":"2.0.0"}}`,
	})
	mc3 := checkManifestDeps(dir3)
	if !hasFinding(mc3.findings, "duplicate dependency a") {
		t.Errorf("expected duplicate finding, got %v", mc3.findings)
	}

	// Unparseable JSON: fail-closed.
	dir4 := t.TempDir()
	writeTree(t, dir4, map[string]string{"package.json": `{"dependencies": [`})
	mc4 := checkManifestDeps(dir4)
	if mc4.ok {
		t.Error("unparseable package.json must be fail-closed (ok=false)")
	}
	if !hasFinding(mc4.findings, "could not run (package.json)") {
		t.Errorf("expected fail-closed finding, got %v", mc4.findings)
	}
}

func TestManifestPythonDeps(t *testing.T) {
	// Clean: pinned and ranged specifiers.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"requirements.txt": "requests==2.31.0\nflask>=2.0\npytest~=7.0\nPillow[image]==9.0\n",
	})
	mc := checkManifestDeps(dir)
	if mc.skipped || !mc.ok || len(mc.findings) != 0 {
		t.Fatalf("clean requirements: skipped=%v ok=%v findings=%v", mc.skipped, mc.ok, mc.findings)
	}
	if mc.ecosystem != "python" {
		t.Errorf("ecosystem = %q, want python", mc.ecosystem)
	}

	// Unpinned + duplicate (bare name), extras normalized.
	dir2 := t.TempDir()
	writeTree(t, dir2, map[string]string{
		"requirements.txt": "requests\nrequests==2.0.0\n",
	})
	mc2 := checkManifestDeps(dir2)
	if !hasFinding(mc2.findings, "unpinned dependency requests") {
		t.Errorf("expected unpinned finding, got %v", mc2.findings)
	}
	if !hasFinding(mc2.findings, "duplicate dependency requests") {
		t.Errorf("expected duplicate finding, got %v", mc2.findings)
	}

	// Comments, options and recursion lines are skipped.
	dir3 := t.TempDir()
	writeTree(t, dir3, map[string]string{
		"requirements.txt": "# comment\n-r other.txt\n-e .\n--index-url https://x\n\nrequests==2.0\n",
	})
	mc3 := checkManifestDeps(dir3)
	if len(mc3.findings) != 0 {
		t.Errorf("options/comment lines must be skipped, got %v", mc3.findings)
	}
}

func TestManifestMavenDeps(t *testing.T) {
	// Clean with xmlns (namespace must not break local-name matching).
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"pom.xml": `<?xml version="1.0"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <dependencies>
    <dependency><groupId>org.junit</groupId><artifactId>junit</artifactId><version>4.13.2</version></dependency>
    <dependency><groupId>com.google.guava</groupId><artifactId>guava</artifactId><version>33.0.0</version></dependency>
  </dependencies>
</project>`,
	})
	mc := checkManifestDeps(dir)
	if mc.skipped || !mc.ok || len(mc.findings) != 0 {
		t.Fatalf("clean pom: skipped=%v ok=%v findings=%v", mc.skipped, mc.ok, mc.findings)
	}
	if mc.ecosystem != "maven" {
		t.Errorf("ecosystem = %q, want maven", mc.ecosystem)
	}

	// Unpinned (missing version) + duplicate.
	dir2 := t.TempDir()
	writeTree(t, dir2, map[string]string{
		"pom.xml": `<project><dependencies>
<dependency><groupId>g</groupId><artifactId>a</artifactId></dependency>
<dependency><groupId>g</groupId><artifactId>a</artifactId><version>1.0</version></dependency>
</dependencies></project>`,
	})
	mc2 := checkManifestDeps(dir2)
	if !hasFinding(mc2.findings, "unpinned dependency g:a") {
		t.Errorf("expected unpinned finding, got %v", mc2.findings)
	}
	if !hasFinding(mc2.findings, "duplicate dependency g:a") {
		t.Errorf("expected duplicate finding, got %v", mc2.findings)
	}

	// Unparseable XML: fail-closed.
	dir3 := t.TempDir()
	writeTree(t, dir3, map[string]string{"pom.xml": `<project><dependencies>`})
	mc3 := checkManifestDeps(dir3)
	if mc3.ok {
		t.Error("unparseable pom.xml must be fail-closed (ok=false)")
	}
}

func TestManifestRustDeps(t *testing.T) {
	// Clean: version, inline table, path and git deps are all pinned; dev and
	// target deps sections are honored; workspace deps are not direct deps.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Cargo.toml": `[package]
name = "x"
[dependencies]
serde = "1.0"
tokio = { version = "1.3", features = ["full"] }
local = { path = "../local" }
gitdep = { git = "https://example.com/x" }
[dev-dependencies]
crit = "0.1"
[workspace.dependencies]
shared = "2.0"
[target.'cfg(unix)'.dependencies]
libc = "0.2"
`,
	})
	mc := checkManifestDeps(dir)
	if mc.skipped || !mc.ok || len(mc.findings) != 0 {
		t.Fatalf("clean Cargo.toml: skipped=%v ok=%v findings=%v", mc.skipped, mc.ok, mc.findings)
	}
	if mc.ecosystem != "rust" {
		t.Errorf("ecosystem = %q, want rust", mc.ecosystem)
	}

	// Unpinned: bare inline table without version/path/git; [dependencies.x]
	// sub-table without version.
	dir2 := t.TempDir()
	writeTree(t, dir2, map[string]string{
		"Cargo.toml": `[dependencies]
foo = { features = ["x"] }
[dependencies.bar]
path2 = "ignored"
`,
	})
	mc2 := checkManifestDeps(dir2)
	if !hasFinding(mc2.findings, "unpinned dependency foo") {
		t.Errorf("expected unpinned finding for inline table, got %v", mc2.findings)
	}
	if !hasFinding(mc2.findings, "unpinned dependency bar") {
		t.Errorf("expected unpinned finding for sub-table, got %v", mc2.findings)
	}

	// Duplicate.
	dir3 := t.TempDir()
	writeTree(t, dir3, map[string]string{
		"Cargo.toml": `[dependencies]
a = "1.0"
[dependencies]
a = "2.0"
`,
	})
	mc3 := checkManifestDeps(dir3)
	if !hasFinding(mc3.findings, "duplicate dependency a") {
		t.Errorf("expected duplicate finding, got %v", mc3.findings)
	}
}

func TestManifestDepsNoManifestSkips(t *testing.T) {
	dir := t.TempDir()
	mc := checkManifestDeps(dir)
	if !mc.skipped {
		t.Fatal("no-manifest project must be an honest skip")
	}
	if !strings.Contains(mc.skippedNote, "no supported dependency manifest") {
		t.Errorf("skip note = %q", mc.skippedNote)
	}
}

func TestManifestDepsMultiEcosystem(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"package.json": `{"dependencies":{"express":"*"}}`,
		"Cargo.toml": `[dependencies]
foo = "1.0"`,
	})
	mc := checkManifestDeps(dir)
	if mc.ecosystem != "node+rust" {
		t.Errorf("ecosystem = %q, want node+rust", mc.ecosystem)
	}
	if !hasFinding(mc.findings, "unpinned dependency express") {
		t.Errorf("expected merged node finding, got %v", mc.findings)
	}
	if len(mc.findings) != 1 {
		t.Errorf("expected exactly the node finding, got %v", mc.findings)
	}
}

func TestManifestDepsGoUnchanged(t *testing.T) {
	// Go semantics are untouched: the check runs (ok=true) and the missing
	// module is surfaced as a finding; the engine turns findings into FAIL.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":  "module depfixture\n\ngo 1.20\n",
		"main.go": "package main\nimport \"example.com/missing/lib\"\nvar _ = lib.X\n",
	})
	mc := checkManifestDeps(dir)
	if mc.skipped || !mc.ok {
		t.Fatalf("go manifest: skipped=%v ok=%v findings=%v", mc.skipped, mc.ok, mc.findings)
	}
	if !hasFinding(mc.findings, "missing module for import example.com/missing/lib") {
		t.Errorf("expected Go missing-module finding, got %v", mc.findings)
	}
	if mc.ecosystem != "go" {
		t.Errorf("ecosystem = %q, want go", mc.ecosystem)
	}
}
