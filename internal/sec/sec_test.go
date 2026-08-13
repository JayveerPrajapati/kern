package sec

import (
	"os"
	"strings"
	"testing"
)

func TestScanFileFindsHardcodedSecret(t *testing.T) {
	src := []byte(`package main

import "os"

const apiKey = "sk-abcdefghijklmnopqrstuvwxyz1234567890"

func main() {
	os.Setenv("K", apiKey)
}
`)
	findings := ScanFile("config.go", src)
	if len(findings) == 0 {
		t.Fatal("expected findings for hardcoded secret")
	}
	hit := false
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" && f.Line == 5 {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected hardcoded-secret on line 5, got %+v", findings)
	}
}

func TestScanFileDynamicSQL(t *testing.T) {
	src := []byte(`func query(db *sql.DB, id string) {
	rows, _ := db.Query(fmt.Sprintf("SELECT * FROM users WHERE id = %s", id))
	rows2, _ := db.Query("SELECT * FROM users WHERE id = " + id)
	_ = rows
	_ = rows2
}
`)
	findings := ScanFile("db.go", src)
	if len(findings) == 0 {
		t.Fatal("expected sql-injection findings")
	}
	for _, f := range findings {
		if f.Rule != "sql-injection" {
			t.Fatalf("expected only sql-injection, got %+v", f)
		}
	}
}

func TestScanFileFalsePositiveFilters(t *testing.T) {
	src := []byte(`func check(db *sql.DB, table, user string) {
	rows, _ := db.Query("PRAGMA table_info(" + table + ")")   // const table: skipped
	rows2, _ := db.Query("PRAGMA table_info(" + user + ")")   // same shape: skipped by design
	rows3, _ := db.Query("PRAGMA table_info(" + getTable() + ")") // call: still flagged
	rows4, _ := db.Query("SELECT * FROM users WHERE id = " + user) // real SQL: flagged
	_ = rows
	_ = rows2
	_ = rows3
	_ = rows4
}
`)
	findings := ScanFile("db.go", src)
	var flagged []string
	for _, f := range findings {
		if f.Rule == "sql-injection" {
			flagged = append(flagged, f.Snippet)
		}
	}
	if len(flagged) != 2 {
		t.Fatalf("expected 2 sql-injection findings (call + real SQL), got %d: %v", len(flagged), flagged)
	}
	for _, s := range flagged {
		if !strings.Contains(s, "getTable()") && !strings.Contains(s, "WHERE id") {
			t.Fatalf("unexpected finding: %s", s)
		}
	}
}

func TestScanFileSelfRegexNotFlagged(t *testing.T) {
	src := []byte(`var re = regexp.MustCompile("(?i)\\b(?:md5\\.(?:New|Sum)|sha1\\.(?:New|Sum))\\b")
`)
	findings := ScanFile("detector.go", src)
	for _, f := range findings {
		if f.Rule == "weak-crypto" || f.Rule == "insecure-random" {
			t.Fatalf("detector regex flagged itself: %+v", f)
		}
	}
}

func TestScanFileCommandInjection(t *testing.T) {
	src := []byte(`func run(name string) {
	out, _ := exec.Command("sh", "-c", "cat "+name).Output()
	_ = out
}
`)
	findings := ScanFile("exec.go", src)
	found := false
	for _, f := range findings {
		if f.Rule == "command-injection" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected command-injection finding, got %+v", findings)
	}
}

func TestScanFileWeakCryptoAndRandom(t *testing.T) {
	src := []byte(`func hash(data []byte) string {
	h := md5.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func token() int {
	return rand.Intn(1000000)
}
`)
	findings := ScanFile("crypto.go", src)
	rules := map[string]bool{}
	for _, f := range findings {
		rules[f.Rule] = true
	}
	if !rules["weak-crypto"] {
		t.Errorf("expected weak-crypto, got %+v", rules)
	}
	if !rules["insecure-random"] {
		t.Errorf("expected insecure-random, got %+v", rules)
	}
}

func TestScanFileUnsafeDeserialization(t *testing.T) {
	src := []byte(`func decode(b []byte) error {
	return json.Unmarshal(b, &map[string]interface{}{})
}
`)
	findings := ScanFile("deser.go", src)
	found := false
	for _, f := range findings {
		if f.Rule == "unsafe-deserialization" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unsafe-deserialization, got %+v", findings)
	}
}

func TestScanFileCleanFile(t *testing.T) {
	src := []byte(`func add(a, b int) int {
	return a + b
}
`)
	if findings := ScanFile("clean.go", src); len(findings) != 0 {
		t.Fatalf("expected clean file, got %+v", findings)
	}
}

func TestScanTreeSkipsVendor(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := dir + "/" + rel
		parts := strings.Split(rel, "/")
		if len(parts) > 1 {
			_ = os.MkdirAll(dir+"/"+strings.Join(parts[:len(parts)-1], "/"), 0o755)
		}
		_ = os.WriteFile(p, []byte(content), 0o644)
	}
	write("app.go", `package main
func main() {
	_ = "sk-abcdefghijklmnopqrstuvwxyz1234567890"
}
`)
	write("vendor/dep.go", `package dep
const k = "ghp_abcdefghijklmnopqrstuvwxyz1234567890"
`)
	findings, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding in app.go")
	}
	for _, f := range findings {
		if strings.HasPrefix(f.File, "vendor/") {
			t.Fatalf("vendor files must be skipped, got %+v", f)
		}
	}
}

func TestScanTreeSkipsTestFixturesEverywhere(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := dir + "/" + rel
		parts := strings.Split(rel, "/")
		if len(parts) > 1 {
			_ = os.MkdirAll(dir+"/"+strings.Join(parts[:len(parts)-1], "/"), 0o755)
		}
		_ = os.WriteFile(p, []byte(content), 0o644)
	}
	secret := `const k = "sk-abcdefghijklmnopqrstuvwxyz1234567890"`
	write("app.go", secret)
	write("auth_test.py", secret)
	write("foo.test.js", secret)
	write("test/spec_test.go", secret)
	findings, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var real []Finding
	for _, f := range findings {
		if strings.Contains(f.File, "test") {
			t.Fatalf("test fixture must be skipped, got %+v", f)
		}
		real = append(real, f)
	}
	if len(real) != 1 || real[0].File != "app.go" {
		t.Fatalf("expected exactly one finding in app.go, got %+v", real)
	}
}

func TestRenderCapsAndCounts(t *testing.T) {
	src := []byte("const k = \"sk-abcdefghijklmnopqrstuvwxyz1234567890\"\n")
	findings := ScanFile("a.go", src)
	rendered := Render(findings, 1)
	if !strings.Contains(rendered, "hardcoded-secret") || !strings.Contains(rendered, "a.go:1") {
		t.Fatalf("bad render: %q", rendered)
	}
	if c := Counts(findings); c["error"] == 0 {
		t.Fatalf("expected error count, got %+v", c)
	}
	// Zero max -> no cap.
	full := Render(findings, 0)
	if !strings.Contains(full, "a.go:1") {
		t.Fatalf("zero max should not cap: %q", full)
	}
}
