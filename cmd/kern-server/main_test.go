package main

import "testing"

// TestIsLoopbackAddr (P2-10): loopback binds stay auth-free; a bare ":port"
// or an explicit non-loopback host binds all interfaces and must be treated
// as non-loopback (fail closed). Parse failures fail closed too.
func TestIsLoopbackAddr(t *testing.T) {
	loopback := []string{"127.0.0.1:8090", "localhost:8090", "[::1]:8090"}
	nonLoopback := []string{":8090", "0.0.0.0:8090", "192.168.1.5:8090", "10.0.0.1:443", "garbage"}
	for _, a := range loopback {
		if !isLoopbackAddr(a) {
			t.Errorf("isLoopbackAddr(%q) = false, want true", a)
		}
	}
	for _, a := range nonLoopback {
		if isLoopbackAddr(a) {
			t.Errorf("isLoopbackAddr(%q) = true, want false", a)
		}
	}
}

// TestWantsVersion: -v/--version and the "version" positional all request
// the version print-and-exit path; empty args and unrelated positionals do
// not. Mirrors the kern-mcp fix (commit 76031df) so `kern-server version`
// never silently starts the server.
func TestWantsVersion(t *testing.T) {
	tests := []struct {
		name  string
		show  bool
		short bool
		args  []string
		want  bool
	}{
		{name: "version positional", args: []string{"version"}, want: true},
		{name: "-v short flag", short: true, want: true},
		{name: "--version long flag", show: true, want: true},
		{name: "empty args", want: false},
		{name: "unrelated positional", args: []string{"serve"}, want: false},
		// flag.Parse stops at the first positional, so ["--root","x","version"]
		// leaves only ["version"] in args — flag values never reach wantsVersion.
		{name: "flag value then positional", args: []string{"version"}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantsVersion(tc.show, tc.short, tc.args); got != tc.want {
				t.Errorf("wantsVersion(show=%v, short=%v, args=%v) = %v, want %v", tc.show, tc.short, tc.args, got, tc.want)
			}
		})
	}
}
