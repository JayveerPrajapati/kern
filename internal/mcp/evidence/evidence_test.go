package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidenceMissingSource(t *testing.T) {
	ctx := context.Background()
	_, err := Handle(ctx, Hooks{}, map[string]any{
		"action": "verify",
	})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected error on missing file/url, got: %v", err)
	}
}

// TestEvidenceVerifyRefusesCredentialFile pins F5: a credential-shaped file
// argument must be refused before the bundle read serves its contents.
func TestEvidenceVerifyRefusesCredentialFile(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "bundle.pem")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY MATERIAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Handle(context.Background(), Hooks{}, map[string]any{
		"action": "verify",
		"file":   secret,
	})
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("expected credential refusal, got: %v", err)
	}
}
