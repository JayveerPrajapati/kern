package sec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFileChecksumFirstLineNoPanic(t *testing.T) {
	// Regression (e2e 2026-09-13, SliceManagerDeps): a 64-hex checksum on
	// the FIRST line of a file — checksum manifests list the hash first —
	// made isDocumentedChecksum compute src[:-1] and panic with
	// "slice bounds out of range [:-1]".
	src := []byte(strings.Repeat("ab", 32) + "  release.tar.gz\n")
	findings := ScanFile("docs/checksums.txt", src) // must not panic
	for _, f := range findings {
		if f.Rule != "hardcoded-secret" && f.Rule != "" {
			t.Errorf("unexpected rule for first-line checksum: %+v", f)
		}
	}
	// Sanity: an empty file (the other e2e trigger shape) stays clean.
	if got := ScanFile("docs/empty.txt", nil); len(got) != 0 {
		t.Errorf("expected no findings for empty file, got %+v", got)
	}
}

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

func TestScanTreeSkipsMinifiedJS(t *testing.T) {
	dir := t.TempDir()
	// A vendored minified bundle: single line > 2000 chars containing a
	// fake password-like string. Must produce no findings.
	longLine := strings.Repeat("a", 5000) + ` var pwd = "hunter2secret123";`
	write := func(rel, content string) {
		p := dir + "/" + rel
		parts := strings.Split(rel, "/")
		if len(parts) > 1 {
			_ = os.MkdirAll(dir+"/"+strings.Join(parts[:len(parts)-1], "/"), 0o755)
		}
		_ = os.WriteFile(p, []byte(content), 0o644)
	}
	write("static/redoc.standalone.js", longLine)
	// A real source file must still be scanned.
	write("app.go", `package main
func main() {
	_ = "sk-abcdefghijklmnopqrstuvwxyz1234567890"
}
`)
	findings, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.HasSuffix(f.File, ".js") {
			t.Fatalf("minified JS must be skipped, got %+v", f)
		}
	}
	// Ensure the non-JS source file was still scanned.
	found := false
	for _, f := range findings {
		if strings.HasSuffix(f.File, "app.go") && f.Rule == "hardcoded-secret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected finding in app.go, got %+v", findings)
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

// K3 regression: Java test files (*Test.java, *Tests.java, *Spec.java) and
// files under src/test/ directories must be skipped. The old isTestFile only
// matched _test. and .test. patterns, missing the JUnit/Maven convention.
func TestScanTreeSkipsJavaTestFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := dir + "/" + rel
		parts := strings.Split(rel, "/")
		if len(parts) > 1 {
			_ = os.MkdirAll(dir+"/"+strings.Join(parts[:len(parts)-1], "/"), 0o755)
		}
		_ = os.WriteFile(p, []byte(content), 0o644)
	}
	secret := `String k = "sk-abcdefghijklmnopqrstuvwxyz1234567890";`
	// Production file — should be scanned.
	write("src/main/java/com/example/Config.java", secret)
	// Java test files — must be skipped.
	write("src/test/java/com/example/CacheKeyGeneratorTest.java", secret)
	write("src/test/java/com/example/ValidationHelperTests.java", secret)
	write("src/test/java/com/example/FlowSpec.java", secret)
	write("src/test/java/com/example/IntegrationIT.java", secret)
	findings, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.Contains(f.File, "Test") || strings.Contains(f.File, "/test/") {
			t.Fatalf("Java test file must be skipped, got %+v", f)
		}
	}
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding (from Config.java), got %d: %+v", len(findings), findings)
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

