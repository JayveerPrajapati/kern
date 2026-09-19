package explain

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

func TestExplain(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "0")
	root := testfixture.Repo(t)

	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build failed: %v", err)
	}

	res, err := Explain(ix, "Server")
	if err != nil {
		t.Fatalf("Explain failed: %v", err)
	}

	if !strings.Contains(res, "Server") {
		t.Errorf("expected Server in narrative, got: %s", res)
	}

	if _, err := Explain(ix, ""); err == nil {
		t.Error("expected error on empty subject")
	}
}
