package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

// runMetaExit recovers the fatalUsage sentinel from runMeta.
func runMetaExit(t *testing.T, rest []string) (code int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	runMeta(rest)
	return 0
}

// runReposExit recovers the fatalUsage sentinel from runRepos.
func runReposExit(t *testing.T, rest []string) (code int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	runRepos(rest)
	return 0
}

// runGuideExit recovers the fatalUsage sentinel from runGuide.
func runGuideExit(t *testing.T, rest []string) (code int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	runGuide(rest)
	return 0
}

// runExplainExit recovers the fatalUsage sentinel from runExplain.
func runExplainExit(t *testing.T, rest []string) (code int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	runExplain(rest)
	return 0
}

// runCrossRepoImpactExit recovers the fatalUsage sentinel from
// runCrossRepoImpact.
func runCrossRepoImpactExit(t *testing.T, rest []string) (code int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	runCrossRepoImpact(rest)
	return 0
}

// TestMetaRejectsUnknownFlagAfterRequest (QA): `kern meta "some request"
// --nonsense` must exit 2 instead of silently appending the unknown flag to
// the free-form request.
func TestMetaRejectsUnknownFlagAfterRequest(t *testing.T) {
	code := runMetaExit(t, []string{"some request", "--nonsense"})
	if code != 2 {
		t.Fatalf("kern meta \"some request\" --nonsense exit code = %d, want 2 (usage error)", code)
	}
}

// TestMetaRejectsUnknownFlagMessage checks the stderr text names the flag.
func TestMetaRejectsUnknownFlagMessage(t *testing.T) {
	stderr := captureStderr(t, func() {
		_ = runMetaExit(t, []string{"some request", "--nonsense"})
	})
	for _, want := range []string{"unknown flag", "--nonsense"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("meta unknown-flag message missing %q; got:\n%s", want, stderr)
		}
	}
}

// TestMetaKnownFlagsStillWork (QA): known flags must keep exiting 0.
func TestMetaKnownFlagsStillWork(t *testing.T) {
	if code := runMetaExit(t, []string{"--help"}); code != 0 {
		t.Fatalf("kern meta --help exit code = %d, want 0", code)
	}
	if code := runMetaExit(t, []string{"-h"}); code != 0 {
		t.Fatalf("kern meta -h exit code = %d, want 0", code)
	}
}

// TestMetaRootFlagStillParsesKnownFlagValue: `--root DIR` must be consumed
// as a known flag (not rejected as unknown). With no request, meta still
// exits 2 via the missing-request usage error — and that error must not be
// the "unknown flag" rejection.
func TestMetaRootFlagStillParsesKnownFlagValue(t *testing.T) {
	stderr := captureStderr(t, func() {
		code := runMetaExit(t, []string{"--root", "."})
		if code != 2 {
			t.Fatalf("kern meta --root . (no request) exit code = %d, want 2 (missing request)", code)
		}
	})
	if strings.Contains(stderr, "unknown flag") {
		t.Errorf("kern meta --root . rejected --root as unknown flag; got:\n%s", stderr)
	}
}

// TestReposRejectsUnknownFlag (QA): `kern repos --nonsense` must exit 2.
func TestReposRejectsUnknownFlag(t *testing.T) {
	code := runReposExit(t, []string{"--nonsense"})
	if code != 2 {
		t.Fatalf("kern repos --nonsense exit code = %d, want 2 (usage error)", code)
	}
}

// TestReposListRejectsTrailingUnknownFlag (QA): `kern repos list --nonsense`
// must exit 2 instead of silently ignoring the flag.
func TestReposListRejectsTrailingUnknownFlag(t *testing.T) {
	code := runReposExit(t, []string{"list", "--nonsense"})
	if code != 2 {
		t.Fatalf("kern repos list --nonsense exit code = %d, want 2 (usage error)", code)
	}
}

// TestReposKnownPathsStillWork (QA): known invocations must keep exiting 0.
func TestReposKnownPathsStillWork(t *testing.T) {
	if code := runReposExit(t, nil); code != 0 {
		t.Fatalf("bare kern repos exit code = %d, want 0", code)
	}
	if code := runReposExit(t, []string{"list"}); code != 0 {
		t.Fatalf("kern repos list exit code = %d, want 0", code)
	}
}

// TestGuideRejectsUnknownFlag (QA): `kern guide --nonsense` must exit 2.
func TestGuideRejectsUnknownFlag(t *testing.T) {
	code := runGuideExit(t, []string{"--nonsense"})
	if code != 2 {
		t.Fatalf("kern guide --nonsense exit code = %d, want 2 (usage error)", code)
	}
}

// TestGuideKnownCallStillWorks: a bare `kern guide` must still exit 0.
func TestGuideKnownCallStillWorks(t *testing.T) {
	if code := runGuideExit(t, nil); code != 0 {
		t.Fatalf("bare kern guide exit code = %d, want 0", code)
	}
}

// TestExplainRejectsTrailingUnknownFlag (QA): Go's flag package stops at the
// first positional, so `kern explain <sym> --nonsense` must exit 2, not run.
func TestExplainRejectsTrailingUnknownFlag(t *testing.T) {
	code := runExplainExit(t, []string{"SomeSymbol", "--nonsense"})
	if code != 2 {
		t.Fatalf("kern explain <sym> --nonsense exit code = %d, want 2 (usage error)", code)
	}
}

// TestCrossRepoImpactRejectsTrailingUnknownFlag (QA): `kern cross-repo-impact
// <sym> --nonsense` must exit 2, not run.
func TestCrossRepoImpactRejectsTrailingUnknownFlag(t *testing.T) {
	code := runCrossRepoImpactExit(t, []string{"SomeSymbol", "--nonsense"})
	if code != 2 {
		t.Fatalf("kern cross-repo-impact <sym> --nonsense exit code = %d, want 2 (usage error)", code)
	}
}

// TestMetaRefusesNoCodeIntentExits2 (P1): a request with no code intent
// exits 2 (usage-style) with the refusal message instead of running a junk
// symbol search (which used to exit 0 with confident-wrong results).
func TestMetaRefusesNoCodeIntentExits2(t *testing.T) {
	code := runMetaExit(t, []string{"make me a sandwich"})
	if code != 2 {
		t.Fatalf("kern meta \"make me a sandwich\" exit code = %d, want 2 (no code intent)", code)
	}
}

// TestMetaRefusesNoCodeIntentMessage: the refusal guidance names the
// explicit tools to use instead of trusting a junk result.
func TestMetaRefusesNoCodeIntentMessage(t *testing.T) {
	stderr := captureStderr(t, func() {
		_ = runMetaExit(t, []string{"make me a sandwich"})
	})
	for _, want := range []string{"no code intent detected", "kern search", "kern arch", "kern buddy"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal message missing %q; got:\n%s", want, stderr)
		}
	}
}

// TestMetaStillRoutesCodeRequests (P1): code-intent requests keep routing
// through the CLI path — the refusal gate only fires when there is NO code
// intent. "how does dispatch work" classifies to explore (the empty server
// then errors loading the index, which is a routing outcome, not a refusal).
func TestMetaStillRoutesCodeRequests(t *testing.T) {
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	_, err := srv.HandleMeta(context.Background(), map[string]any{"request": "how does dispatch work?"})
	if mcp.IsNoCodeIntent(err) {
		t.Fatalf("code-intent request must not be refused: %v", err)
	}
}
