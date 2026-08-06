package fw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogCoverage(t *testing.T) {
	if len(Catalog()) < 40 {
		t.Fatalf("catalog looks thin: %d frameworks", len(Catalog()))
	}
	seen := map[string]bool{}
	for _, f := range Catalog() {
		if f.ID == "" || f.Name == "" || f.Lang == "" {
			t.Errorf("framework missing id/name/lang: %+v", f)
		}
		if seen[f.ID] {
			t.Errorf("duplicate framework id %q", f.ID)
		}
		seen[f.ID] = true
		if len(f.Files) == 0 && len(f.Deps) == 0 && len(f.Code) == 0 {
			t.Errorf("framework %s has no signals", f.ID)
		}
	}
}

func TestByID(t *testing.T) {
	if ByID("spring-boot") == nil || ByID("nope") != nil {
		t.Fatal("ByID lookup broken")
	}
}

func TestByLangJava(t *testing.T) {
	got := ByLang("java")
	if len(got) == 0 {
		t.Fatal("no java frameworks")
	}
	for _, f := range got {
		if f.Lang != "java" {
			t.Errorf("ByLang(java) returned %s", f.Lang)
		}
	}
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	abs := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func detectIn(t *testing.T, files map[string]string) []Detected {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		write(t, root, name, content)
	}
	d, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func ids(d []Detected) map[string]bool {
	m := map[string]bool{}
	for _, x := range d {
		m[x.ID] = true
	}
	return m
}

func TestDetectJavaSpringBoot(t *testing.T) {
	d := detectIn(t, map[string]string{
		"pom.xml": `<project><artifactId>spring-boot-starter-web</artifactId></project>`,
		"src/main/java/com/example/App.java": `package com.example;
import org.springframework.boot.*;
@SpringBootApplication
public class App { public static void main(String[] args) {} }`,
	})
	got := ids(d)
	if !got["spring-boot"] {
		t.Errorf("expected spring-boot, got %v", got)
	}
	if !got["spring-mvc"] {
		t.Errorf("expected spring-mvc, got %v", got)
	}
}

func TestDetectPythonFlaskAndDjango(t *testing.T) {
	d := detectIn(t, map[string]string{
		"app.py": `from flask import Flask
app = Flask(__name__)
@app.route("/")
def index(): return "hi"`,
		"manage.py": `#!/usr/bin/env python
from django.core.management import execute_from_command_line`,
	})
	got := ids(d)
	if !got["flask"] || !got["django"] {
		t.Errorf("expected flask and django, got %v", got)
	}
}

func TestDetectNodeExpressNest(t *testing.T) {
	d := detectIn(t, map[string]string{
		"package.json": `{"dependencies": {"express": "^4", "@nestjs/core": "^10"}}`,
		"src/app.ts":   `import { Module } from '@nestjs/common';`,
	})
	got := ids(d)
	if !got["express"] || !got["nestjs"] {
		t.Errorf("expected express and nestjs, got %v", got)
	}
}

func TestDetectGoGinViaDepAndCode(t *testing.T) {
	d := detectIn(t, map[string]string{
		"go.mod":  "module example\nrequire github.com/gin-gonic/gin v1.9.0",
		"main.go": "package main\nfunc main() { r := gin.Default() }",
	})
	got := ids(d)
	if !got["gin"] {
		t.Errorf("expected gin, got %v", got)
	}
}

func TestDetectRubyRailsByFile(t *testing.T) {
	d := detectIn(t, map[string]string{
		"Gemfile":            `gem "rails", "~> 7.0"`,
		"config/routes.rb":   `Rails.application.routes.draw do; end`,
	})
	got := ids(d)
	if !got["rails"] {
		t.Errorf("expected rails, got %v", got)
	}
}

func TestDetectIgnoresVendored(t *testing.T) {
	d := detectIn(t, map[string]string{
		"node_modules/express/index.js": "require('express')",
		"src/index.js":                  "require('express')",
	})
	got := ids(d)
	// express detected only via the real source file — node_modules is skipped.
	if !got["express"] {
		t.Errorf("expected express, got %v", got)
	}
}

func TestRenderEmpty(t *testing.T) {
	if Render(nil) == "" {
		t.Fatal("render should not be empty")
	}
}

func TestRenderDetected(t *testing.T) {
	d := detectIn(t, map[string]string{"manage.py": ""})
	out := Render(d)
	if out == "" || !strings.Contains(out, "Django") {
		t.Errorf("render missing framework: %q", out)
	}
}
