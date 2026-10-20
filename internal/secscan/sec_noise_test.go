package secscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFileSkipsCodeExpressionSecrets(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestScanFileSkipsCanonicalExampleSecrets(t *testing.T) {
	t.Parallel()
	// Mirrors docs/adr/0011 (AWS example key in a bash demo line) and
	// sdk/contract/tools_call.json (fake token in the mask fixture):
	// canonical documentation fakes are not findings.
	src := []byte(`echo 'AWS_SECRET="AKIAIOSFODNN7EXAMPLE"' > secret.txt
{"args": { "text": "token=sk-abc123" },
 "other": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
echo 'AWS_SECRET="AKIA9X2KQ7W3ZP4RT6NB"' > real.txt
`)
	findings := ScanFile("docs/demo.md", src)
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" && f.Line != 4 {
			t.Errorf("canonical example secret flagged (noise): %+v", f)
		}
	}
	hit := false
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" && f.Line == 4 {
			hit = true
		}
	}
	if !hit {
		t.Errorf("real-looking key must still fire, got %+v", findings)
	}
}

func TestScanTreeSkipsFixtureDirs(t *testing.T) {
	t.Parallel()
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

// TestScanFileNetmaskInDocstringSuppressed (QA 2026-10-19): requests'
// utils.py:744 — "Example: if mask is 24 function returns 255.255.255.0" —
// was flagged as an error-severity hardcoded-secret IP. The netmask sits in
// a Python docstring (documentation), not a credential: address literals in
// comment/docstring spans must be suppressed for the IP label.
func TestScanFileNetmaskInDocstringSuppressed(t *testing.T) {
	src := []byte(`def dotted_netmask(mask: int) -> str:
    """Converts mask from /xx format to xxx.xxx.xxx.xxx

    Example: if mask is 24 function returns 255.255.255.0

    :rtype: str
    """
    bits = 0xFFFFFFFF ^ (1 << 32 - mask) - 1
    return socket.inet_ntoa(struct.pack(">I", bits))
`)
	for _, f := range ScanFile("src/requests/utils.py", src) {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "IP") {
			t.Fatalf("docstring netmask flagged as secret: %+v", f)
		}
	}
	// Positive control: the same address in real code (not a docstring)
	// must still be flagged — the suppression is span-scoped, not global.
	real := ScanFile("net.go", []byte("MASK = 255.255.255.0\n"))
	var caught bool
	for _, f := range real {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "secret: IP") {
			caught = true
		}
	}
	if !caught {
		t.Fatalf("netmask in real code must still be flagged, got %+v", real)
	}
}

// TestScanFilePythonSliceNotIPV6 (QA 2026-10-19): requests' utils.py:1028 —
// "if sample[1::2] == _null2" — was flagged as a bogus IPV6 match. A Python
// slice's single-char groups must not read as an address; the IPV6 rule
// requires ≥2-char hex groups.
func TestScanFilePythonSliceNotIPV6(t *testing.T) {
	src := []byte(`    if sample[::2] == _null2:  # 1st and 3rd are null
        if sample[1::2] == _null2:  # 2nd and 4th are null
            return "utf-16-le"
`)
	for _, f := range ScanFile("src/requests/utils.py", src) {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "IPV6") {
			t.Fatalf("python slice flagged as ipv6 secret: %+v", f)
		}
	}
	// Positive control: a real public IPv6 in code is still flagged.
	real := ScanFile("net.go", []byte("upstream := 2606:4700:4700::1111\n"))
	var caught bool
	for _, f := range real {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "secret: IPV6") {
			caught = true
		}
	}
	if !caught {
		t.Fatalf("public IPv6 in real code must still be flagged, got %+v", real)
	}
}
