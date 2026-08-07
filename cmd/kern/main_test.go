package main

import "testing"

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
