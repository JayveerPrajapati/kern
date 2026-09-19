package optimize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

func TestFewShotInjectsBaselines(t *testing.T) {
	// Isolate memory + cache under a temp XDG dir.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = memory.Add(root, "the fastapi session signs the bearer token into a cookie")

	// Put a go.mod so memory is keyed on this exact root (no discovery needed).
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module t\n"), 0o644)

	res, err := Prompt("how does the fastapi session store the bearer token?", "", Options{
		FewShot: true,
		Root:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "baseline") {
		t.Fatalf("expected baseline markers in output, got %q", res.Output)
	}

	// Without FewShot no baseline section is added.
	res2, err := Prompt("how does the fastapi session store the bearer token?", "", Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res2.Output, "baseline") {
		t.Fatalf("did not expect baseline without FewShot, got %q", res2.Output)
	}
}

func TestFewShotNoMemory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	res, err := Prompt("some unrelated fresh topic here", "", Options{FewShot: true, Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "baseline") {
		t.Fatalf("no lessons to recall; expected no baseline, got %q", res.Output)
	}
}

// TestPromptCacheMaskBeforeKeyAndPreview: with Mask on, the raw prompt is
// masked BEFORE it enters the exact-cache key computation and the semantic
// cache Store/Lookup — so the key JSON and the on-disk semantic preview never
// carry the raw secret.
func TestPromptCacheMaskBeforeKeyAndPreview(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	secret := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890ABCDEF"
	prompt := "deploy using token " + secret + " please"

	first, err := Prompt(prompt, "", Options{Cache: true, Mask: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache {
		t.Fatal("first call must not be served from cache")
	}

	// Identical repeat: masking is deterministic, so the masked-derived key is
	// stable and the exact cache serves it.
	second, err := Prompt(prompt, "", Options{Cache: true, Mask: true})
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache {
		t.Fatal("identical masked call must hit the exact cache")
	}

	// The on-disk semantic preview (the stored 2KB input per entry) must not
	// contain the raw secret, and must contain the masked placeholder.
	idxPath := filepath.Join(cache.Dir(), "data", "sem", "prompt-index.json")
	raw, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("semantic preview on disk contains the raw secret")
	}
	if !strings.Contains(string(raw), "[MASKED_") {
		t.Fatal("semantic preview on disk must contain a masked placeholder")
	}
}

func TestPromptMaskProviderTokens(t *testing.T) {
	// optimize --mask routes through the shared pii.Mask: provider tokens are
	// replaced with placeholders before the prompt is compressed/sent, then
	// restored in the deterministic output. This is the optimize-level
	// passthrough check — the per-shape masking assertions live in the pii
	// package (TestMaskProviderTokens).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prompt := "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890ABCDEF " +
		"gh ghp_abcdefghijklmnopqrstuvwxyz1234567890 " +
		"gai AIzaSyabcdefghijklmnopqrstuvwxyz1234567 " +
		"openai sk-abcdefghijklmnopqrstuvwxyz123456"
	res, err := Prompt(prompt, "", Options{Mask: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "[MASKED_") {
		t.Fatalf("placeholders must be restored after unmask, got %q", res.Output)
	}
	for _, tok := range []string{
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890ABCDEF",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"AIzaSyabcdefghijklmnopqrstuvwxyz1234567",
		"sk-abcdefghijklmnopqrstuvwxyz123456",
	} {
		if !strings.Contains(res.Output, tok) {
			t.Fatalf("provider token %q lost in mask round-trip: %q", tok, res.Output)
		}
	}
}

func TestLogWithKernYAMLProfile(t *testing.T) {
	tempDir := t.TempDir()
	kernDir := filepath.Join(tempDir, ".kern")
	if err := os.MkdirAll(kernDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	yamlContent := `
profiles:
  test-profile:
    truncate_rules:
      - match: "console.log"
        action: strip_completely
      - match: "ValidationError:"
        keep_lines_before: 1
        keep_lines_after: 1
`
	if err := os.WriteFile(filepath.Join(kernDir, "kern.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write kern.yaml failed: %v", err)
	}

	log := strings.Join([]string{
		"2024-01-01 INFO uninteresting chatter 1",
		"2024-01-01 INFO noise: console.log should be removed",
		"2024-01-01 INFO request context id=999",
		"2024-01-01 ValidationError: bad request",
		"2024-01-01 INFO detail: field missing",
		"2024-01-01 INFO uninteresting chatter 2",
	}, "\n")

	res, err := Log(log, Options{
		Root:    tempDir,
		Profile: "test-profile",
	})
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	if strings.Contains(res.Output, "console.log") {
		t.Errorf("expected console.log to be stripped completely, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "ValidationError: bad request") {
		t.Errorf("expected ValidationError to be preserved, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "request context id=999") {
		t.Errorf("expected request context before to be preserved, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "detail: field missing") {
		t.Errorf("expected detail after to be preserved, got:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "uninteresting chatter") {
		t.Errorf("expected uninteresting chatter to be omitted, got:\n%s", res.Output)
	}
}