// TestInsecureRandomSuppressedForVisualLogic verifies that Math.random /
// rand.Intn used for visual/animation/game effects (fireworks, particles,
// dice) is NOT flagged when no security keyword is nearby. The rule's
// summary is "for security-relevant data" — visual randomness is not.
func TestInsecureRandomSuppressedForVisualLogic(t *testing.T) {
	src := []byte(`<script>
function Firework(x, y) {
    this.spawningTime = opts.fireworkSpawnTime * Math.random() |0;
    this.reachTime = opts.fireworkBaseReachTime + opts.fireworkAddedReachTime * Math.random() |0;
    this.lineWidth = opts.fireworkBaseLineWidth + opts.fireworkAddedLineWidth * Math.random();
    this.circleFinalSize = opts.fireworkCircleBaseSize + opts.fireworkCircleAddedSize * Math.random();
}
</script>`)
	findings := ScanFile("animation.js", src)
	for _, f := range findings {
		if f.Rule == "insecure-random" {
			t.Fatalf("visual Math.random must not be flagged: %+v", f)
		}
	}
}

// TestInsecureRandomFlaggedForSecurityContext verifies that the same
// insecure-random call IS flagged when a security keyword (token, password,
// nonce, session, etc.) is within the context window.
func TestInsecureRandomFlaggedForSecurityContext(t *testing.T) {
	src := []byte(`function generateSessionToken() {
    return Math.random();
}
`)
	findings := ScanFile("auth.js", src)
	found := false
	for _, f := range findings {
		if f.Rule == "insecure-random" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected insecure-random for security-relevant Math.random")
	}
}

// TestEmailSuppressedInPlaceholder verifies that emails in HTML placeholder
// attributes are not flagged as hardcoded secrets — they are UX hints, not
// credentials.
func TestEmailSuppressedInPlaceholder(t *testing.T) {
	src := []byte(`<input type="email" placeholder="xyz@gmail.com" id="email" />`)
	findings := ScanFile("form.html", src)
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" {
			t.Fatalf("placeholder email must not be flagged: %+v", f)
		}
	}
}

// TestEmailSuppressedInCSSComment verifies that emails inside CSS comments
// (/* ... */) are not flagged — they are documentation, not secrets.
func TestEmailSuppressedInCSSComment(t *testing.T) {
	src := []byte(`.candle {
    /* contact me at nathkaran327@gmail.com for help */
    color: orange;
}`)
	findings := ScanFile("style.css", src)
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" {
			t.Fatalf("CSS comment email must not be flagged: %+v", f)
		}
	}
}

// TestEmailSuppressedInHTMLComment verifies that emails inside HTML comments
// (<!-- ... -->) are not flagged.
func TestEmailSuppressedInHTMLComment(t *testing.T) {
	src := []byte(`<!-- admin contact: admin@example.com -->
<div>content</div>`)
	findings := ScanFile("page.html", src)
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" {
			t.Fatalf("HTML comment email must not be flagged: %+v", f)
		}
	}
}

// TestEmailFlaggedInRealCode verifies that a genuinely hardcoded email in
// source code (not a placeholder/comment) is still flagged.
func TestEmailFlaggedInRealCode(t *testing.T) {
	src := []byte(`const adminEmail = "admin@company.com";
sendAlert(adminEmail);`)
	findings := ScanFile("alert.go", src)
	found := false
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Snippet, "@") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected hardcoded email to be flagged in real code")
	}
}

// TestScanTreeRecordsUnreadableFile: an unreadable file must surface as a
// warning finding, not silently vanish from a "clean" scan. A15.
func TestScanTreeRecordsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.go")
	if err := os.WriteFile(path, []byte("package app\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("chmod: %v", err)
	}
	findings, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var found bool
	for _, f := range findings {
		if f.Rule == "unreadable-file" {
			found = true
			if f.Severity != "warning" {
				t.Errorf("severity = %q, want warning", f.Severity)
			}
		}
	}
	if !found {
		t.Error("no unreadable-file finding for a chmod-000 source file")
	}
}

func TestDocFileProseSecretsSuppressed(t *testing.T) {
	src := []byte(`# README

The server binds 128.0.0.1:8090 by default.
Contact kern@example.org for help.
See https://user:pass@example.com/api for the legacy endpoint.
Masking accepts the sk-live-…/sk-test-… dash-form prefixes.
`)
	findings := ScanFile("README.md", src)
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" {
			t.Fatalf("doc prose EMAIL/IP/URL_CRED must be suppressed, got %+v", f)
		}
	}
}

