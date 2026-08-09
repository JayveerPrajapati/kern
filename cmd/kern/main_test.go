package main

import "testing"

func TestParseFlagsPack(t *testing.T) {
	f, rest := parseFlags([]string{"--max-tokens", "4000", "--no-instructions", "--out", "x.txt", "."})
	if f.maxTokens != 4000 {
		t.Fatalf("expected --max-tokens 4000, got %d", f.maxTokens)
	}
	if !f.noinstructions {
		t.Fatal("expected --no-instructions parsed")
	}
	if f.out != "x.txt" {
		t.Fatalf("expected --out x.txt, got %q", f.out)
	}
	if len(rest) != 1 || rest[0] != "." {
		t.Fatalf("expected positional root preserved, got %v", rest)
	}
	// Defaults: instructions included, no token budget.
	f2, rest2 := parseFlags([]string{"."})
	if f2.noinstructions {
		t.Fatal("expected instructions included by default")
	}
	if f2.maxTokens != 0 {
		t.Fatalf("expected no budget by default, got %d", f2.maxTokens)
	}
	if len(rest2) != 1 || rest2[0] != "." {
		t.Fatalf("expected positional root preserved, got %v", rest2)
	}
}

func TestParseFlagsHTTP(t *testing.T) {
	f, rest := parseFlags([]string{"--http", "127.0.0.1:8080", "extra"})
	if f.http != "127.0.0.1:8080" {
		t.Fatalf("expected --http parsed, got %q", f.http)
	}
	if len(rest) != 1 || rest[0] != "extra" {
		t.Fatalf("expected positional args preserved, got %v", rest)
	}
}

func TestMCPHTTPAddrResolution(t *testing.T) {
	cases := []struct {
		name string
		args []string
		http string
		want string
	}{
		{"positional wins", []string{"127.0.0.1:9000"}, "", "127.0.0.1:9000"},
		{"flag fallback", nil, ":9000", ":9000"},
		{"empty means stdio", nil, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := flags{http: c.http}
			if got := mcpHTTPAddr(c.args, f); got != c.want {
				t.Fatalf("mcpHTTPAddr(%v, %q) = %q, want %q", c.args, c.http, got, c.want)
			}
		})
	}
	// End-to-end through parseFlags, matching the real main() call path
	// (rest is os.Args[2:], so it excludes the "mcp" subcommand).
	f, rest := parseFlags([]string{"--http", ":9000"})
	if got := mcpHTTPAddr(rest, f); got != ":9000" {
		t.Fatalf("parseFlags + mcpHTTPAddr = %q, want %q", got, ":9000")
	}
}

func TestParseFlagsHold(t *testing.T) {
	f, rest := parseFlags([]string{"--hold", "db-models"})
	if !f.hold {
		t.Fatal("expected --hold parsed")
	}
	if len(rest) != 1 || rest[0] != "db-models" {
		t.Fatalf("expected positional scope preserved, got %v", rest)
	}
	// Default: --hold absent.
	f2, _ := parseFlags([]string{"db-models"})
	if f2.hold {
		t.Fatal("expected --hold unset by default")
	}
}

func TestParseFlagsNearDepthDefaults(t *testing.T) {
	f, _ := parseFlags([]string{"near", "Foo"})
	if f.depth != -1 {
		t.Fatalf("expected depth default -1 (fallback to 2), got %d", f.depth)
	}
	f2, _ := parseFlags([]string{"near", "Foo", "--depth", "3"})
	if f2.depth != 3 {
		t.Fatalf("expected --depth 3 parsed, got %d", f2.depth)
	}
}

func TestParseFlagsSeverity(t *testing.T) {
	f, rest := parseFlags([]string{"myrepo", "--severity", "error,warning"})
	if f.severity != "error,warning" {
		t.Fatalf("expected --severity parsed, got %q", f.severity)
	}
	if len(rest) != 1 || rest[0] != "myrepo" {
		t.Fatalf("expected positional root preserved, got %v", rest)
	}
}
