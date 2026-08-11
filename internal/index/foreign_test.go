package index

import (
	"testing"
)

func symsIn(lang string, src string) []Symbol {
	syms, calls, _, _, _ := extractForeign("file."+extFor(lang), []byte(src), lang)
	_ = calls
	return syms
}

func extFor(lang string) string {
	switch lang {
	case "python":
		return "py"
	case "javascript":
		return "js"
	case "typescript":
		return "ts"
	case "rust":
		return "rs"
	case "cpp":
		return "cpp"
	case "java":
		return "java"
	case "ruby":
		return "rb"
	case "shell":
		return "sh"
	}
	return lang
}

func findSym(syms []Symbol, name string) *Symbol {
	for i := range syms {
		if syms[i].FullName() == name {
			return &syms[i]
		}
	}
	return nil
}

const pySrc = `import os

def greet(name):
    return "hi " + name

class User:
    def __init__(self, name):
        self.name = name

    def login(self, token):
        return validate(token)

def main():
    u = User("x")
    return greet(u.name)
`

func TestPythonExtract(t *testing.T) {
	syms, calls, _, _, _ := extractForeign("app.py", []byte(pySrc), "python")
	if s := findSym(syms, "greet"); s == nil || s.Kind != "func" || s.Lang != "python" {
		t.Fatalf("expected func greet, got %+v", s)
	}
	if s := findSym(syms, "User"); s == nil || s.Kind != "class" {
		t.Fatalf("expected class User, got %+v", s)
	}
	if s := findSym(syms, "User.login"); s == nil || s.Kind != "method" || s.Receiver != "User" {
		t.Fatalf("expected method User.login, got %+v", s)
	}
	if s := findSym(syms, "main"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func main, got %+v", s)
	}
	if !contains(calls["User.login"], "validate") {
		t.Fatalf("expected User.login to call validate, got %v", calls["User.login"])
	}
	if !contains(calls["main"], "greet") {
		t.Fatalf("expected main to call greet, got %v", calls["main"])
	}
	if !contains(calls["main"], "User") {
		t.Fatalf("expected main to reference User, got %v", calls["main"])
	}
}

