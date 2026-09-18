package sec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFileSkipsCodeExpressionSecrets(t *testing.T) {
	src := []byte(`package evidence

import "crypto/ed25519"

func Key(priv []byte, pub []byte) *KeyPair {
	return &KeyPair{
		PublicKey:   ed25519.PublicKey(pub),
		PrivateKey:  ed25519.PrivateKey(priv),
	}
}

var client_secret = getSecret1()

const apiKey = "sk-abcdefghijklmnopqrstuvwxyz1234567890"
`)
	findings := ScanFile("internal/evidence/keys.go", src)
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" && (f.Line == 8 || f.Line == 12) {
			t.Errorf("code expression flagged as secret (P1-6): %+v", f)
		}
	}
	hit := false
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" && f.Line == 14 {
			hit = true
		}
	}
	if !hit {
		t.Errorf("quoted literal must still fire, got %+v", findings)
	}
}

// TestScanFileDedupesSameLineSameRule pins the line-scoped contract: two
// md5.Sum calls on one line (doctor.go:422) are a single weak-crypto
// finding, not two.
func TestScanFileDedupesSameLineSameRule(t *testing.T) {
	src := []byte(`package doctor

import "crypto/md5"

func stale(cur, src []byte) string {
	return sumText(md5.Sum(cur), md5.Sum(src))
}
`)
	var weak []Finding
	for _, f := range ScanFile("internal/doctor/doctor.go", src) {
		if f.Rule == "weak-crypto" {
			weak = append(weak, f)
		}
	}
	if len(weak) != 1 {
		t.Fatalf("expected 1 weak-crypto finding for one line, got %+v", weak)
	}
}

func TestScanTreeSkipsFixtureDirs(t *testing.T) {
	dir := t.TempDir()
	// Single-label secret (mirrors TestScanTreeSkipsTestFixturesEverywhere):
	// one credential, one finding — the test pins directory skipping, not
	// label overlap.
	secret := "const k = \"sk-abcdefghijklmnopqrstuvwxyz1234567890\"\n"
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.go", "package app\n"+secret)
	write("testfixture/f.go", "package testfixture\n"+secret)
	write("fixtures/x.go", "package fixtures\n"+secret)
	write("testfixture/query.go", "package testfixture\nfunc Get(id string) string {\n\tres, _ := u.db.Query(\"user:\" + id)\n\treturn res\n}\n")
	findings, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.Contains(f.File, "testfixture") || strings.Contains(f.File, "fixtures") {
			t.Errorf("fixture dir must be skipped, got %+v", f)
		}
	}
	if len(findings) != 1 || findings[0].File != "app.go" {
		t.Fatalf("expected exactly the app.go finding, got %+v", findings)
	}
}
