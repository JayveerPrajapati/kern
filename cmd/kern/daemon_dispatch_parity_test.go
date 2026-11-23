package main

// Slice 5: pin the daemon dispatch MAPPING without binding a Unix socket
// (test sandboxes deny socket binds). The daemon is a proxy in front of the
// same in-process command path: it only forwards commands on the read-only
// whitelist, and KERN_DAEMON=0 must fall back to the in-process path. The
// live socket round-trip is deliberately not exercised by default.

import "testing"

func TestDaemonProxyMapsGraphReadCommands(t *testing.T) {
	for _, cmd := range []string{"graph", "context", "explore", "impact", "search", "why", "refs"} {
		if !daemonProxyCommands[cmd] {
			t.Errorf("daemon proxy must map read command %q", cmd)
		}
	}
}

func TestDaemonProxyExcludesMutatingCommands(t *testing.T) {
	for _, cmd := range []string{"apply", "commit", "setup", "uninstall", "update", "heal", "execute"} {
		if daemonProxyCommands[cmd] {
			t.Errorf("daemon proxy must NOT forward mutating command %q", cmd)
		}
	}
}

// TestDaemonDispatchFallsBackInProcess pins that disabling the daemon (or
// running inside a daemon child) yields (zero, false) so main() executes the
// exact in-process command path these tests compare — never a partial answer.
func TestDaemonDispatchFallsBackInProcess(t *testing.T) {
	t.Run("KERN_DAEMON=0", func(t *testing.T) {
		t.Setenv("KERN_DAEMON", "0")
		resp, ok := daemonDispatch("graph", []string{"writeTree"})
		if ok {
			t.Fatalf("daemonDispatch must fall back in-process when disabled, got %+v", resp)
		}
		if resp.Stdout != "" || resp.Exit != 0 {
			t.Fatalf("fallback response must be zero-valued, got %+v", resp)
		}
	})
	t.Run("daemon child", func(t *testing.T) {
		t.Setenv("KERN_DAEMON", "1")
		t.Setenv("KERN_DAEMON_CHILD", "1")
		if _, ok := daemonDispatch("graph", []string{"writeTree"}); ok {
			t.Fatal("daemonDispatch must not re-enter the proxy inside a daemon child")
		}
	})
}