func TestPythonTripleDelimiterIsolation(t *testing.T) {
	// A ''' docstring spanning lines must not be closed by a """ that appears
	// inside it, and code after the docstring must still be analysed.
	src := `'''first line
inner """quote"""
still docstring
'''

def work():
    return helper()

def helper():
    return 1
`
	syms, calls, _, _, _ := extractForeign("app.py", []byte(src), "python")
	if s := findSym(syms, "work"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func work after ''' docstring, got %+v", syms)
	}
	if s := findSym(syms, "helper"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func helper, got %+v", syms)
	}
	if !contains(calls["work"], "helper") {
		t.Fatalf("expected work to call helper, got %v", calls["work"])
	}
}

const jsSrc = `import { x } from "./x.js"

export const MAX = 10

export function add(a, b) {
  return a + b
}

const square = (n) => n * n

export class Store {
  constructor() {
    this.data = {}
  }

  get total() {
    return this.items()
  }

  push(item) {
    return add(this.size, 1)
  }
}

function helper() {
  const s = new Store()
  return square(s.total)
}
`

func TestJavascriptExtract(t *testing.T) {
	syms, calls, _, _, _ := extractForeign("store.js", []byte(jsSrc), "javascript")
	if s := findSym(syms, "MAX"); s == nil || s.Kind != "const" {
		t.Fatalf("expected const MAX, got %+v", s)
	}
	if s := findSym(syms, "add"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func add, got %+v", s)
	}
	if s := findSym(syms, "square"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func square, got %+v", s)
	}
	if s := findSym(syms, "Store"); s == nil || s.Kind != "class" {
		t.Fatalf("expected class Store, got %+v", s)
	}
	if s := findSym(syms, "Store.constructor"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Store.constructor, got %+v", s)
	}
	if s := findSym(syms, "Store.total"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Store.total, got %+v", s)
	}
	if s := findSym(syms, "Store.push"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Store.push, got %+v", s)
	}
	if s := findSym(syms, "helper"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func helper, got %+v", s)
	}
	if !contains(calls["Store.total"], "items") {
		t.Fatalf("expected Store.total to call items, got %v", calls["Store.total"])
	}
	if !contains(calls["Store.push"], "add") {
		t.Fatalf("expected Store.push to call add, got %v", calls["Store.push"])
	}
	if !contains(calls["helper"], "Store") || !contains(calls["helper"], "square") {
		t.Fatalf("expected helper to call Store and square, got %v", calls["helper"])
	}
}

const tsSrc = `export interface Shape {
  area(): number
}

export enum Color {
  Red,
  Blue,
}

type Id = number

export const createId = (): Id => 1

export function render(s: Shape): string {
  return compute(s.area())
}

export class Circle implements Shape {
  area(): number {
    return this.radius()
  }
  radius(): number {
    return 3
  }
}
`

func TestTypescriptExtract(t *testing.T) {
	syms, calls, _, _, _ := extractForeign("shape.ts", []byte(tsSrc), "typescript")
	if s := findSym(syms, "Shape"); s == nil || s.Kind != "interface" {
		t.Fatalf("expected interface Shape, got %+v", s)
	}
	if s := findSym(syms, "Color"); s == nil || s.Kind != "enum" {
		t.Fatalf("expected enum Color, got %+v", s)
	}
	if s := findSym(syms, "Id"); s == nil || s.Kind != "type" {
		t.Fatalf("expected type Id, got %+v", s)
	}
	if s := findSym(syms, "createId"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func createId, got %+v", s)
	}
	if s := findSym(syms, "Circle"); s == nil || s.Kind != "class" {
		t.Fatalf("expected class Circle, got %+v", s)
	}
	if s := findSym(syms, "Circle.area"); s == nil || s.Kind != "method" || s.Receiver != "Circle" {
		t.Fatalf("expected method Circle.area, got %+v", s)
	}
	if !contains(calls["Circle.area"], "radius") {
		t.Fatalf("expected Circle.area to call radius, got %v", calls["Circle.area"])
	}
}

const rsSrc = `pub struct Point {
    x: i32,
    y: i32,
}

pub enum Shape {
    Circle,
    Square,
}

trait Area {
    fn area(&self) -> f64;
}

impl Point {
    pub fn new(x: i32, y: i32) -> Self {
        Point { x, y }
    }

    fn dist(&self) -> i32 {
        let d = Point::new(1, 2);
        d.x
    }
}

fn main() {
    let p = Point::new(0, 0);
    println!("{:?}", p);
}
`

func TestRustExtract(t *testing.T) {
	syms, calls, _, _, _ := extractForeign("point.rs", []byte(rsSrc), "rust")
	if s := findSym(syms, "Point"); s == nil || s.Kind != "struct" {
		t.Fatalf("expected struct Point, got %+v", s)
	}
	if s := findSym(syms, "Shape"); s == nil || s.Kind != "enum" {
		t.Fatalf("expected enum Shape, got %+v", s)
	}
	if s := findSym(syms, "Area"); s == nil || s.Kind != "trait" {
		t.Fatalf("expected trait Area, got %+v", s)
	}
	if s := findSym(syms, "Area.area"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Area.area, got %+v", s)
	}
	if s := findSym(syms, "Point.new"); s == nil || s.Kind != "method" || s.Receiver != "Point" {
		t.Fatalf("expected method Point.new, got %+v", s)
	}
	if s := findSym(syms, "main"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func main, got %+v", s)
	}
	if !contains(calls["Point.dist"], "new") {
		t.Fatalf("expected Point.dist to call new, got %v", calls["Point.dist"])
	}
}

const cppSrc = `#include <vector>

struct Point {
    int x;
    int y;
};

class Shape {
public:
    Shape();
    ~Shape();
    double area() const;
};

Shape::Shape() {}

double Shape::area() const {
    return calc();
}

void Shape::draw(Renderer& r) {
    r.paint(*this);
}

int main(int argc, char** argv) {
    Shape s;
    s.draw(g);
    return 0;
}
`

func TestCppExtract(t *testing.T) {
	syms, calls, _, _, _ := extractForeign("shape.cpp", []byte(cppSrc), "cpp")
	if s := findSym(syms, "Point"); s == nil || s.Kind != "struct" {
		t.Fatalf("expected struct Point, got %+v", s)
	}
	if s := findSym(syms, "Shape"); s == nil || s.Kind != "class" {
		t.Fatalf("expected class Shape, got %+v", s)
	}
	if s := findSym(syms, "Shape.Shape"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Shape.Shape (ctor), got %+v", s)
	}
	if s := findSym(syms, "Shape.area"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Shape.area, got %+v", s)
	}
	if s := findSym(syms, "Shape.draw"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Shape.draw, got %+v", s)
	}
	if s := findSym(syms, "main"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func main, got %+v", s)
	}
	if !contains(calls["Shape.area"], "calc") {
		t.Fatalf("expected Shape.area to call calc, got %v", calls["Shape.area"])
	}
	if !contains(calls["main"], "s.draw") {
		t.Fatalf("expected main to call s.draw, got %v", calls["main"])
	}
}

const javaSrc = `package com.example;

import java.util.List;

public class App {
    private int count;

    public App(int count) {
        this.count = count;
    }

    public int add(int a, int b) {
        return a + b;
    }

    public static void main(String[] args) {
        App app = new App(1);
        System.out.println(app.add(1, 2));
    }
}

class Helper {
    void run() {
        util.log("x");
    }
}
`

func TestJavaExtract(t *testing.T) {
	syms, calls, _, _, _ := extractForeign("App.java", []byte(javaSrc), "java")
	if s := findSym(syms, "App"); s == nil || s.Kind != "class" {
		t.Fatalf("expected class App, got %+v", s)
	}
	if s := findSym(syms, "App.App"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method App.App (ctor), got %+v", s)
	}
	if s := findSym(syms, "App.add"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method App.add, got %+v", s)
	}
	if s := findSym(syms, "App.main"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method App.main, got %+v", s)
	}
	if s := findSym(syms, "Helper"); s == nil || s.Kind != "class" {
		t.Fatalf("expected class Helper, got %+v", s)
	}
	if s := findSym(syms, "Helper.run"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Helper.run, got %+v", s)
	}
	if !contains(calls["App.main"], "App") || !contains(calls["App.main"], "System.out.println") {
		t.Fatalf("expected App.main calls, got %v", calls["App.main"])
	}
	if !contains(calls["Helper.run"], "util.log") {
		t.Fatalf("expected Helper.run to call util.log, got %v", calls["Helper.run"])
	}
}

const rbSrc = `module Util
  def self.log(msg)
    puts msg
  end
end

class User
  def initialize(name)
    @name = name
  end

  def validate(token)
    check(token)
  end
end

def main
  u = User.new("x")
  log = Util.log("hi")
end
`

func TestRubyExtract(t *testing.T) {
	syms, calls, _, _, _ := extractForeign("app.rb", []byte(rbSrc), "ruby")
	if s := findSym(syms, "Util"); s == nil || s.Kind != "module" {
		t.Fatalf("expected module Util, got %+v", s)
	}
	if s := findSym(syms, "Util.log"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method Util.log, got %+v", s)
	}
	if s := findSym(syms, "User"); s == nil || s.Kind != "class" {
		t.Fatalf("expected class User, got %+v", s)
	}
	if s := findSym(syms, "User.initialize"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method User.initialize, got %+v", s)
	}
	if s := findSym(syms, "User.validate"); s == nil || s.Kind != "method" {
		t.Fatalf("expected method User.validate, got %+v", s)
	}
	if s := findSym(syms, "main"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func main, got %+v", s)
	}
	if !contains(calls["User.validate"], "check") {
		t.Fatalf("expected User.validate to call check, got %v", calls["User.validate"])
	}
	if !contains(calls["main"], "User.new") || !contains(calls["main"], "Util.log") {
		t.Fatalf("expected main calls, got %v", calls["main"])
	}
}

func TestLanguageDetection(t *testing.T) {
	extCases := map[string]string{
		"a.go": "go", "a.py": "python", "a.js": "javascript", "a.tsx": "typescript",
		"a.vue": "javascript", "a.svelte": "javascript", "a.astro": "typescript",
		"a.css": "css", "a.scss": "css", "a.less": "css",
		"a.html": "html", "a.htm": "html",
		"a.md": "markdown", "a.mdx": "markdown", "a.markdown": "markdown",
		"a.json": "json", "a.jsonc": "json",
		"a.yml": "yaml", "a.yaml": "yaml",
		"a.rs": "rust", "a.c": "c", "a.cpp": "cpp", "a.cs": "csharp",
		"a.java": "java", "a.rb": "ruby", "a.php": "php", "a.sh": "shell",
		"a.txt": "",
	}
	for rel, want := range extCases {
		if got := detectLang(rel, nil); got != want {
			t.Fatalf("detectLang(%s) = %q, want %q", rel, got, want)
		}
	}
	shebangCases := []struct {
		src  string
		want string
	}{
		{"#!/usr/bin/env python3\nprint(1)\n", "python"},
		{"#!/bin/bash\necho hi\n", "shell"},
		{"#!/usr/bin/env node\nconsole.log(1)\n", "javascript"},
		{"#!/usr/bin/env ruby\nputs 1\n", "ruby"},
		{"no shebang here\n", ""},
	}
	for _, c := range shebangCases {
		if got := detectLang("script", []byte(c.src)); got != c.want {
			t.Fatalf("detectLang(shebang) = %q, want %q", got, c.want)
		}
	}
}

func TestBuildMixedProject(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go":   srcMain,
		"app.py":    pySrc,
		"store.js":  jsSrc,
		"point.rs":  rsSrc,
		"shape.cpp": cppSrc,
		"App.java":  javaSrc,
		"app.rb":    rbSrc,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	langs := ix.Languages()
	for _, want := range []string{"go", "python", "javascript", "rust", "cpp", "java", "ruby"} {
		if !contains(langs, want) {
			t.Fatalf("expected language %s in index, got %v", want, langs)
		}
	}
	// Search across languages.
	if len(ix.Search("greet", 10)) == 0 {
		t.Fatal("expected cross-language search hit for greet")
	}
	if len(ix.Search("method *login*", 10)) == 0 {
		t.Fatal("expected method login search hit")
	}
	// Reverse callers across languages.
	if !contains(ix.CallersOf("greet"), "main") {
		t.Fatalf("expected greet callers from Go main, got %v", ix.CallersOf("greet"))
	}
	if len(ix.CallersOf("validate")) == 0 {
		t.Fatalf("expected validate callers (Python), got %v", ix.CallersOf("validate"))
	}
}

func TestSearchKindPrefixes(t *testing.T) {
	syms := symsIn("python", pySrc)
	_ = syms
	dir := writeTree(t, map[string]string{"app.py": pySrc})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Search("class User", 10)) != 1 {
		t.Fatal("expected class User")
	}
	if len(ix.Search("func greet", 10)) != 1 {
		t.Fatal("expected func greet")
	}
	if len(ix.Search("method *login*", 10)) != 1 {
		t.Fatal("expected method User.login")
	}
}

func TestSfcExtractVue(t *testing.T) {
	src := `<template>
  <div class="card" id="main">{{ count }}</div>
</template>

<script setup lang="ts">
import { ref } from "vue"
const count = ref(0)
export function bump(): number {
  return count.value + 1
}
</script>

<style scoped>
.card { color: red; }
</style>
`
	syms, _, _, _, _ := extractForeign("components/Counter.vue", []byte(src), "typescript")
	if s := findSym(syms, "bump"); s == nil {
		t.Fatalf("expected func bump from vue script, got %v", syms)
	}
	if s := findSym(syms, "count"); s == nil || s.Kind != "const" {
		t.Fatalf("expected const count from vue script, got %v", s)
	}
	for _, bad := range []string{"ref", "card"} {
		if findSym(syms, bad) != nil {
			t.Fatalf("template/style symbol %q should not leak into the index", bad)
		}
	}
	if s := findSym(syms, "main"); s != nil {
		t.Fatalf("html id %q from template must not be indexed as a script symbol", "main")
	}
}

func TestSfcExtractSvelte(t *testing.T) {
	src := `<script>
  export let name = "world"
  function greet() {
    return "hello " + name
  }
</script>

<h1 id="title">Hello {name}</h1>
`
	syms, _, _, _, _ := extractForeign("widget.svelte", []byte(src), "javascript")
	if s := findSym(syms, "greet"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func greet from svelte script, got %v", s)
	}
	if s := findSym(syms, "name"); s == nil {
		t.Fatalf("expected let name from svelte script, got %v", s)
	}
}

func TestCssExtract(t *testing.T) {
	src := `.card {
  color: #fff;
  padding: 1rem;
}

#app-root {
  --gap: 1rem;
}

:root { --gap2: 2rem; }

@keyframes slide-in {
  from { opacity: 0; }
}
`
	syms := symsIn("css", src)
	if s := findSym(syms, "card"); s == nil || s.Kind != "class" {
		t.Fatalf("expected class card, got %v", s)
	}
	if s := findSym(syms, "app-root"); s == nil || s.Kind != "const" {
		t.Fatalf("expected const app-root, got %v", s)
	}
	if s := findSym(syms, "slide-in"); s == nil || s.Kind != "func" {
		t.Fatalf("expected func slide-in (keyframes), got %v", s)
	}
	if s := findSym(syms, "--gap"); s == nil || s.Kind != "prop" {
		t.Fatalf("expected prop --gap, got %v", s)
	}
	if s := findSym(syms, "--gap2"); s == nil || s.Kind != "prop" {
		t.Fatalf("expected inline prop --gap2, got %v", s)
	}
	for _, bad := range []string{"fff", "color"} {
		if findSym(syms, bad) != nil {
			t.Fatalf("declaration value %q should not be a symbol", bad)
		}
	}
}

func TestHtmlExtract(t *testing.T) {
	src := `<!doctype html>
<html>
<body>
  <!-- commented out: <div id="hidden"></div> -->
  <nav id="navbar"><a href="/">home</a></nav>
  <main id="content"></main>
</body>
</html>
`
	syms := symsIn("html", src)
	if s := findSym(syms, "navbar"); s == nil || s.Kind != "const" {
		t.Fatalf("expected const navbar, got %v", s)
	}
	if s := findSym(syms, "content"); s == nil {
		t.Fatalf("expected const content, got %v", s)
	}
	if findSym(syms, "hidden") != nil {
		t.Fatal("html comment ids must not be indexed")
	}
}

func TestSfcDetection(t *testing.T) {
	if got := detectLang("a.vue", []byte(`<script setup lang="ts">`)); got != "typescript" {
		t.Fatalf("expected typescript for vue ts, got %q", got)
	}
	if got := detectLang("a.vue", []byte(`<script>`)); got != "javascript" {
		t.Fatalf("expected javascript for plain vue, got %q", got)
	}
	if got := detectLang("a.svelte", []byte(`<script lang="ts">`)); got != "typescript" {
		t.Fatalf("expected typescript for svelte ts, got %q", got)
	}
	if got := detectLang("a.astro", nil); got != "typescript" {
		t.Fatalf("expected typescript for astro, got %q", got)
	}
	if got := detectLang("style.less", nil); got != "css" {
		t.Fatalf("expected css for less, got %q", got)
	}
	if got := detectLang("README.md", nil); got != "markdown" {
		t.Fatalf("expected markdown, got %q", got)
	}
	if got := detectLang("pkg.json", nil); got != "json" {
		t.Fatalf("expected json, got %q", got)
	}
	if got := detectLang("config.yaml", nil); got != "yaml" {
		t.Fatalf("expected yaml, got %q", got)
	}
	if got := detectLang("style.css", nil); got != "css" {
		t.Fatalf("expected css, got %q", got)
	}
	if got := detectLang("index.html", nil); got != "html" {
		t.Fatalf("expected html, got %q", got)
	}
	if got := detectLang("notes.txt", nil); got != "" {
		t.Fatalf("expected unsupported, got %q", got)
	}
}

func TestAstroExtract(t *testing.T) {
	src := `---
import Layout from "../layouts/Layout.astro";
interface Props {
  title: string;
}
const title = "Home";
---
<Layout>
  <main id="hero"><h1>{title}</h1></main>
</Layout>
<script>
  document.title = "hi";
</script>
`
	syms, _, _, _, _ := extractForeign("pages/Index.astro", []byte(src), "typescript")
	if s := findSym(syms, "Props"); s == nil || s.Kind != "interface" {
		t.Fatalf("expected interface Props from astro frontmatter, got %v", s)
	}
	if s := findSym(syms, "title"); s == nil || s.Kind != "const" {
		t.Fatalf("expected const title from astro frontmatter, got %v", s)
	}
	if findSym(syms, "hero") != nil {
		t.Fatal("astro markup id must not leak into the index")
	}
}

func TestMarkdownExtract(t *testing.T) {
	src := `# kern

## Install

Quick start with one line:

### CLI reference

## Contributing
`
	syms := symsIn("markdown", src)
	for _, h := range []string{"kern", "Install", "CLI reference", "Contributing"} {
		if s := findSym(syms, h); s == nil || s.Kind != "heading" {
			t.Fatalf("expected heading %q, got %v", h, s)
		}
	}
	for _, bad := range []string{"Quick start with one line:"} {
		if findSym(syms, bad) != nil {
			t.Fatalf("paragraph %q must not be a heading", bad)
		}
	}
}

func TestJsonExtract(t *testing.T) {
	src := `{
  "name": "kern",
  "scripts": {
    "build": "go build"
  },
  "// comment": {"x": 1}
}
`
	syms := symsIn("json", src)
	if s := findSym(syms, "name"); s == nil || s.Kind != "prop" {
		t.Fatalf("expected prop name, got %v", s)
	}
	if s := findSym(syms, "scripts"); s == nil || s.Kind != "prop" {
		t.Fatalf("expected prop scripts, got %v", s)
	}
	if findSym(syms, "build") == nil {
		t.Fatal("expected nested prop build (line-start key)")
	}
}

func TestYamlExtract(t *testing.T) {
	src := `name: kern
version: 0.1.0
tasks:
  build: go build
  # deploy: rsync (commented out)
`
	syms := symsIn("yaml", src)
	for _, k := range []string{"name", "version", "tasks"} {
		if s := findSym(syms, k); s == nil || s.Kind != "prop" {
			t.Fatalf("expected prop %s, got %v", k, s)
		}
	}
	for _, bad := range []string{"build", "deploy", "go build"} {
		if findSym(syms, bad) != nil {
			t.Fatalf("nested/comment key %q must not be a top-level symbol", bad)
		}
	}
}