func TestDocFileRealSecretStillCaught(t *testing.T) {
	src := []byte("# Notes\n\nAPI key: sk-abcdefghijklmnopqrstuvwxyz1234567890\n")
	findings := ScanFile("NOTES.md", src)
	found := false
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("real secret in a doc file must still be caught, got %+v", findings)
	}
}

func TestExampleDomainEmailSuppressed(t *testing.T) {
	src := []byte(`git(t, dir, "config", "user.email", "testfixture@example.com")
const contact = "ops@example.org"
`)
	findings := ScanFile("fixture.go", src)
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" {
			t.Fatalf("example-domain email must not be flagged, got %+v", f)
		}
	}
}

func TestRuleIDLiteralSuppressed(t *testing.T) {
	src := []byte(`res.Findings = append(res.Findings, domain.Finding{
	RuleID:      "secret:incumbent-unavailable",
	Severity:    domain.SeverityWarn,
})`)
	findings := ScanFile("check.go", src)
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" {
			t.Fatalf("RuleID literal must not be flagged, got %+v", f)
		}
	}
}

func TestCodeEvalQuotedDescriptionSuppressed(t *testing.T) {
	desc := []byte(`add("py-yaml-load", SeverityError, "yaml.load without an explicit Loader= (unsafe by default)")
return "pickle.loads"
`)
	for _, f := range ScanFile("rules.go", desc) {
		if f.Rule == "code-eval" {
			t.Fatalf("quoted code-eval description must not be flagged, got %+v", f)
		}
	}

	real := []byte("data := yaml.load(userInput)\n")
	found := false
	for _, f := range ScanFile("app.go", real) {
		if f.Rule == "code-eval" {
			found = true
		}
	}
	if !found {
		t.Fatal("unquoted yaml.load call must still be flagged as code-eval")
	}
}

// e2e round 2 (P1): package-lock.json "resolved" registry URLs were flagged
// URL_CRED: the scheme-less DSN regex read https as the username and
// //registry.npmjs.org/ (up to the scoped package's @) as the password.
func TestLockfileRegistryURLSuppressed(t *testing.T) {
	src := []byte(`"resolved": "https://registry.npmjs.org/@babel/code-frame/-/code-frame-7.29.0.tgz",
"integrity": "sha512-abc"
`)
	for _, f := range ScanFile("package-lock.json", src) {
		if f.Rule == "hardcoded-secret" {
			t.Fatalf("registry URL must not be flagged, got %+v", f)
		}
	}
}

// Real scheme-less DSNs must still be flagged after the lockfile fix.
func TestSchemelessDSNStillCaught(t *testing.T) {
	src := []byte("const dsn = postgres:secretpw@db.internal:5432/proc\n")
	var caught bool
	for _, f := range ScanFile("config.go", src) {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "URL_CRED") {
			caught = true
		}
	}
	if !caught {
		t.Fatal("scheme-less DSN with password must still be flagged")
	}
}

// e2e round 2 (P1): browser User-Agent versions (Chrome/124.0.0.0) and
// long decimal chains read as IPv4 addresses.
func TestVersionContextIPSuppressed(t *testing.T) {
	src := []byte("ua := \"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36\"\n" +
		"version := 1.2.3.4.5\n")
	for _, f := range ScanFile("ua.go", src) {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "secret: IP") {
			t.Fatalf("version-context IP must not be flagged, got %+v", f)
		}
	}
}

