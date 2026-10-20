package metaroute_test

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/metaroute"
)

// QA 5.3 (question misrouting): question-shaped kern_meta requests must never
// route to build/exec/verify-class tools — "how does the verify API work
// here?" contains the keyword "verify" and used to classify to kern_verify,
// which ran a real 27-second build. The question guard rewrites those
// classifications to the search flow. These tests assert the ROUTING TABLE
// (no timing, no execution).

// TestClassifyMetaRequest_QuestionVerifyNeverBuilds pins the headline case:
// a question whose text mentions "verify" must classify to kern_search with
// the significant terms ("verify"), never to kern_verify.
func TestClassifyMetaRequest_QuestionVerifyNeverBuilds(t *testing.T) {
	tool, args := metaroute.ClassifyMetaRequest("how does the verify API work here?")
	if tool == "kern_verify" {
		t.Fatal("question-shaped request must never classify to kern_verify (it would run a build)")
	}
	if tool != "kern_search" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_search", tool)
	}
	if got := args["query"]; got != "verify" {
		t.Errorf("args[query] = %q, want %q (stopwords dropped, API dropped as generic)", got, "verify")
	}
}

// TestClassifyMetaRequest_QuestionGuardCoversEveryBuildClassTool pins that the
// guard covers the whole build/exec/verify family, not just kern_verify:
// question-shaped requests mentioning run/exec/heal keywords classify to the
// search flow.
func TestClassifyMetaRequest_QuestionGuardCoversEveryBuildClassTool(t *testing.T) {
	cases := []struct {
		request string
		want    string
	}{
		{"how does the verify API work here?", "kern_search"}, // "verify" keyword → kern_verify
		{"how do i execute this?", "kern_search"},             // "execute" → kern_exec
		{"how do i run this task?", "kern_search"},            // "run this task" → kern_run
		{"how do i fix the build?", "kern_search"},            // "fix the build" → kern_heal
		{"how do i heal this repo?", "kern_search"},           // "heal" → kern_heal
	}
	for _, tc := range cases {
		tool, _ := metaroute.ClassifyMetaRequest(tc.request)
		if tool == "kern_verify" || tool == "kern_exec" || tool == "kern_run" || tool == "kern_heal" {
			t.Errorf("%q: question-shaped request must never classify to build-class tool, got %q", tc.request, tool)
		}
		if tool != tc.want {
			t.Errorf("%q: ClassifyMetaRequest = %q, want %q", tc.request, tool, tc.want)
		}
	}
}

// TestClassifyMetaRequest_ImperativeVerifyUnaffected pins that the guard only
// fires for question-shaped requests: the imperative "run a full verification
// of this repo" keeps its build-class route (kern_verify via the keyword arm,
// or kern_run via the semantic escalation — both legitimately run the verify
// pipeline the user asked for; the acceptance test that routes kern_meta to a
// deep-catalog sub-tool depends on it). The guard must NOT rewrite it.
func TestClassifyMetaRequest_ImperativeVerifyUnaffected(t *testing.T) {
	tool, _ := metaroute.ClassifyMetaRequest("run a full verification of this repo")
	if tool == "kern_search" {
		t.Fatal("imperative verify request must keep its build-class route (guard must not fire)")
	}
	if tool != "kern_verify" && tool != "kern_run" {
		t.Fatalf("imperative verify request = %q, want kern_verify or kern_run", tool)
	}
	tool, _ = metaroute.ClassifyMetaRequest("verify this")
	if tool != "kern_verify" {
		t.Fatalf("imperative 'verify this' = %q, want kern_verify", tool)
	}
}

// TestClassifyMetaRequest_QuestionSymbolRoutesPreserved pins that the guard
// does NOT hijack question-shaped requests whose classification is already
// safe and useful: explore for how-does questions about real symbols, impact
// for what-breaks, arch for architecture. These are the pinned routes from
// TestHandleRoutingPreserved — the question gate only rewrites build-class
// tools.
func TestClassifyMetaRequest_QuestionSymbolRoutesPreserved(t *testing.T) {
	cases := []struct {
		request  string
		wantTool string
	}{
		{"how does dispatch work?", "kern_explore"},
		{"how does NewServer work?", "kern_explore"},
		{"what breaks if I change dispatch", "kern_impact"},
		{"show me the architecture", "kern_arch"},
		{"how does CLI command dispatch work in this repo?", "kern_search"},
	}
	for _, tc := range cases {
		tool, _ := metaroute.ClassifyMetaRequest(tc.request)
		if tool != tc.wantTool {
			t.Errorf("%q: ClassifyMetaRequest = %q, want %q", tc.request, tool, tc.wantTool)
		}
	}
}

// TestIsQuestionShape pins the question-shape predicate: trailing "?", leading
// question words, and leading imperative softeners count; imperative
// work-requests do not.
func TestIsQuestionShape(t *testing.T) {
	questions := []string{
		"how does routing work here?",
		"how does the verify api work here",
		"what breaks if i change dispatch",
		"why is the build failing?",
		"where is the memory store",
		"which package owns dispatch",
		"who calls NewServer",
		"show me the architecture",
		"explain the dispatch module",
		"tell me about the index",
	}
	for _, q := range questions {
		if !metaroute.IsQuestionShape(q) {
			t.Errorf("IsQuestionShape(%q) = false, want true", q)
		}
	}
	imperatives := []string{
		"run a full verification of this repo",
		"verify this",
		"find the NewServer function",
		"rebuild the index",
		"fix the build",
		"list the mcp tools",
		"compress this log: foo",
	}
	for _, q := range imperatives {
		if metaroute.IsQuestionShape(q) {
			t.Errorf("IsQuestionShape(%q) = true, want false", q)
		}
	}
}

// TestQuestionSearchQuery pins the stopword reduction: significant terms only,
// deduped, capped, deterministic — no LLM.
func TestQuestionSearchQuery(t *testing.T) {
	cases := []struct {
		request string
		want    string
	}{
		{"how does the verify API work here?", "verify"},
		{"how does routing work here?", "routing"},
		{"how does dispatch work?", "dispatch"},
		{"how does the index store work?", "index store"},
		{"what breaks if I change dispatch", "breaks dispatch"},
		{"why is the build failing?", "build failing"},
		{"how does it work?", "how does it work?"}, // all-stopword → raw fallback
	}
	for _, tc := range cases {
		if got := metaroute.QuestionSearchQuery(tc.request); got != tc.want {
			t.Errorf("QuestionSearchQuery(%q) = %q, want %q", tc.request, got, tc.want)
		}
	}
}
