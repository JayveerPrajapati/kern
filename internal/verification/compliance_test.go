package verification

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---- License classifier unit tests ----

const mitLicenseText = `MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.`

const apacheLicenseText = `Apache License
Version 2.0, January 2004
http://www.apache.org/licenses/

TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0`

const bsd3LicenseText = `Copyright (c) 2023, Example Corp. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its
   contributors may be used to endorse or promote products derived from
   this software without specific prior written permission.`

const gpl3LicenseText = `GNU GENERAL PUBLIC LICENSE
Version 3, 29 June 2007

Copyright (C) 2007 Free Software Foundation, Inc. <https://fsf.org/>
Everyone is permitted to copy and distribute verbatim copies of this license
document, but changing it is not allowed.`

const unknownLicenseText = `Proprietary software license. All rights reserved.
Do not redistribute, copy, or modify without written permission from Example Corp.`

// TestClassifyLicense drives the high-precision classifier over fixture
// license texts: canonical signatures classify correctly and a non-license
// text is never guessed.
func TestClassifyLicense(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"MIT", mitLicenseText, "MIT"},
		{"Apache-2.0", apacheLicenseText, "Apache-2.0"},
		{"BSD-3-Clause", bsd3LicenseText, "BSD-3-Clause"},
		{"GPL-3.0", gpl3LicenseText, "GPL-3.0"},
		{"unknown", unknownLicenseText, "unknown"},
		{"empty", "", "unknown"},
	}
	for _, tc := range cases {
		if got := classifyLicense(tc.text); got != tc.want {
			t.Errorf("classifyLicense(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestClassifyLicenseHighPrecision asserts near-misses do NOT classify: an
// MIT text missing its core grant sentence, or a BSD text missing the
// redistribution clauses, must stay "unknown" (false-positive-averse).
func TestClassifyLicenseHighPrecision(t *testing.T) {
	truncatedMIT := "Permission is hereby granted, free of charge, to any person obtaining\ncopies of this work. This text stops before the full MIT grant sentence completes."
	// Missing the "in source and binary forms" phrase entirely.
	looseBSD := "Redistributions of source code must retain the above copyright notice."
	// "General Public License" without a version signature.
	vagueGPL := "This project is covered by the GNU General Public License."
	for name, text := range map[string]string{
		"truncated MIT": truncatedMIT,
		"loose BSD":     looseBSD,
		"vague GPL":     vagueGPL,
	} {
		if got := classifyLicense(text); got != "unknown" {
			t.Errorf("classifyLicense(%s) = %q, want unknown (high precision)", name, got)
		}
	}
}

// ---- Secrets regex tests ----

// TestSecretPatternsPositive asserts each high-precision pattern matches its
// canonical secret shape.
func TestSecretPatternsPositive(t *testing.T) {
	longGHP := "ghp_" + strings.Repeat("A", 36)
	cases := []struct {
		kind string
		in   string
	}{
		{"aws-access-key", "AKIAIOSFODNN7EXAMPLE"},
		{"github-pat", longGHP},
		{"github-pat-fine-grained", "github_pat_" + strings.Repeat("B", 30)},
		{"slack-token", strings.Join([]string{"xoxb", "123456789012", "123456789012", "ABCDEFGHIJKLMNOPQRSTUVWX"}, "-")},
		{"private-key", "-----BEGIN RSA PRIVATE KEY-----"},
		{"private-key", "-----BEGIN PRIVATE KEY-----"},
		{"private-key", "-----BEGIN OPENSSH PRIVATE KEY-----"},
	}
	for _, tc := range cases {
		matched := false
		for _, p := range secretPatterns {
			if p.re.MatchString(tc.in) {
				matched = true
				if p.kind != tc.kind {
					t.Errorf("%q matched kind %q, want %q", tc.in, p.kind, tc.kind)
				}
			}
		}
		if !matched {
			t.Errorf("%q matched no pattern, want %q", tc.in, tc.kind)
		}
	}
}

// TestSecretPatternsNegative asserts plausible-but-not-secret strings never
// match (the scanner is high-precision by design).
func TestSecretPatternsNegative(t *testing.T) {
	benign := []string{
		"AKIA1234",                             // too short after the prefix
		"AKIA" + strings.Repeat("X", 15),       // one char short
		"ghp_",                                 // truncated token
		"ghp_" + strings.Repeat("A", 35),       // one char short
		"github_pat_short",                     // fine-grained token too short
		"xoxo-no-dash-token",                   // not a slack token shape
		"xoxb-",                                // truncated slack token
		"-----BEGIN PUBLIC KEY-----",           // public keys are not secrets
		"the private key is stored in vault",   // prose, not a PEM block
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9", // generic JWT — too noisy, skipped
	}
	for _, in := range benign {
		for _, p := range secretPatterns {
			if p.re.MatchString(in) {
				t.Errorf("false positive: %q matched %q", in, p.kind)
			}
		}
	}
}

// TestMaskSecret asserts the masked snippet shows first 4 + "…" + last 4 and
// never the raw value.
func TestMaskSecret(t *testing.T) {
	s := "AKIAIOSFODNN7EXAMPLE"
	m := maskSecret(s)
	if strings.Contains(m, s) {
		t.Errorf("masked snippet leaks the raw secret: %q", m)
	}
	if m != "AKIA…MPLE" {
		t.Errorf("maskSecret(%q) = %q, want %q", s, m, "AKIA…MPLE")
	}
	if got := maskSecret("short"); got != "****" {
		t.Errorf("maskSecret(short) = %q, want ****", got)
	}
}

// ---- govulncheck JSON parser tests ----

const govulncheckEmpty = `{"Version":"v1.1.4","Vulnerabilities":null,"Config":{"DB":{"URI":"https://vuln.go.dev"},"Env":{}}}`

const govulncheckOneVuln = `{
  "Version": "v1.1.4",
  "Vulnerabilities": [
    {
      "ID": "GO-2023-1234",
      "Details": "A maliciously crafted input can cause an out-of-bounds read in the parser, leading to a panic or memory disclosure.",
      "Aliases": ["CVE-2023-1234"],
      "OSV": {
        "id": "GO-2023-1234",
        "affected": [
          {
            "package": {"name": "example.com/fancy", "ecosystem": "Go"},
            "ranges": [
              {"type": "SEMVER", "events": [{"introduced": "0"}, {"fixed": "1.2.3"}]}
            ]
          }
        ]
      }
    }
  ],
  "Config": {"DB": {"URI": "https://vuln.go.dev"}}
}`

// TestParseGovulncheckJSON covers the empty-scan and one-vuln shapes of the
// govulncheck -json output.
func TestParseGovulncheckJSON(t *testing.T) {
	empty, err := parseGovulncheckJSON([]byte(govulncheckEmpty))
	if err != nil {
		t.Fatalf("empty blob: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty blob parsed %d vulns, want 0", len(empty))
	}

	one, err := parseGovulncheckJSON([]byte(govulncheckOneVuln))
	if err != nil {
		t.Fatalf("one-vuln blob: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("one-vuln blob parsed %d vulns, want 1", len(one))
	}
	v := one[0]
	if v.ID != "GO-2023-1234" {
		t.Errorf("ID = %q, want GO-2023-1234", v.ID)
	}
	if v.Module != "example.com/fancy" {
		t.Errorf("Module = %q, want example.com/fancy", v.Module)
	}
	if v.Introduced != "0" {
		t.Errorf("Introduced = %q, want 0", v.Introduced)
	}
	if v.Fixed != "1.2.3" {
		t.Errorf("Fixed = %q, want 1.2.3", v.Fixed)
	}
	if len(v.Summary) > 200 {
		t.Errorf("summary not truncated to ~200 chars: %d", len(v.Summary))
	}
}

// TestParseGovulncheckJSONError asserts a fetch-failure shape (top-level
// Error) is surfaced as an error so the check reports SKIPPED, never a
// fabricated clean scan.
func TestParseGovulncheckJSONError(t *testing.T) {
	_, err := parseGovulncheckJSON([]byte(`{"Error":"failed to fetch the vulnerability database: dial tcp: lookup vuln.go.dev: no such host"}`))
	if err == nil {
		t.Fatal("fetch-failure blob must error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to fetch") {
		t.Errorf("error = %q, want fetch-failure mention", err.Error())
	}
}

// ---- Engine-level integration tests ----

// TestVerifyComplianceOptIn asserts the compliance checks run ONLY when
// requested: requesting one adds exactly that check (nil everywhere else),
// and a run that does not request them leaves them nil (defaults untouched).
func TestVerifyComplianceOptIn(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module compliancefixture\n\ngo 1.20\n",
	})

	t.Setenv("KERN_GOVULNCHECK", filepath.Join(t.TempDir(), "definitely-missing-govulncheck"))

	licenseOnly := NewEngine(dir).Verify([]string{"license"})
	if licenseOnly.License == nil {
		t.Fatal("license-only run: License == nil")
	}
	if licenseOnly.CVE != nil || licenseOnly.Secrets != nil ||
		licenseOnly.Build != nil || licenseOnly.UnitTests != nil || licenseOnly.Security != nil {
		t.Error("license-only run leaked other checks (CVE/Secrets/Build/UnitTests/Security must be nil)")
	}

	trio := NewEngine(dir).Verify([]string{"cve", "license", "secrets"})
	if trio.CVE == nil || trio.License == nil || trio.Secrets == nil {
		t.Fatal("trio run must populate CVE, License and Secrets")
	}
	if trio.Build != nil || trio.UnitTests != nil || trio.Security != nil || trio.Architecture != nil || trio.Dependency != nil {
		t.Error("trio run must NOT drag in the default checks (build/test/security/architecture/dependency)")
	}

	securityOnly := NewEngine(dir).Verify([]string{"security"})
	if securityOnly.CVE != nil || securityOnly.License != nil || securityOnly.Secrets != nil {
		t.Error("run without compliance requests must leave CVE/License/Secrets nil (default behavior untouched)")
	}
}

// TestVerifyCVESkipped asserts the SKIPPED path: govulncheck absent → the
// check reports SKIPPED with the install hint and the verdict is never FAIL.
func TestVerifyCVESkipped(t *testing.T) {
	dir := verifyFixture(t)
	missing := filepath.Join(t.TempDir(), "no-govulncheck-here")
	t.Setenv("KERN_GOVULNCHECK", missing)

	res := NewEngine(dir).VerifyCVE()
	if res == nil {
		t.Fatal("nil CVE result")
	}
	if res.Status != StatusSkipped {
		t.Fatalf("Status = %q, want %q", res.Status, StatusSkipped)
	}
	if !strings.Contains(res.Detail, "not installed") {
		t.Errorf("Detail = %q, want the install hint", res.Detail)
	}
	if !res.OK {
		t.Error("SKIPPED check must keep OK=true (never a hard failure)")
	}

	engine := NewEngine(dir).Verify([]string{"cve"})
	if engine.CVE == nil {
		t.Fatal("engine run: CVE == nil")
	}
	if engine.CVE.Status != StatusSkipped {
		t.Fatalf("engine run: CVE.Status = %q, want SKIPPED", engine.CVE.Status)
	}
	if engine.Verdict == VerdictFail {
		t.Errorf("verdict = %q, must not FAIL when the scanner is absent", engine.Verdict)
	}
}

// TestVerifyLicenseEngine drives the full license check over a go.mod +
// vendor/ fixture: main-module root LICENSE, vendored module licenses, and
// unknown-license WARN findings.
func TestVerifyLicenseEngine(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":                               "module example.com/licfixture\n\ngo 1.20\n\nrequire (\n\texample.com/mitdep v1.0.0\n\texample.com/apachedep v1.0.0\n\texample.com/nolicdep v1.0.0\n)\n",
		"LICENSE":                              mitLicenseText,
		"vendor/modules.txt":                   "# example.com/mitdep v1.0.0\n## explicit\n# example.com/apachedep v1.0.0\n## explicit\n# example.com/nolicdep v1.0.0\n## explicit\n",
		"vendor/example.com/mitdep/LICENSE":    mitLicenseText,
		"vendor/example.com/apachedep/LICENSE": apacheLicenseText,
	})

	res := NewEngine(dir).VerifyLicense()
	if res == nil {
		t.Fatal("nil license result")
	}
	if res.Skipped != "" {
		t.Fatalf("Skipped = %q, want empty (go.mod + vendor present)", res.Skipped)
	}
	byModule := map[string]string{}
	for _, m := range res.Modules {
		byModule[m.Module] = m.License
	}
	if got := byModule["example.com/licfixture"]; got != "MIT" {
		t.Errorf("main module license = %q, want MIT", got)
	}
	if got := byModule["example.com/mitdep"]; got != "MIT" {
		t.Errorf("mitdep license = %q, want MIT", got)
	}
	if got := byModule["example.com/apachedep"]; got != "Apache-2.0" {
		t.Errorf("apachedep license = %q, want Apache-2.0", got)
	}
	if got := byModule["example.com/nolicdep"]; got != "unknown" {
		t.Errorf("nolicdep license = %q, want unknown", got)
	}
	foundUnknown := false
	for _, f := range res.Findings {
		if strings.Contains(f, "unknown license: example.com/nolicdep") {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Errorf("findings = %v, want an unknown-license WARN for nolicdep", res.Findings)
	}
	if !res.OK {
		t.Error("license check must keep OK=true (advisory WARN, never fail)")
	}
}

// TestVerifyLicenseSkipped asserts the honest SKIPPED when neither go.mod nor
// vendor/ exists.
func TestVerifyLicenseSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"README.md": "no go module here"})
	res := NewEngine(dir).VerifyLicense()
	if res == nil {
		t.Fatal("nil license result")
	}
	if res.Skipped != "no vendor/ or go.mod found" {
		t.Errorf("Skipped = %q, want %q", res.Skipped, "no vendor/ or go.mod found")
	}
	if len(res.Modules) != 0 {
		t.Errorf("Modules = %v, want none when skipped", res.Modules)
	}
}

