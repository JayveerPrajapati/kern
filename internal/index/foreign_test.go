package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/langparse"
)

func findSym(syms []Symbol, name string) *Symbol {
	for i := range syms {
		if syms[i].FullName() == name {
			return &syms[i]
		}
	}
	return nil
}

func symsIn(lang string, src string) []Symbol {
	syms, calls, _, _, _ := langparse.ExtractForeign("file."+extFor(lang), []byte(src), lang, tsExtract)
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

func TestDocFilesDoNotOwnCallEdges(t *testing.T) {
	// An HTML page whose script/template text mentions the code symbol
	// "runReport" (the same shape that used to over-match in kern's own
	// web/*.html templates).
	src := []byte(`<html>
<head><title>dashboard</title></head>
<body>
  <button onclick="runReport()">Go</button>
</body>
</html>
`)
	_, calls, _, _, _ := langparse.ExtractForeign("dashboard.html", src, "html", tsExtract)
	if got := calls["file:dashboard.html"]; len(got) != 0 {
		t.Fatalf("html file owns call edges %v, want none (doc files are not callers)", got)
	}
	// Markdown mentioning a code symbol must not own calls either.
	_, calls, _, _, _ = langparse.ExtractForeign("README.md", []byte("# Public\n\nCall add() from the docs.\n"), "markdown", tsExtract)
	if got := calls["file:README.md"]; len(got) != 0 {
		t.Fatalf("markdown file owns call edges %v, want none (doc files are not callers)", got)
	}
}

func TestSingleLineFuncBodyDoesNotSwallowFile(t *testing.T) {
	dir := t.TempDir()
	src := `export function useAuth() { return { user: 1 } }
export function useInfiniteData<T>(url: string) { return { data: [] as T[] } }
export function Page() {
  const auth = useAuth()
  const data = useInfiniteData<UserDto>("/api/users")
  return { auth, data }
}
`
	if err := os.WriteFile(filepath.Join(dir, "app.ts"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Page calls both; the single-line functions call nothing (no spurious
	// cross-edges).
	if got := ix.Callers["useAuth"]; len(got) != 1 || got[0] != "Page" {
		t.Fatalf("Callers[useAuth] = %v, want [Page] (no spurious callers)", got)
	}
	if got := ix.Callers["useInfiniteData"]; len(got) != 1 || got[0] != "Page" {
		t.Fatalf("Callers[useInfiniteData] = %v, want [Page]", got)
	}
	if got := ix.Calls["useAuth"]; len(got) != 0 {
		t.Fatalf("useAuth callees = %v, want none (spurious cross-edges)", got)
	}
}
