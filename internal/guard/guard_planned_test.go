package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

const srcLib = `package lib

func Public() string {
	return inner()
}

func inner() string {
	return "x"
}

func Deep() {
	Public()
}

func UntestedHot() {}
`

const srcClient = `package client

import "lib"

func Caller() {
	lib.Public()
	lib.UntestedHot()
}

func LocalOnly() {}
`

func TestGuardRejectsForbiddenEdge(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "client", To: "lib", Action: "forbid"}}}
	violations := CheckBoundaries(ix, b, []string{"client/client.go"})
	// Caller calls lib.Public and lib.UntestedHot, both crossing the same
	// client->lib boundary, so they collapse into one evidence-carrying
	// violation for the file pair.
	if len(violations) != 1 {
		t.Fatalf("expected 1 collapsed violation, got %+v", violations)
	}
	v := violations[0]
	if v.CallerFile != "client/client.go" || v.CalleeFile != "lib/lib.go" {
		t.Errorf("wrong edge evidence: %+v", v)
	}
	if v.Symbol != "Public" {
		t.Errorf("expected Public as the offending symbol, got %s", v.Symbol)
	}
	if !strings.Contains(RenderViolations(violations), "REJECT") {
		t.Error("render should say REJECT")
	}
}

func TestGuardAllowOverridesForbid(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{
		{From: "client", To: "lib", Action: "forbid"},
		{From: "client", To: "lib", Action: "allow"},
	}}
	if v := CheckBoundaries(ix, b, []string{"client/client.go"}); len(v) != 0 {
		t.Errorf("allow rule must override forbid, got %+v", v)
	}
}

func TestGuardImportLevelAndPass(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"client/imports.go": `package client

import "lib"

func Touch() {}`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// No rules -> PASS.
	if v := CheckBoundaries(ix, &Boundaries{}, []string{"client/client.go"}); len(v) != 0 {
		t.Errorf("no rules must pass, got %+v", v)
	}
	// A file that only imports the forbidden package (no call) is still caught.
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "client", To: "lib", Action: "forbid"}}}
	v := CheckBoundaries(ix, b, []string{"client/imports.go"})
	if len(v) != 1 {
		t.Fatalf("import-level crossing should be flagged, got %+v", v)
	}
	if v[0].CallerFile != "client/imports.go" {
		t.Errorf("wrong caller file: %+v", v[0])
	}
}

// TestGuardImportAttributionCleanChangedFile: the import-level boundary check
// must attribute imports per changed file, not per package. A clean file in a
// package where a sibling file imports a forbidden package is NOT a violation
// (regression: the old check used package-aggregated imports and falsely
// flagged every changed file in the directory).
func TestGuardImportAttributionCleanChangedFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"db/db.go": `package db

func Get() {}
`,
		"web/web.go": `package web

import "example.com/repo/db"

func UseDB() {}
`,
		"web/clean.go": `package web

func Clean() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "web", To: "db", Action: "forbid"}}}
	violations := CheckBoundaries(ix, b, []string{"web/clean.go"})
	if len(violations) != 0 {
		t.Fatalf("clean changed file must not inherit a sibling's forbidden import, got %+v", violations)
	}
}

// TestGuardImportAttributionChangedImporter: when the changed file itself
// imports the forbidden package, exactly one violation is reported with that
// file as the caller.
func TestGuardImportAttributionChangedImporter(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"db/db.go": `package db

func Get() {}
`,
		"web/clean.go": `package web

func Clean() {}
`,
		"web/bad.go": `package web

import "example.com/repo/db"

func UseDB() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "web", To: "db", Action: "forbid"}}}
	violations := CheckBoundaries(ix, b, []string{"web/bad.go"})
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %+v", violations)
	}
	v := violations[0]
	if v.CallerFile != "web/bad.go" || v.CalleeFile != "db/" {
		t.Errorf("wrong edge evidence: %+v", v)
	}
}

func TestGuardLoadAndInit(t *testing.T) {
	dir := writeTree(t, map[string]string{})
	if err := InitBoundaries(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".kern", "boundaries.json")); err != nil {
		t.Fatalf("init should write boundaries.json: %v", err)
	}
	b, err := LoadBoundaries(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Rules) == 0 {
		t.Error("starter template should contain a rule")
	}
}

// TestGuardCatchesJavaImportViolation: Java import edges are now extracted
// by the indexer, so guard's import-level check must catch a forbidden
// dotted import even though no call edge exists.
func TestGuardCatchesJavaImportViolation(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"src/main/java/com/example/config/AppConfig.java": `package com.example.config;
import com.example.commons.vault.IVaultService;
public class AppConfig {
    // import only — no call into vault
}
`,
		"src/main/java/com/example/commons/vault/IVaultService.java": `package com.example.commons.vault;
public interface IVaultService {
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "config", To: "vault", Action: "forbid"}}}
	violations := CheckBoundaries(ix, b, []string{"src/main/java/com/example/config/AppConfig.java"})
	if len(violations) == 0 {
		t.Fatal("expected a violation for config -> vault import, got none")
	}
	v := violations[0]
	if v.CallerFile != "src/main/java/com/example/config/AppConfig.java" {
		t.Errorf("wrong caller file: %s", v.CallerFile)
	}
	if v.RuleFrom != "config" || v.RuleTo != "vault" {
		t.Errorf("wrong rule: %s -> %s", v.RuleFrom, v.RuleTo)
	}
}

// TestGuardJavaAllowOverridesForbid: an explicit allow rule must win over a
// forbid rule for Java dotted imports too.
func TestGuardJavaAllowOverridesForbid(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"src/main/java/com/example/config/AppConfig.java": `package com.example.config;
import com.example.commons.vault.IVaultService;
public class AppConfig {
}
`,
		"src/main/java/com/example/commons/vault/IVaultService.java": `package com.example.commons.vault;
public interface IVaultService {
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{
		{From: "config", To: "vault", Action: "forbid"},
		{From: "config", To: "vault", Action: "allow"},
	}}
	if v := CheckBoundaries(ix, b, []string{"src/main/java/com/example/config/AppConfig.java"}); len(v) != 0 {
		t.Errorf("allow rule must override forbid for java imports, got %+v", v)
	}
}

// TestGuardImportMatchesDotted: the dotted-path matcher itself.

// TestBoundariesParsesPureFlag: "pure": true in .kern/boundaries.json opts
// into @pure assertions; absent, the flag defaults to false and nothing
// changes.
func TestBoundariesParsesPureFlag(t *testing.T) {
	dir := writeTree(t, map[string]string{
		".kern/boundaries.json": `{"pure": true}`,
	})
	b, err := LoadBoundaries(dir)
	if err != nil {
		t.Fatalf("boundaries file with pure flag must load: %v", err)
	}
	if b == nil || !b.Pure {
		t.Fatalf("expected Pure=true, got %+v", b)
	}
	// Default: no "pure" field -> false.
	dir2 := writeTree(t, map[string]string{
		".kern/boundaries.json": `{"rules": []}`,
	})
	b2, err := LoadBoundaries(dir2)
	if err != nil {
		t.Fatalf("default boundaries file must load: %v", err)
	}
	if b2 == nil || b2.Pure {
		t.Fatalf("expected Pure=false by default, got %+v", b2)
	}
}
