package preedit

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

func TestAnalyze(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0")
	root := testfixture.Repo(t)

	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build failed: %v", err)
	}

	ctx := context.Background()

	// 1. By file
	res, err := Analyze(ctx, ix, root, "web/server.go", "", "")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if !strings.Contains(res, "PRE-EDIT PREDICTIVE IMPACT REPORT") {
		t.Errorf("missing header in response: %s", res)
	}
	if !strings.Contains(res, "Direct Callers") {
		t.Errorf("missing Direct Callers section: %s", res)
	}

	// 2. By symbol
	resSym, err := Analyze(ctx, ix, root, "", "NewServer", "")
	if err != nil {
		t.Fatalf("Analyze symbol error: %v", err)
	}
	if !strings.Contains(resSym, "Target Symbol: NewServer") {
		t.Errorf("missing target symbol line: %s", resSym)
	}

	// 3. Error on empty input
	_, errEmpty := Analyze(ctx, &index.Index{}, root, "", "", "")
	if errEmpty == nil {
		t.Error("expected error when both file and symbol are empty")
	}
}
