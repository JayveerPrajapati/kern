package grepscope

import "testing"

func TestSymbolShaped(t *testing.T) {
	cases := map[string]bool{
		"Load": true, "load_user": true, "User.Login": true, "foo.bar": true,
		"foo-bar": false, "*.go": false, "$x": false, "": false,
		"9lives": false, ".hidden": false, "governance": true,
	}
	for in, want := range cases {
		if got := SymbolShaped(in); got != want {
			t.Errorf("SymbolShaped(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFlagsOK(t *testing.T) {
	ok := [][]string{
		{"-rn", "foo", "dir"}, {"--recursive", "foo", "dir"},
		{"-n", "foo", "dir"}, {"-i", "foo"}, {"--", "foo", "dir"},
	}
	for _, args := range ok {
		if !FlagsOK(args) {
			t.Errorf("FlagsOK(%v) = false, want true", args)
		}
	}
	bad := [][]string{
		{"-e", "foo", "dir"}, {"-c", "foo", "dir"}, {"-l", "foo", "dir"},
		{"-A", "3", "foo", "dir"}, {"--include=*.go", "foo", "dir"},
		{"--files-with-matches", "foo", "dir"},
	}
	for _, args := range bad {
		if FlagsOK(args) {
			t.Errorf("FlagsOK(%v) = true, want false", args)
		}
	}
}

func TestCleanScope(t *testing.T) {
	ok := map[string]string{
		"internal": "internal", "internal/": "internal", "./internal": "internal",
		".": ".", "internal/governance/firewall.go": "internal/governance/firewall.go",
		"internal/../cmd": "cmd",
	}
	for in, want := range ok {
		got, valid := CleanScope(in)
		if !valid || got != want {
			t.Errorf("CleanScope(%q) = (%q,%v), want (%q,true)", in, got, valid, want)
		}
	}
	bad := []string{"", "-x", "~/x", "/abs/path", "../x", "internal/../../x", "a*b", "a?b", "a[b", "a\\b", "$HOME/x", "`x`"}
	for _, in := range bad {
		if got, valid := CleanScope(in); valid {
			t.Errorf("CleanScope(%q) = (%q,true), want refusal", in, got)
		}
	}
}

func TestScoped(t *testing.T) {
	pattern, scope, ok := Scoped([]string{"-rn", "governance", "internal"})
	if !ok || pattern != "governance" || scope != "internal" {
		t.Fatalf("Scoped(simple) = (%q,%q,%v)", pattern, scope, ok)
	}
	pattern, scope, ok = Scoped([]string{"-n", "edit", ".opencode/plugins/kern.ts"})
	if !ok || pattern != "edit" || scope != ".opencode/plugins/kern.ts" {
		t.Fatalf("Scoped(file) = (%q,%q,%v)", pattern, scope, ok)
	}
	bad := [][]string{
		{"-rn", "governance"},                   // unscoped: the existing Tier 1 path
		{"-rn", "go*", "internal"},              // not symbol-shaped
		{"-rn", "governance", "a", "b"},         // two targets
		{"-rn", "-l", "governance", "internal"}, // list shape
		{"-rn", "governance", "/abs"},           // absolute
		{"-rn", "governance", "../x"},           // escape
		{"--include=*.go", "governance", "dir"}, // include filter
		{"-e", "governance", "internal"},        // -e value misparse
	}
	for _, args := range bad {
		if _, _, ok := Scoped(args); ok {
			t.Errorf("Scoped(%v) = true, want false", args)
		}
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		file, scope string
		want        bool
	}{
		{"internal/governance/firewall.go", "internal", true},
		{"internal/governance/firewall.go", "internal/governance", true},
		{"internal/governance/firewall.go", "internal/governance/firewall.go", true},
		{"internalx/a.go", "internal", false},
		{"cmd/kern/main.go", "internal", false},
		{"a.go", ".", true},
		{"a.go", "", true},
		{"./a.go", "a.go", true},
	}
	for _, c := range cases {
		if got := Matches(c.file, c.scope); got != c.want {
			t.Errorf("Matches(%q,%q) = %v, want %v", c.file, c.scope, got, c.want)
		}
	}
}