// QA F1 (2026-09-22): "version": "0.9.9.1" in server.json was flagged as a
// hardcoded-secret IP and failed kern's own CI gate. Version-key values and
// leading-zero dotted quads are build strings, not addresses.
func TestJSONVersionFieldIPSuppressed(t *testing.T) {
	src := []byte("{\n" +
		"  \"name\": \"server\",\n" +
		"  \"version\": \"0.9.9.1\",\n" +
		"  \"address\": \"203.0.113.7\"\n" +
		"}\n")
	var versionFlagged, addressCaught bool
	for _, f := range ScanFile("server.json", src) {
		if f.Rule != "hardcoded-secret" || !strings.Contains(f.Message, "secret: IP") {
			continue
		}
		if strings.Contains(f.Snippet, "0.9.9.1") {
			versionFlagged = true
		}
		if strings.Contains(f.Snippet, "203.0.113.7") {
			addressCaught = true
		}
	}
	if versionFlagged {
		t.Fatal("version-field dotted quad must not be flagged as IP secret")
	}
	if !addressCaught {
		t.Fatal("public IP in an address field must still be flagged")
	}
}

// Version keys in YAML/properties form and bare leading-zero dotted quads
// (no key context) are version strings, not network addresses.
func TestVersionKeyContextFormsSuppressed(t *testing.T) {
	cases := []struct{ rel, line string }{
		{"deploy.yaml", "appVersion: 1.2.3.4"},
		{"app.properties", "build_version=2.10.0.1"},
		{"meta.go", "  ver := \"0.9.9.1\" // no key context, leading-zero octet"},
	}
	for _, c := range cases {
		for _, f := range ScanFile(c.rel, []byte(c.line+"\n")) {
			if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "secret: IP") {
				t.Fatalf("%s: version string must not be flagged, got %+v", c.rel, f)
			}
		}
	}
}

// A public IP in real code must still be flagged.
func TestRealIPStillCaught(t *testing.T) {
	src := []byte("upstream := 8.8.8.8:53\n")
	findings := ScanFile("net.go", src)
	var caught bool
	for _, f := range findings {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "secret: IP") {
			caught = true
		}
	}
	if !caught {
		t.Fatalf("public IP must still be flagged, findings: %+v", findings)
	}
}

// SVG files are graphics: dotted numbers there are path coordinates,
// never network addresses.
func TestSVGPathIPSuppressed(t *testing.T) {
	src := []byte("<svg><path d=\"M12.5.3.4 L1.2.3.4 5.6.7.8\"/></svg>\n")
	for _, f := range ScanFile("icon.svg", src) {
		if f.Rule == "hardcoded-secret" {
			t.Fatalf("SVG path coordinates must not be flagged, got %+v", f)
		}
	}
}

// Google Fonts CSS URLs put a font weight after "@" (family=Inter:wght@300)
// which reads as user:pass@host with an all-digit host - never a real one.
func TestGoogleFontsCSSURLSuppressed(t *testing.T) {
	src := []byte("href=\"https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&display=swap\"\n")
	for _, f := range ScanFile("index.html", src) {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "URL_CRED") {
			t.Fatalf("Google Fonts CSS URL must not be flagged, got %+v", f)
		}
	}
}

// Lockfile deprecation notices cite author emails (isaacs@izs.me in glob's),
// which are npm registry metadata, not credentials.
func TestLockfileAuthorEmailSuppressed(t *testing.T) {
	src := []byte("\"deprecated\": \"Old versions of glob are not supported, and contain widely publicized security vulnerabilities. isaacs@izs.me\"\n")
	for _, f := range ScanFile("package-lock.json", src) {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "EMAIL") {
			t.Fatalf("lockfile author email must not be flagged, got %+v", f)
		}
	}
}

func TestMarkdownTableAndLogsSuppressed(t *testing.T) {
	src := []byte(`# Benchmark Results

| Metric | Value |
|---|---|
| MoE ACTIVE/Token : 77,127,680 | 100 |

## Sample Execution Log
(msvc_delivery) INFO 21:00:39.609 - Early warning to deliver order 'b32ad' to customer_id 'd94a6c43d9f487c1bef659f05c002213'
(msvc_delivery) INFO 21:01:01.662 - Deliverying order 'b32ad' for customer_id 'd94a6c43d9f487c1bef659f05c002213'

Checkout at https://example.com/api?id=4074cca80beef0123456789abcdef0123

## Real secret in doc must still be caught:
sk-proj-abc1234567890abcdef1234567890
`)
	findings := ScanFile("README.md", src)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding for real secret in doc, got %d: %+v", len(findings), findings)
	}
	if findings[0].Rule != "hardcoded-secret" || !strings.Contains(findings[0].Snippet, "sk-proj-") {
		t.Errorf("expected OpenAI key finding, got %+v", findings[0])
	}
}

