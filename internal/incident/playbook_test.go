package incident

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// TestPlaybookStoreRoundTrip exercises Add → List → Find for the heal-playbook
// store (Feature Batch D): a playbook added for a signature is listed and
// found by an incident whose deterministic signature matches.
func TestPlaybookStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	inc := domain.Incident{Alert: domain.Alert{Service: "checkout", Message: "checkout failures"}}
	sig := SignatureForIncident(inc)
	pb := Playbook{
		Signature: sig,
		Steps:     []string{"restart checkout pods", "verify queue depth", "rollback v1.2.0"},
		Source:    "oncall",
	}
	if err := AddPlaybook(root, pb); err != nil {
		t.Fatalf("AddPlaybook: %v", err)
	}

	list, err := ListPlaybooks(root)
	if err != nil {
		t.Fatalf("ListPlaybooks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list length = %d, want 1", len(list))
	}
	if list[0].Signature != pb.Signature || len(list[0].Steps) != 3 {
		t.Errorf("stored playbook = %+v, want signature %s with 3 steps", list[0], pb.Signature)
	}
	if list[0].TS.IsZero() {
		t.Error("playbook TS not stamped")
	}

	// Find by an incident whose signature matches (service scope + error class).
	if got, ok := FindPlaybook(root, inc); !ok {
		t.Fatal("FindPlaybook: no match for matching incident")
	} else if got.Signature != pb.Signature {
		t.Errorf("found signature = %q, want %q", got.Signature, pb.Signature)
	}
}

// TestPlaybookStoreUpsertBySignature: adding a playbook with the same
// signature replaces the stored one (a signature is an incident-class key).
func TestPlaybookStoreUpsertBySignature(t *testing.T) {
	root := t.TempDir()
	_ = AddPlaybook(root, Playbook{Signature: "scope:svc:abc", Steps: []string{"old"}})
	_ = AddPlaybook(root, Playbook{Signature: "scope:svc:abc", Steps: []string{"new"}, Source: "updated"})
	list, err := ListPlaybooks(root)
	if err != nil {
		t.Fatalf("ListPlaybooks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list length = %d, want 1 (upsert by signature)", len(list))
	}
	if len(list[0].Steps) != 1 || list[0].Steps[0] != "new" {
		t.Errorf("steps = %v, want [new]", list[0].Steps)
	}
}

// TestPlaybookStoreEmptySignatureRejected: a playbook without a signature is
// an error, never silently stored.
func TestPlaybookStoreEmptySignatureRejected(t *testing.T) {
	if err := AddPlaybook(t.TempDir(), Playbook{Steps: []string{"x"}}); err == nil {
		t.Fatal("AddPlaybook with empty signature must error")
	}
}

