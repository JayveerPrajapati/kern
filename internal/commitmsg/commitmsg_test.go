package commitmsg

import (
	"strings"
	"testing"
)

func TestGenerateFeatWithScope(t *testing.T) {
	diff := `diff --git a/api/login.go b/api/login.go
index abc..def 100644
--- a/api/login.go
+++ b/api/login.go
@@ -10,3 +10,7 @@
 func loginHandler() {
+	authenticate(req)
+	return session
 }
diff --git a/api/signup.go b/api/signup.go
index 111..222 100644
--- a/api/signup.go
+++ b/api/signup.go
@@ -5,2 +5,2 @@
-oldBody
+register(req) // add signup support
`
	m := Generate(diff)
	if m.Type != "feat" {
		t.Errorf("type = %q, want feat", m.Type)
	}
	if m.Scope != "api" {
		t.Errorf("scope = %q, want api", m.Scope)
	}
	if !strings.HasPrefix(m.Subject, "feat(api):") || !strings.Contains(m.Subject, "authenticate") {
		t.Errorf("subject = %q", m.Subject)
	}
	if len(m.Body) != 2 {
		t.Fatalf("expected 2 body bullets, got %v", m.Body)
	}
	if !strings.Contains(m.Body[0], "api/login.go (2+,0-)") {
		t.Errorf("body bullet = %q", m.Body[0])
	}
	if !strings.Contains(m.String(), m.Subject) {
		t.Error("String() must include the subject")
	}
}

func TestGenerateFixBeatsFeat(t *testing.T) {
	diff := `diff --git a/cmd/kern/main.go b/cmd/kern/main.go
--- a/cmd/kern/main.go
+++ b/cmd/kern/main.go
@@ -1,2 +1,2 @@
-return nil
+return fmt.Errorf("fix the crash: %w", err)
`
	m := Generate(diff)
	if m.Type != "fix" {
		t.Errorf("type = %q, want fix (priority over feat keyword)", m.Type)
	}
}

func TestGenerateTestChange(t *testing.T) {
	diff := `diff --git a/pkg/auth/auth_test.go b/pkg/auth/auth_test.go
--- a/pkg/auth/auth_test.go
+++ b/pkg/auth/auth_test.go
@@ -1,2 +1,2 @@
+assert.True(t, ok)
`
	m := Generate(diff)
	if m.Type != "test" {
		t.Errorf("type = %q, want test", m.Type)
	}
	if m.Scope != "auth" {
		t.Errorf("scope = %q, want auth (generic pkg stripped)", m.Scope)
	}
}

func TestGenerateDocs(t *testing.T) {
	diff := `diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,2 +1,2 @@
+document the new install flow here
`
	m := Generate(diff)
	if m.Type != "docs" {
		t.Errorf("type = %q, want docs", m.Type)
	}
}

func TestGenerateRename(t *testing.T) {
	diff := `diff --git a/old.go b/new.go
similarity index 95%
rename from old.go
rename to new.go
`
	m := Generate(diff)
	if len(m.Body) != 1 || !strings.Contains(m.Body[0], "new.go") || !strings.Contains(m.Body[0], "renamed") {
		t.Errorf("body = %v, want renamed new.go", m.Body)
	}
}

func TestGenerateQuotedPaths(t *testing.T) {
	diff := `diff --git "a/my file.go" "b/my file.go"
index abc..def 100644
--- "a/my file.go"
+++ "b/my file.go"
@@ -1,2 +1,2 @@
+authenticate(req)
diff --git "a/dir/with space/file.ts" "b/dir/with space/file.ts"
index 111..222 100644
--- "a/dir/with space/file.ts"
+++ "b/dir/with space/file.ts"
@@ -1 +1 @@
+export const x = 1
`
	m := Generate(diff)
	if len(m.Body) != 2 {
		t.Fatalf("expected 2 body bullets, got %v", m.Body)
	}
	if !strings.Contains(m.Body[0], "my file.go (1+,0-)") {
		t.Errorf("body bullet = %q, want quoted path parsed cleanly", m.Body[0])
	}
	if !strings.Contains(m.Body[1], "dir/with space/file.ts (1+,0-)") {
		t.Errorf("body bullet = %q, want quoted path parsed cleanly", m.Body[1])
	}
}

func TestGenerateQuotedRename(t *testing.T) {
	diff := `diff --git "a/old file.go" "b/new file.go"
similarity index 95%
rename from old file.go
rename to new file.go
`
	m := Generate(diff)
	if len(m.Body) != 1 || !strings.Contains(m.Body[0], "new file.go") || !strings.Contains(m.Body[0], "renamed") {
		t.Errorf("body = %v, want renamed new file.go", m.Body)
	}
}

func TestGenerateDeclarationExportedIsFeat(t *testing.T) {
	// A new exported declaration is a feature even when the surrounding added
	// body line carries fix keywords ("error"), which used to over-match.
	diff := `diff --git a/api/handler.go b/api/handler.go
--- a/api/handler.go
+++ b/api/handler.go
@@ -1,2 +1,6 @@
 func loginHandler() {
+func (h *Handler) Login() {
+	return h.session, fmt.Errorf("guard the error path")
+}
 	return h.session
 }
`
	m := Generate(diff)
	if m.Type != "feat" {
		t.Errorf("type = %q, want feat (exported declaration beats body fix keywords)", m.Type)
	}
	if !strings.HasPrefix(m.Subject, "feat(") {
		t.Errorf("subject = %q, want feat(...)", m.Subject)
	}
	if !strings.Contains(m.Subject, "login") {
		t.Errorf("subject = %q, want it named after the added declaration", m.Subject)
	}
}

func TestGenerateDeclarationNaming(t *testing.T) {
	// The subject must name the added top-level declaration, not a random
	// non-stopword from an added body line.
	diff := `diff --git a/internal/app/intent.go b/internal/app/intent.go
--- a/internal/app/intent.go
+++ b/internal/app/intent.go
@@ -1,3 +1,4 @@
 func CompileIntent(raw string) {
+func environmentFor(it string) string {
+	return "production"
+}
 	return raw
 }
`
	m := Generate(diff)
	if m.Type != "feat" {
		t.Errorf("type = %q, want feat (additive new symbol)", m.Type)
	}
	if !strings.Contains(m.Subject, "environmentfor") {
		t.Errorf("subject = %q, want it named after the added declaration", m.Subject)
	}
}

func TestGenerateNoDeclarationKeepsKeywordScoring(t *testing.T) {
	// Without any top-level declaration, the legacy fix-keyword scoring must
	// still win (fix over feat) so existing behavior is preserved.
	diff := `diff --git a/cmd/kern/main.go b/cmd/kern/main.go
--- a/cmd/kern/main.go
+++ b/cmd/kern/main.go
@@ -1,2 +1,2 @@
-return nil
+return fmt.Errorf("fix the crash: %w", err)
`
	m := Generate(diff)
	if m.Type != "fix" {
		t.Errorf("type = %q, want fix (no declarations → body keyword scoring)", m.Type)
	}
}

func TestGenerateDeterministic(t *testing.T) {
	diff := `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1 +1 @@
+handle(n)
`
	a := Generate(diff)
	b := Generate(diff)
	if a.String() != b.String() {
		t.Error("same diff must produce identical messages")
	}
}

func TestGenerateEmptyDiff(t *testing.T) {
	m := Generate("")
	if m.Type != "chore" || !strings.HasPrefix(m.Subject, "chore") {
		t.Errorf("empty diff -> %q / %q", m.Type, m.Subject)
	}
}