// ---- Secrets engine test ----

// gitFixture creates a tiny git repo with one committed secret and returns
// its root. The commit history is scanned by VerifySecrets.
func gitFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":     "module secretfixture\n\ngo 1.20\n",
		"config.txt": "endpoint=prod\ntoken=AKIAIOSFODNN7EXAMPLE\n",
	})
	for _, args := range [][]string{
		{"init"},
		{"add", "."},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "add config with secret"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// TestVerifySecretsEngine scans a fixture git history and asserts the finding
// carries commit/file/line and a MASKED snippet (never the raw secret).
func TestVerifySecretsEngine(t *testing.T) {
	dir := gitFixture(t)
	res := NewEngine(dir).VerifySecrets()
	if res == nil {
		t.Fatal("nil secrets result")
	}
	if res.Status == StatusSkipped {
		t.Fatalf("secrets scan skipped: %s", res.Detail)
	}
	if res.Count != 1 {
		t.Fatalf("Count = %d, want 1 (one committed AKIA key); findings: %+v", res.Count, res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "aws-access-key" {
		t.Errorf("Kind = %q, want aws-access-key", f.Kind)
	}
	if f.Commit == "" {
		t.Error("finding must carry the short commit hash")
	}
	if f.File != "config.txt" {
		t.Errorf("File = %q, want config.txt", f.File)
	}
	if f.Line != 2 {
		t.Errorf("Line = %d, want 2", f.Line)
	}
	if f.Snippet == "AKIAIOSFODNN7EXAMPLE" || strings.Contains(f.Snippet, "AKIAIOSFODNN7") {
		t.Errorf("snippet leaks the raw secret: %q", f.Snippet)
	}
	if f.Snippet != "AKIA…MPLE" {
		t.Errorf("Snippet = %q, want masked AKIA…MPLE", f.Snippet)
	}
	if !res.OK {
		t.Error("secrets check must keep OK=true (advisory findings, never fail)")
	}
	if res.Detail == "" {
		t.Error("Detail should carry the scan confirmation")
	}
}

// TestVerifySecretsSkipped asserts a non-git root reports SKIPPED, not a
// hard failure.
func TestVerifySecretsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"README.md": "not a git repo"})
	res := NewEngine(dir).VerifySecrets()
	if res == nil {
		t.Fatal("nil secrets result")
	}
	if res.Status != StatusSkipped {
		t.Fatalf("Status = %q, want %q", res.Status, StatusSkipped)
	}
	if !res.OK {
		t.Error("SKIPPED secrets check must keep OK=true")
	}
}

