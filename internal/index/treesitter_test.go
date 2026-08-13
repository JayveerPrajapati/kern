//go:build treesitter

package index

import (
	"testing"
)

func TestTreeSitterAvailableMapping(t *testing.T) {
	cases := []struct {
		lang, rel string
		want      bool
	}{
		{"python", "x.py", true},
		{"dart", "a.dart", true},
		{"typescript", "a.ts", true},
		{"typescript", "a.tsx", true},
		{"shell", "a.sh", true},
		{"go", "a.go", true},
		{"csharp", "a.cs", false}, // no csharp grammar wired
		{"markdown", "a.md", false},
	}
	for _, c := range cases {
		got := TreeSitterAvailable(c.lang)
		if got != c.want {
			t.Errorf("TreeSitterAvailable(%q) = %v; want %v", c.lang, got, c.want)
		}
	}
}

func TestTreeSitterLanguageFor(t *testing.T) {
	cases := []struct {
		lang, rel string
		want      string
	}{
		{"typescript", "a.ts", "typescript"},
		{"typescript", "a.tsx", "tsx"},
		{"shell", "a.sh", "bash"},
		{"python", "a.py", "python"},
	}
	for _, c := range cases {
		got, ok := tsLanguageFor(c.lang, c.rel)
		if !ok || got != c.want {
			t.Errorf("tsLanguageFor(%q,%q) = %q,%v; want %q,true", c.lang, c.rel, got, ok, c.want)
		}
	}
}

func TestTreeSitterExtractPython(t *testing.T) {
	src := `import os

def helper():
    return 1

class Service:
    def run(self):
        helper()
        return os.path.join("a", "b")
`
	syms, calls, _, _, err := tsExtract("svc.py", []byte(src), "python")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	if _, ok := byName["helper"]; !ok {
		t.Errorf("expected helper func, got %v", syms)
	}
	svc, ok := byName["Service"]
	if !ok || svc.Kind != "class" {
		t.Errorf("expected Service class, got %+v", svc)
	}
	run, ok := byName["run"]
	if !ok || run.Kind != "method" {
		t.Errorf("expected run method, got %+v", run)
	}
	if run.Receiver != "Service" {
		t.Errorf("run receiver = %q; want Service", run.Receiver)
	}
	found := false
	for _, c := range calls["Service.run"] {
		if c == "helper" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Service.run -> helper call edge, got %v", calls["Service.run"])
	}
}

func TestTreeSitterExtractShell(t *testing.T) {
	src := `#!/bin/bash

deploy() {
  echo "deploying"
}

backup() {
  deploy
}
`
	syms, calls, _, _, err := tsExtract("ci.sh", []byte(src), "shell")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	if _, ok := byName["deploy"]; !ok {
		t.Errorf("expected deploy func, got %v", syms)
	}
	found := false
	for _, c := range calls["backup"] {
		if c == "deploy" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected backup -> deploy call edge, got %v", calls["backup"])
	}
}

func TestTreeSitterExtractTSX(t *testing.T) {
	src := `import React from "react";

interface Props {
  title: string;
}

export function Header() {
  return <h1>{props.title}</h1>;
}
`
	syms, _, _, _, err := tsExtract("Header.tsx", []byte(src), "typescript")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range syms {
		if s.Name == "Header" && s.Kind == "func" {
			found = true
		}
		if s.Name == "Props" && s.Kind == "interface" {
			// interface recognized too
		}
	}
	if !found {
		t.Errorf("expected Header func from tsx grammar, got %v", syms)
	}
}

func TestTreeSitterExtractRust(t *testing.T) {
	src := `struct Config {
    name: String,
}

impl Config {
    fn load(&self) -> u32 {
        1
    }
}

fn main() {
    let c = Config { name: String::new() };
    c.load();
}
`
	syms, calls, _, _, err := tsExtract("main.rs", []byte(src), "rust")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	if _, ok := byName["Config"]; !ok {
		t.Errorf("expected Config struct, got %v", syms)
	}
	load, ok := byName["load"]
	if !ok || load.Kind != "method" {
		t.Errorf("expected load method, got %+v", load)
	}
	if load.Receiver != "Config" {
		t.Errorf("load receiver = %q; want Config", load.Receiver)
	}
	if len(calls["main"]) == 0 {
		t.Errorf("expected main to have calls, got %v", calls)
	}
}

func TestTreeSitterExtractDart(t *testing.T) {
	src := `class Cat extends Animal implements Pet {
  String name = "cat";

  String meow(String who) {
    return greet(who) + name;
  }

  String get label => name;

  set label(String v) {
    name = v;
  }
}

void main() {
  final c = Cat();
  c.meow("x");
  greet("y");
}

String greet(String who) => "hi " + who;
`
	syms, calls, inherits, _, err := tsExtract("main.dart", []byte(src), "dart")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	cat, ok := byName["Cat"]
	if !ok || cat.Kind != "class" {
		t.Errorf("expected Cat class, got %+v", cat)
	}
	meow, ok := byName["meow"]
	if !ok || meow.Kind != "method" || meow.Receiver != "Cat" {
		t.Errorf("expected Cat.meow method, got %+v", meow)
	}
	if _, ok := byName["greet"]; !ok {
		t.Errorf("expected greet func, got %v", syms)
	}
	bases := inherits["Cat"]
	if len(bases) != 2 {
		t.Fatalf("expected 2 bases for Cat, got %v", bases)
	}
	hasExt, hasImp := false, false
	for _, b := range bases {
		if b == "extends:Animal" {
			hasExt = true
		}
		if b == "implements:Pet" {
			hasImp = true
		}
	}
	if !hasExt || !hasImp {
		t.Errorf("expected extends:Animal and implements:Pet, got %v", bases)
	}
	if len(calls["main"]) == 0 {
		t.Errorf("expected main to have calls, got %v", calls)
	}
	gotCall := false
	for _, c := range calls["main"] {
		if c == "meow" || c == "greet" || c == "Cat" {
			gotCall = true
		}
	}
	if !gotCall {
		t.Errorf("expected main to call meow/greet/Cat, got %v", calls["main"])
	}
}
