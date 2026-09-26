package envelope

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

// newFixtureHooks builds Hooks backed by a real Platform over the tiny
// testfixture repo — the same offline fixture the internal/mcp handler
// suite uses — so the happy paths exercise the real Analyze pipeline.
func newFixtureHooks(t *testing.T) (Hooks, string) {
	t.Helper()
	t.Setenv("KERN_PRELOAD", "0")
	root := testfixture.Repo(t)
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	return Hooks{
		PlatformFor: func(_ context.Context, _ string) (*app.Platform, error) {
			return p, nil
		},
	}, root
}

// decodeEnvelope unmarshals the envelope output and fails the test when it
// is not valid JSON.
func decodeEnvelope(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("envelope output is not valid JSON: %v\n%s", err, out)
	}
	return m
}

func TestContextEnvelopeHappyPath(t *testing.T) {
	h, root := newFixtureHooks(t)
	out, err := ContextEnvelope(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer",
	})
	if err != nil {
		t.Fatalf("ContextEnvelope: %v", err)
	}
	m := decodeEnvelope(t, out)
	if m["envelope_version"] != float64(1) {
		t.Errorf("envelope_version = %v, want 1", m["envelope_version"])
	}
	if m["schema_version"] != "1.0.0" {
		t.Errorf("schema_version = %v, want 1.0.0", m["schema_version"])
	}
	// TokenCount/FittedText carry no json tags, so they marshal with their
	// Go field names (TokenCount, FittedText), unlike envelope_version.
	if tc, _ := m["TokenCount"].(float64); tc <= 0 {
		t.Errorf("TokenCount = %v, want > 0", m["TokenCount"])
	}
}

func TestContextEnvelopeDefaultRoot(t *testing.T) {
	spyErr := errors.New("platform unavailable")
	var gotRoot string
	h := Hooks{
		PlatformFor: func(_ context.Context, root string) (*app.Platform, error) {
			gotRoot = root
			return nil, spyErr
		},
	}
	_, err := ContextEnvelope(context.Background(), h, map[string]any{"change": "NewServer"})
	if !errors.Is(err, spyErr) {
		t.Fatalf("err = %v, want spy error", err)
	}
	if gotRoot != "." {
		t.Fatalf("default root = %q, want \".\"", gotRoot)
	}
}

func TestContextEnvelopeChangeRequired(t *testing.T) {
	h, root := newFixtureHooks(t)
	if _, err := ContextEnvelope(context.Background(), h, map[string]any{"root": root}); err == nil {
		t.Fatal("expected error for missing change")
	} else if !strings.Contains(err.Error(), "change is required") {
		t.Fatalf("err = %v, want 'change is required'", err)
	}
	// Zero-value Hooks: the missing-change check must fire before any hook call.
	if _, err := ContextEnvelope(context.Background(), Hooks{}, nil); err == nil {
		t.Fatal("expected error for missing change with nil args")
	}
}

func TestContextEnvelopePlatformError(t *testing.T) {
	spyErr := errors.New("index build failed")
	h := Hooks{
		PlatformFor: func(_ context.Context, _ string) (*app.Platform, error) {
			return nil, spyErr
		},
	}
	_, err := ContextEnvelope(context.Background(), h, map[string]any{"change": "NewServer"})
	if !errors.Is(err, spyErr) {
		t.Fatalf("err = %v, want spy error", err)
	}
}

func TestContextEnvelopeAnalyzeError(t *testing.T) {
	h, root := newFixtureHooks(t)
	if _, err := ContextEnvelope(context.Background(), h, map[string]any{
		"root": root, "change": "DefinitelyNotASymbolZZZ",
	}); err == nil {
		t.Fatal("expected error for unresolvable change")
	}
}

func TestContextEnvelopeMaxTokens(t *testing.T) {
	h, root := newFixtureHooks(t)
	out, err := ContextEnvelope(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer", "max_tokens": "4096",
	})
	if err != nil {
		t.Fatalf("ContextEnvelope(max_tokens): %v", err)
	}
	m := decodeEnvelope(t, out)
	if ft, _ := m["FittedText"].(string); ft == "" {
		t.Errorf("FittedText missing or empty after budgeting")
	}
	if tc, _ := m["TokenCount"].(float64); tc <= 0 {
		t.Errorf("TokenCount = %v, want > 0 after budgeting", m["TokenCount"])
	}
}

func TestContextEnvelopeBadMaxTokens(t *testing.T) {
	h, root := newFixtureHooks(t)
	if _, err := ContextEnvelope(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer", "max_tokens": "many",
	}); err == nil {
		t.Fatal("expected error for malformed max_tokens")
	}
}

func TestContextEnvelopeFreshnessAppended(t *testing.T) {
	h, root := newFixtureHooks(t)
	h.LoadIndex = func(_ context.Context, _ string) (*index.Index, error) {
		return &index.Index{Root: root}, nil
	}
	h.FreshnessFooter = func(_ map[string]any, _ *index.Index) string {
		return "\n// freshness: test-proof"
	}
	out, err := ContextEnvelope(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer", "with_freshness": true,
	})
	if err != nil {
		t.Fatalf("ContextEnvelope(with_freshness): %v", err)
	}
	if !strings.HasPrefix(out, "{") {
		t.Errorf("output must start with the JSON envelope, got: %s", out)
	}
	if !strings.Contains(out, "// freshness: test-proof") {
		t.Errorf("freshness footer not appended, got: %s", out)
	}
}

// TestContextEnvelopeFreshnessLoadError pins the best-effort contract: a
// load failure never fails the envelope call and appends no footer.
func TestContextEnvelopeFreshnessLoadError(t *testing.T) {
	h, root := newFixtureHooks(t)
	h.LoadIndex = func(_ context.Context, _ string) (*index.Index, error) {
		return nil, errors.New("index load failed")
	}
	h.FreshnessFooter = func(_ map[string]any, _ *index.Index) string {
		return "\n// freshness: should-not-appear"
	}
	out, err := ContextEnvelope(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer", "with_freshness": true,
	})
	if err != nil {
		t.Fatalf("ContextEnvelope must not fail on index load error: %v", err)
	}
	if strings.Contains(out, "should-not-appear") {
		t.Errorf("footer appended despite load error, got: %s", out)
	}
	// The output is still valid JSON (no footer appended).
	decodeEnvelope(t, out)
}

func TestContextEnvelopeFreshnessOptOut(t *testing.T) {
	h, root := newFixtureHooks(t)
	h.LoadIndex = func(_ context.Context, _ string) (*index.Index, error) {
		return &index.Index{Root: root}, nil
	}
	h.FreshnessFooter = func(_ map[string]any, _ *index.Index) string {
		return "\n// freshness: should-not-appear"
	}
	out, err := ContextEnvelope(context.Background(), h, map[string]any{
		"root": root, "change": "NewServer",
	})
	if err != nil {
		t.Fatalf("ContextEnvelope: %v", err)
	}
	if strings.Contains(out, "should-not-appear") {
		t.Errorf("footer appended without with_freshness, got: %s", out)
	}
	decodeEnvelope(t, out)
}