// ---- F7: SKIPPED verdict semantics + license classifier fallback ----

// TestComplianceSkippedTrioVerdictNotFail pins F7 end-to-end: when the
// compliance checks are SKIPPED (missing govulncheck binary, no manifest,
// not a git repo), the run's verdict is SKIPPED — never FAIL. A missing tool
// is an operational gap, not a verification failure.
func TestComplianceSkippedTrioVerdictNotFail(t *testing.T) {
	dir := verifyFixture(t) // go.mod present; NOT a git repo
	t.Setenv("KERN_GOVULNCHECK", filepath.Join(t.TempDir(), "no-govulncheck-here"))
	res := NewEngine(dir).Verify([]string{"cve", "license", "secrets"})
	if res.CVE == nil || res.License == nil || res.Secrets == nil {
		t.Fatal("trio run must populate CVE, License and Secrets")
	}
	if res.CVE.Status != StatusSkipped {
		t.Errorf("CVE.Status = %q, want %q (missing binary must skip)", res.CVE.Status, StatusSkipped)
	}
	if res.Secrets.Status != StatusSkipped {
		t.Errorf("Secrets.Status = %q, want %q (non-git root must skip)", res.Secrets.Status, StatusSkipped)
	}
	if res.Verdict == VerdictFail {
		t.Errorf("skipped compliance checks must never FAIL; verdict=%q summary=%q", res.Verdict, res.Summary)
	}
	if res.Verdict != VerdictSkipped {
		// license may add WARN findings, but the skipped cve+secrets must
		// keep the run at SKIPPED (checked before warn in verdictOf).
		t.Errorf("verdict = %q, want %q when compliance checks are skipped", res.Verdict, VerdictSkipped)
	}
}