// TestScanJSONChecksumKeyNotASecret (F-DG5 follow-up): a 64-hex value under
// a JSON key that names it a checksum ("sha256": "<hex>", as in the
// diff-gate tool-schema baseline) is a documented digest, not a hardcoded
// secret — while the same hex under a credential-shaped key still flags.
func TestScanJSONChecksumKeyNotASecret(t *testing.T) {
	baseline := []byte("{\n  \"tools\": [\n    {\n      \"name\": \"kern_search\",\n      \"sha256\": \"87756121650b1999b61eb3f61c859ad9a16df75422a83f43722e57cd6ca76202\"\n    }\n  ]\n}\n")
	fs := ScanFile("tool-schemas.json", baseline)
	if len(fs) != 0 {
		t.Fatalf("sha256-named digest must not flag, got %+v", fs)
	}
	secret := []byte("{\n  \"api_key\": \"87756121650b1999b61eb3f61c859ad9a16df75422a83f43722e57cd6ca76202\"\n}\n")
	fs = ScanFile("config.json", secret)
	if len(fs) == 0 {
		t.Fatal("hex under a credential key must still flag")
	}
}

// e2e (2026-09-25): `kern security .` on kern's own repo flagged 37
// hardcoded-secret HEX findings on internal/setup/plugin_hashes.go — the
// SHA-256 plugin-hash registry. A 64-hex digest in KEY position mapped to a
// boolean (the hash-table shape) is a shipped-artifact allowlist, not a
// credential.
func TestHashTableContextSuppressed(t *testing.T) {
	var b strings.Builder
	b.WriteString("package setup\n\nvar shippedPluginHashes = map[string]bool{\n")
	for i := 0; i < 37; i++ {
		fmt.Fprintf(&b, "\t\"%064x\": true,\n", i)
	}
	b.WriteString("}\n")
	for _, f := range ScanFile("internal/setup/plugin_hashes.go", []byte(b.String())) {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "HEX") {
			t.Fatalf("hash-table entry must not be flagged, got %+v", f)
		}
	}
	// Same shape, but the registry name carries the marker instead of the
	// file name (var shippedPluginHashes in registry.go): still suppressed.
	for _, f := range ScanFile("internal/setup/registry.go", []byte(b.String())) {
		if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "HEX") {
			t.Fatalf("hash-named var entry must not be flagged, got %+v", f)
		}
	}
}

// The suppression must NOT swallow real secrets: a 64-char hex in VALUE
// position — a credential assignment in a normal file, in a file named
// *hashes.go, or as a non-boolean map value — is still flagged.
func TestHashTableContextRealSecretStillFlagged(t *testing.T) {
	hex := "87756121650b1999b61eb3f61c859ad9a16df75422a83f43722e57cd6ca76202"
	cases := []struct{ rel, src string }{
		{"config.go", "const apiKey = \"" + hex + "\"\n"},
		{"internal/setup/plugin_hashes.go", "apiKey := \"" + hex + "\"\n"},
		{"hashes.go", "const secret = \"" + hex + "\"\n"},
		{"hashes.go", "\"" + hex + "\": \"not-a-boolean\"\n"},
		{"hashes.go", "var shippedPluginHashes = map[string]string{\n\t\"" + hex + "\": \"value\",\n}\n"},
	}
	for _, c := range cases {
		var caught bool
		for _, f := range ScanFile(c.rel, []byte(c.src)) {
			if f.Rule == "hardcoded-secret" && strings.Contains(f.Message, "HEX") {
				caught = true
			}
		}
		if !caught {
			t.Errorf("%s: value-position 64-hex must still be flagged for src %q", c.rel, c.src)
		}
	}
}
