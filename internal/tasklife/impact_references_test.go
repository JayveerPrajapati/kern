package tasklife

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

func writeImpactFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestImpactNonCallableSymbolsReportReferenceEvidence pins audit P0-1: the call
// graph attaches edges to functions/methods only, so a type used only in
// declarations, a const, a var, and a methodless interface all reported
// "0 callers / risk low / 0 affected files" while being referenced across the
// repo. The reference lookup must report the evidence: AST-verified references
// with a risk graded from their file count, or a labelled textual fallback
// with risk "unknown". A low risk with no reference evidence is forbidden.
func TestImpactNonCallableSymbolsReportReferenceEvidence(t *testing.T) {
	root := t.TempDir()
	writeImpactFixture(t, root, "lib/defs.go", `package lib

// FieldOnly is used only as a field type and a composite literal.
type FieldOnly struct{ N int }

// PayloadShape has no methods, so it has no call edges.
type PayloadShape interface{}

const ConstOnly = 42

var VarOnly = 7

func RealFunc() int { return 1 }

func Caller() int { return RealFunc() }
`)
	writeImpactFixture(t, root, "consumer/use.go", `package consumer

import "example/lib"

type Holder struct {
	F lib.FieldOnly
}

var _ lib.PayloadShape

func Use() int {
	x := lib.FieldOnly{N: lib.ConstOnly}
	_ = x
	return lib.VarOnly
}
`)

	p := newTestPlatform(t, root)
	ts := NewTaskService(p, eventbus.New())

	for _, name := range []string{"lib.FieldOnly", "lib.PayloadShape", "lib.ConstOnly", "lib.VarOnly"} {
		_, rep, text, err := ts.Impact(name)
		if err != nil {
			t.Fatalf("Impact(%q): %v", name, err)
		}
		if rep.ReferenceConfidence == "" {
			t.Errorf("Impact(%q) has no reference confidence", name)
		}
		if rep.ReferenceCount == 0 || len(rep.ReferenceFiles) == 0 {
			t.Errorf("Impact(%q) found no references (count=%d files=%v)", name, rep.ReferenceCount, rep.ReferenceFiles)
		}
		if strings.HasPrefix(rep.ReferenceConfidence, "ast") {
			if rep.Risk != "low" && rep.Risk != "medium" && rep.Risk != "high" {
				t.Errorf("Impact(%q) risk = %q with AST evidence, want low/medium/high", name, rep.Risk)
			}
		} else if rep.Risk != "unknown" {
			t.Errorf("Impact(%q) risk = %q with %q evidence, want unknown", name, rep.Risk, rep.ReferenceConfidence)
		}
		if !strings.Contains(text, "Risk: "+rep.Risk) {
			t.Errorf("Impact(%q) text does not report Risk: %s:\n%s", name, rep.Risk, text)
		}
		if !strings.Contains(text, "Referencing files:") || !strings.Contains(text, "Reference count:") {
			t.Errorf("Impact(%q) text omits the reference evidence:\n%s", name, text)
		}
	}

	// A genuinely call-covered function must NOT be downgraded to unknown: the
	// safety net is only for kinds the call graph cannot see.
	_, rep, _, err := ts.Impact("lib.RealFunc")
	if err != nil {
		t.Fatalf("Impact(lib.RealFunc): %v", err)
	}
	if rep.Risk == "unknown" || rep.ReferenceConfidence != "" {
		t.Errorf("Impact(lib.RealFunc) = risk %q confidence %q; a called function must keep graph risk", rep.Risk, rep.ReferenceConfidence)
	}
}