// TestKnownLicenseFallback pins the F7 classifier fallback: famous module
// paths resolve deterministically without a local LICENSE file; anything
// else stays "unknown" rather than guessed.
func TestKnownLicenseFallback(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"github.com/stretchr/testify", "MIT"},
		{"github.com/stretchr/testify/assert", "MIT"},
		{"golang.org/x/sys", "BSD-3-Clause"},
		{"google.golang.org/grpc", "Apache-2.0"},
		{"example.com/some/module", ""},
		{"github.com/stretchr/other-thing", ""},
	}
	for _, tc := range cases {
		if got := knownLicense(tc.path); got != tc.want {
			t.Errorf("knownLicense(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestVerifyLicenseKnownModuleFallback drives the engine-level fallback: a
// go.mod requiring a famous module with no vendor/ tree must classify that
// module from the known-license map instead of flagging it "unknown".
func TestVerifyLicenseKnownModuleFallback(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module licfixture\n\ngo 1.20\n\nrequire github.com/stretchr/testify v1.9.0\n",
	})
	res := NewEngine(dir).VerifyLicense()
	if res == nil {
		t.Fatal("nil license result")
	}
	if res.Skipped != "" {
		t.Fatalf("license check skipped unexpectedly: %s", res.Skipped)
	}
	found := false
	for _, m := range res.Modules {
		if m.Module == "github.com/stretchr/testify" {
			found = true
			if m.License != "MIT" {
				t.Errorf("testify license = %q, want MIT (known-license fallback)", m.License)
			}
		}
	}
	if !found {
		t.Error("testify not present in license modules")
	}
	for _, fd := range res.Findings {
		if strings.Contains(fd, "github.com/stretchr/testify") {
			t.Errorf("known module must not be flagged unknown: %s", fd)
		}
	}
	if !res.OK {
		t.Error("license check must keep OK=true (advisory)")
	}
}
