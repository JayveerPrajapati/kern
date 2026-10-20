package doctor

import (
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func healthy() globalHealthInput {
	g := []byte("#!/bin/sh\nguard\n")
	return globalHealthInput{
		Root: "/work/app", GuardPath: "/h/.kern/hooks/kern-guard.sh",
		GuardInstalled: g, GuardEmbedded: g, KernResolvable: true, Env: noEnv,
		KernHooksDir: "/h/.kern/git-hooks",
	}
}

func find(fs []Finding, check string) *Finding {
	for i := range fs {
		if fs[i].Check == check {
			return &fs[i]
		}
	}
	return nil
}

func TestGlobalHealthHealthyIsQuiet(t *testing.T) {
	fs := globalHealthFindings(healthy())
	if len(fs) != 1 || fs[0].Check != "global-guard" || fs[0].Level != "ok" {
		t.Fatalf("a healthy install should yield one ok finding, got %+v", fs)
	}
}

func TestGlobalHealthGuardMissingAndStale(t *testing.T) {
	in := healthy()
	in.GuardInstalled = nil
	if f := find(globalHealthFindings(in), "global-guard"); f == nil || f.Level != "warn" || !strings.Contains(f.Detail, "kern setup --global-only") {
		t.Errorf("missing guard: %+v", f)
	}
	in = healthy()
	in.GuardInstalled = []byte("old")
	if f := find(globalHealthFindings(in), "global-guard"); f == nil || f.Level != "warn" || !strings.Contains(f.Detail, "stale") {
		t.Errorf("stale guard: %+v", f)
	}
}

func TestGlobalHealthWarnsWhenGuardCannotReachKern(t *testing.T) {
	in := healthy()
	in.KernResolvable = false
	if f := find(globalHealthFindings(in), "global-guard-active"); f == nil || f.Level != "warn" || !strings.Contains(f.Detail, "fails open") {
		t.Errorf("unreachable kern: %+v", f)
	}
}

func TestGlobalHealthReportsOptOuts(t *testing.T) {
	in := healthy()
	in.Env = func(k string) string {
		if k == "KERN_DISABLED" || k == "KERN_BYPASS" {
			return "1"
		}
		return ""
	}
	in.RepoOptOut = true
	n := 0
	for _, f := range globalHealthFindings(in) {
		if f.Check == "kern-opt-out" && f.Level == "warn" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("want 3 opt-out warnings (KERN_DISABLED, bypass, .kern/off), got %d", n)
	}
}

func TestGlobalHealthAutoIndexRefusal(t *testing.T) {
	in := healthy()
	in.UnsafeReason = "it is your home directory"
	if f := find(globalHealthFindings(in), "auto-index"); f == nil || !strings.Contains(f.Detail, "home directory") {
		t.Errorf("unsafe root: %+v", f)
	}
	in.HasIndex = true // deliberately indexed before: no warning
	if f := find(globalHealthFindings(in), "auto-index"); f != nil {
		t.Errorf("a deliberately indexed root must not warn: %+v", f)
	}
}

func TestGlobalHealthGitHooksShadow(t *testing.T) {
	in := healthy()
	in.RepoHooksPath = ".husky"
	if f := find(globalHealthFindings(in), "git-hooks-shadow"); f == nil || !strings.Contains(f.Detail, ".husky") {
		t.Errorf("husky-style shadow: %+v", f)
	}
	in.RepoHooksPath = "/h/.kern/git-hooks/"
	if f := find(globalHealthFindings(in), "git-hooks-shadow"); f != nil {
		t.Errorf("kern's own hooksPath is not a shadow: %+v", f)
	}
}
