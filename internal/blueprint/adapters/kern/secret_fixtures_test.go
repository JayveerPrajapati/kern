package kern

import (
	"os"
	"path/filepath"
	"testing"
)

// SecretFixtureResult holds a materialized secret-fixture directory.
type SecretFixtureResult struct {
	Dir string // absolute path to the directory containing the files
}

// writeSecretFile writes a file into the fixture dir.
func writeSecretFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

// newSecretDir creates a fresh temp directory for a secret fixture.
func newSecretDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// SecretsClean materializes a directory with no secrets; kern sec must find
// nothing in it.
func SecretsClean(t *testing.T) SecretFixtureResult {
	t.Helper()
	dir := newSecretDir(t)
	writeSecretFile(t, dir, "clean.go", "package main\n\nfunc main() { println(\"hello\") }\n")
	writeSecretFile(t, dir, "config.yaml", "server:\n  port: 8080\n  host: localhost\n")
	return SecretFixtureResult{Dir: dir}
}

// SecretsAPIKey materializes a directory containing a hardcoded AWS access
// key; kern sec must flag it with a message containing "AWS".
func SecretsAPIKey(t *testing.T) SecretFixtureResult {
	t.Helper()
	dir := newSecretDir(t)
	writeSecretFile(t, dir, "aws.go", "package main\n\nconst AWSAccessKey = \"AKIAIOSFODNN7EXAMPLE\"\n")
	return SecretFixtureResult{Dir: dir}
}

// SecretsPassword materializes a directory containing a hardcoded password
// assignment; kern sec must flag it with a message containing "PASSWORD".
// Note: kern's PASSWORD regex requires "=" (not ":="), so we use `const`.
func SecretsPassword(t *testing.T) SecretFixtureResult {
	t.Helper()
	dir := newSecretDir(t)
	writeSecretFile(t, dir, "auth.go", "package main\n\nconst password = \"supersecret123\"\n")
	return SecretFixtureResult{Dir: dir}
}

// SecretsPrivateKey materializes a directory containing a PEM private key
// embedded in a .go source file; kern sec must flag it with a message
// containing "PRIVATE_KEY". Note: kern sec only scans source-code extensions
// (.go, .js, .ts, .py, .rb, .java), NOT .pem files, so the key must be in a
// source file to be detected.
func SecretsPrivateKey(t *testing.T) SecretFixtureResult {
	t.Helper()
	dir := newSecretDir(t)
	writeSecretFile(t, dir, "key.go", "package main\n\nvar key = `-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA1234567890abcdefghijklmnopqrstuvwxyz\n-----END RSA PRIVATE KEY-----`\n")
	return SecretFixtureResult{Dir: dir}
}

// SecretsTokenJSON materializes a directory containing a GitHub token in a
// JSON file; kern sec must flag it with a message containing "GITHUB".
func SecretsTokenJSON(t *testing.T) SecretFixtureResult {
	t.Helper()
	dir := newSecretDir(t)
	writeSecretFile(t, dir, "config.json", `{
  "api_token": "ghp_1234567890abcdefghijklmnopqrstuvwxyz1234567890",
  "name": "myapp"
}
`)
	return SecretFixtureResult{Dir: dir}
}

// SecretsTokenYAML materializes a directory containing a password in a YAML
// file; kern sec must flag it with a message containing "PASSWORD".
func SecretsTokenYAML(t *testing.T) SecretFixtureResult {
	t.Helper()
	dir := newSecretDir(t)
	writeSecretFile(t, dir, "secrets.yaml", `database:
  password: mySecretDbPassword456
  host: localhost
`)
	return SecretFixtureResult{Dir: dir}
}

// SecretsFalsePositive materializes a directory containing a known-fake AWS
// key inside a testdata/ directory (a non-test .go file). kern sec DOES flag
// this file (it only skips *_test.go files natively, not testdata/ dirs), so
// the G3 test can verify that Blueprint's DefaultAllowlist suppresses the
// finding for testdata/ paths.
func SecretsFalsePositive(t *testing.T) SecretFixtureResult {
	t.Helper()
	dir := newSecretDir(t)
	writeSecretFile(t, dir, "testdata/fixture.go", `package testdata

// Test fixture: this is a known-fake key used only in tests.
// DO NOT use this key in production.
const TestKey = "AKIAIOSFODNN7EXAMPLE"
`)
	return SecretFixtureResult{Dir: dir}
}
