package approvalkeycli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

// TestRunUsageErrors locks the exit contract: usage problems are exit 2 and
// never touch key material (rotate without --yes must be refused BEFORE any
// rename, or a mistyped command would silently orphan every record).
func TestRunUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"unknown subcommand", []string{"bogus"}},
		{"unknown flag", []string{"status", "--nope"}},
		{"rotate without --yes", []string{"rotate"}},
		{"rotate with unknown flag", []string{"rotate", "--yes", "--nope"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Run(c.args, Hooks{}); got != 2 {
				t.Fatalf("Run(%v) exit = %d, want 2", c.args, got)
			}
		})
	}
}

// stubIntegrity replaces the integrity verdict per approval id, so the clear
// policy is exercised without forging key material.
func stubIntegrity(t *testing.T, byID map[string]governance.IntegrityVerdict) {
	t.Helper()
	orig := approvalIntegrity
	approvalIntegrity = func(a domain.Approval, root string) governance.IntegrityReport {
		if v, ok := byID[a.ID]; ok {
			return governance.IntegrityReport{Verdict: v, Reason: "stub " + string(v)}
		}
		return governance.IntegrityReport{Verdict: governance.IntegrityOK}
	}
	t.Cleanup(func() { approvalIntegrity = orig })
}

func pendingIDs(t *testing.T, root string) string {
	t.Helper()
	pending, err := governance.NewFileStore(root).Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	out := make([]string, 0, len(pending))
	for _, a := range pending {
		out = append(out, a.ID)
	}
	return strings.Join(out, ",")
}

func seedPending(t *testing.T, root, id string) {
	t.Helper()
	if err := governance.NewFileStore(root).AddPending(domain.Approval{ID: id, TaskID: "t", Status: "pending"}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
}

func TestClearStaleOnlyClearsKeyRotated(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"stale-1", "tampered-1", "clean-1"} {
		seedPending(t, root, id)
	}
	stubIntegrity(t, map[string]governance.IntegrityVerdict{
		"stale-1":    governance.IntegrityKeyRotated,
		"tampered-1": governance.IntegrityTampered,
	})

	var code int
	out := captureStdout(t, func() { code = ClearStale(root, "tester", "", true) })
	if code != 0 {
		t.Fatalf("ClearStale exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "cleared: stale-1") || !strings.Contains(out, "cleared 1 stale approval(s)") {
		t.Errorf("output missing the cleared record:\n%s", out)
	}
	left := pendingIDs(t, root)
	if strings.Contains(left, "stale-1") {
		t.Errorf("key-rotated record survived: %s", left)
	}
	if !strings.Contains(left, "tampered-1") || !strings.Contains(left, "clean-1") {
		t.Errorf("ClearStale touched records it must not (%s)", left)
	}
}

func TestClearStaleRequiresConfirmation(t *testing.T) {
	root := t.TempDir()
	seedPending(t, root, "stale-1")
	stubIntegrity(t, map[string]governance.IntegrityVerdict{"stale-1": governance.IntegrityKeyRotated})

	if code := ClearStale(root, "", "", false); code != 2 {
		t.Fatalf("unconfirmed ClearStale exit = %d, want 2", code)
	}
	if !strings.Contains(pendingIDs(t, root), "stale-1") {
		t.Error("unconfirmed ClearStale removed a record")
	}
}

func TestClearStaleNoStaleRecords(t *testing.T) {
	root := t.TempDir()
	seedPending(t, root, "clean-1")
	stubIntegrity(t, nil)

	var code int
	out := captureStdout(t, func() { code = ClearStale(root, "", "", true) })
	if code != 0 || !strings.Contains(out, "no stale") {
		t.Fatalf("exit = %d, want 0 with a no-stale notice\n%s", code, out)
	}
}

func TestStatusRendersKeysAndCounts(t *testing.T) {
	root := t.TempDir()
	if err := governance.NewFileStore(root).AddPending(domain.Approval{ID: "apr-plain", Status: "pending"}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
	var buf bytes.Buffer
	if code := Run([]string{"status", "--root", root}, Hooks{Out: &buf}); code != 0 {
		t.Fatalf("status exit = %d\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"signing — GRANT AUTHORITY",
		"mac — TAMPER DETECTION",
		"path: ",
		"records: 1 ok, 0 key-rotated, 0 tampered, 0 signature-unverifiable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
	// The MAC key must never be presented as a grant authority.
	if strings.Contains(out, "mac — GRANT") {
		t.Error("status mislabels the MAC key as a grant authority")
	}
}