// TestPlaybookStoreCorruptFileFreshStore: a corrupt playbooks.json must load
// as a fresh store (logged, never a panic), and a subsequent Add must repair
// the file.
func TestPlaybookStoreCorruptFileFreshStore(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "playbooks.json"), []byte("not json {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	// List on a corrupt file: fresh empty store, no panic.
	list, err := ListPlaybooks(root)
	if err != nil {
		t.Fatalf("ListPlaybooks on corrupt file: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("corrupt store listed %d playbooks, want 0", len(list))
	}
	// Add repairs the file and round-trips.
	if err := AddPlaybook(root, Playbook{Signature: "sig:abc", Steps: []string{"s"}}); err != nil {
		t.Fatalf("AddPlaybook after corruption: %v", err)
	}
	list, err = ListPlaybooks(root)
	if err != nil || len(list) != 1 {
		t.Fatalf("post-repair list = %d, err = %v, want 1", len(list), err)
	}
}

// TestPlaybookPermissions: the store file is written 0600 (same pattern as
// the incident store) so runbook content stays private to the project owner.
func TestPlaybookPermissions(t *testing.T) {
	root := t.TempDir()
	if err := AddPlaybook(root, Playbook{Signature: "sig:x", Steps: []string{"s"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, ".kern", "playbooks.json"))
	if err != nil {
		t.Fatalf("stat playbooks.json: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("playbooks.json mode = %o, want 600", perm)
	}
}

// TestSignatureDerivationPins: the deterministic signature derivation reuses
// the learning extractor's grouping signal — "scope:<service>:<error-class>"
// when the service is known, else "sig:<error-class>". Pinned so the smoke
// workflow's runbook signature matches the derived incident signature.
func TestSignatureDerivationPins(t *testing.T) {
	al := domain.Alert{Service: "internal/app", Message: "boom"}
	sig := SignatureForAlert(al)
	if !strings.HasPrefix(sig, "scope:internal/app:") {
		t.Errorf("signature = %q, want scope:internal/app:<error-class> prefix", sig)
	}
	// Same service + message → same signature; different message → different.
	al2 := domain.Alert{Service: "internal/app", Message: "boom"}
	if SignatureForAlert(al2) != sig {
		t.Error("identical alerts must derive identical signatures")
	}
	if SignatureForAlert(domain.Alert{Service: "internal/app", Message: "boom2"}) == sig {
		t.Error("different messages must derive different signatures")
	}
	// Service-less alerts fall back to the content-hash form.
	if s := SignatureForAlert(domain.Alert{Message: "boom"}); !strings.HasPrefix(s, "sig:") {
		t.Errorf("service-less signature = %q, want sig: prefix", s)
	}
	// Incident form prefers the correlated affected service.
	inc := domain.Incident{AffectedService: "internal/app", Title: "boom"}
	if SignatureForIncident(inc) != sig {
		t.Errorf("SignatureForIncident = %q, want %q", SignatureForIncident(inc), sig)
	}
}

// TestIngestAlertAutoAttachesPlaybook: when a stored playbook matches a newly
// ingested incident's signature, the incident record carries the playbook
// steps — the auto-attach insertion point (Feature Batch D).
func TestIngestAlertAutoAttachesPlaybook(t *testing.T) {
	root := fixtureRoot(t)
	alert := domain.Alert{ID: "A-1", Severity: domain.SeverityCritical, Message: "boom", Service: "internal/app", OccurredAt: time.Now()}
	sig := SignatureForAlert(alert)
	if err := AddPlaybook(root, Playbook{Signature: sig, Steps: []string{"step1", "step2"}, Source: "test"}); err != nil {
		t.Fatalf("AddPlaybook: %v", err)
	}

	eng, err := NewEngine(root, runtime.NewStore(), memory.NewMemoryStore(t.TempDir()), governance.NewFirewall())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	inc := eng.IngestAlert(alert)
	if inc.PlaybookSignature != sig {
		t.Errorf("PlaybookSignature = %q, want %q", inc.PlaybookSignature, sig)
	}
	if len(inc.PlaybookSteps) != 2 || inc.PlaybookSteps[0] != "step1" {
		t.Errorf("PlaybookSteps = %v, want [step1 step2]", inc.PlaybookSteps)
	}
	if inc.PlaybookSource != "test" {
		t.Errorf("PlaybookSource = %q, want test", inc.PlaybookSource)
	}

	// A non-matching incident gets no playbook.
	other := eng.IngestAlert(domain.Alert{ID: "A-2", Severity: domain.SeverityError, Message: "something else", Service: "internal/app", OccurredAt: time.Now()})
	if other.PlaybookSignature != "" || len(other.PlaybookSteps) != 0 {
		t.Errorf("non-matching incident carried playbook: %+v", other.PlaybookSteps)
	}
}

// TestIngestAlertNoPlaybookStore: with no stored playbooks, ingestion is a
// clean no-op (empty attach), never an error.
func TestIngestAlertNoPlaybookStore(t *testing.T) {
	eng, err := NewEngine(t.TempDir(), runtime.NewStore(), memory.NewMemoryStore(t.TempDir()), governance.NewFirewall())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	inc := eng.IngestAlert(domain.Alert{ID: "A-3", Severity: domain.SeverityWarning, Message: "wobble", Service: "svc"})
	if inc.PlaybookSignature != "" || len(inc.PlaybookSteps) != 0 {
		t.Errorf("empty store attached a playbook: %+v", inc.PlaybookSteps)
	}
}

// TestPlaybookStoreFindAndUpsertBySignature pins the exported signature-keyed
// methods the heal fast path adapts: FindBySignature returns a stored
// playbook for an exact signature and UpsertBySignature inserts or replaces
// by signature (empty signature errors).
func TestPlaybookStoreFindAndUpsertBySignature(t *testing.T) {
	root := t.TempDir()
	store := NewPlaybookStore(root)

	// Find on an empty store: miss.
	if _, ok := store.FindBySignature("sig:abc"); ok {
		t.Fatal("empty store must miss")
	}

	// Upsert insert, then find by signature.
	if err := store.UpsertBySignature("sig:abc", []string{"a.go|b2xk|bmV3"}); err != nil {
		t.Fatalf("UpsertBySignature: %v", err)
	}
	pb, ok := store.FindBySignature("sig:abc")
	if !ok || len(pb.Steps) != 1 || pb.Steps[0] != "a.go|b2xk|bmV3" {
		t.Fatalf("FindBySignature = %+v, ok=%v; want sig:abc with 1 step", pb, ok)
	}

	// Upsert replace by the same signature.
	if err := store.UpsertBySignature("sig:abc", []string{"c.go|eA==|eQ=="}); err != nil {
		t.Fatalf("UpsertBySignature: %v", err)
	}
	pb, ok = store.FindBySignature("sig:abc")
	if !ok || len(pb.Steps) != 1 || pb.Steps[0] != "c.go|eA==|eQ==" {
		t.Fatalf("upsert must replace by signature, got %+v", pb)
	}

	// A different signature is not matched.
	if _, ok := store.FindBySignature("sig:other"); ok {
		t.Fatal("different signature must miss")
	}

	// Empty signature errors (add's invariant).
	if err := store.UpsertBySignature("", []string{"x"}); err == nil {
		t.Fatal("empty signature must error")
	}
}
