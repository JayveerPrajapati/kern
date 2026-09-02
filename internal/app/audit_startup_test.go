package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything written to stderr during the call.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	fn()
	// Close the write end so the read end sees EOF, then restore stderr.
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	os.Stderr = old
	return string(out)
}

// auditTestRoot builds a tiny module root so the startup path (New -> index
// build) is cheap and deterministic for audit-chain tests.
func auditTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module auditstartup\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "svc.go"), "package auditstartup\n\nfunc Main() {}\n")
	return root
}

// recordAuditEntries persists two entries through a platform's audit log, the
// way a running firewall would.
func recordAuditEntries(t *testing.T, p *Platform) {
	t.Helper()
	log := p.Firewall().AuditLog()
	log.Record(governance.AuditEntry{AgentID: "agent-a", Action: "write", Resource: "source", Result: "allowed"})
	log.Record(governance.AuditEntry{AgentID: "agent-b", Action: "write", Resource: "source", Result: "blocked"})
	if !log.VerifyChain() {
		t.Fatal("chain should be intact after recording")
	}
}

// tamperFirstAuditEntry rewrites the persisted first audit entry so its
// AgentID is "evil-agent". It handles both on-disk formats: the legacy
// per-key <key>.json files and the chain.jsonl lines of LogStore.
func tamperFirstAuditEntry(t *testing.T, root string) {
	t.Helper()
	auditDir := filepath.Join(root, ".kern", "audit")

	// Legacy format: one JSON document per key.
	first := filepath.Join(auditDir, "audit-audit-1.json")
	if data, err := os.ReadFile(first); err == nil {
		var e governance.AuditEntry
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatalf("unmarshal persisted entry: %v", err)
		}
		e.AgentID = "evil-agent"
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal tampered entry: %v", err)
		}
		if err := os.WriteFile(first, raw, 0o600); err != nil {
			t.Fatalf("tamper write: %v", err)
		}
		return
	}

	// Chain format: rewrite the first chain.jsonl line whose key matches.
	chain := filepath.Join(auditDir, "chain.jsonl")
	data, err := os.ReadFile(chain)
	if err != nil {
		t.Fatalf("read persisted entry (legacy or chain): %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	tampered := false
	for i, line := range lines {
		var rec struct {
			K string          `json:"k"`
			V json.RawMessage `json:"v"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.K != "audit-audit-1" {
			continue
		}
		var e governance.AuditEntry
		if err := json.Unmarshal(rec.V, &e); err != nil {
			t.Fatalf("unmarshal persisted entry: %v", err)
		}
		e.AgentID = "evil-agent"
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal tampered entry: %v", err)
		}
		rec.V = raw
		out, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal tampered line: %v", err)
		}
		lines[i] = string(out)
		tampered = true
		break
	}
	if !tampered {
		t.Fatal("persisted entry audit-audit-1 not found in legacy files or chain.jsonl")
	}
	if err := os.WriteFile(chain, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
}

// TestAuditChainVerifiedAtStartup ensures the server startup path replays the
// persisted governance audit log and warns loudly — without halting startup —
// when the tamper-evident chain is broken, and stays silent when the chain is
// intact.
func TestAuditChainVerifiedAtStartup(t *testing.T) {
	t.Run("tampered_chain_warns_and_starts", func(t *testing.T) {
		root := auditTestRoot(t)
		p1, err := New(root)
		if err != nil {
			t.Fatalf("first New: %v", err)
		}
		recordAuditEntries(t, p1)

		// Tamper with the persisted first entry (the file itself), whichever
		// on-disk format the store uses.
		tamperFirstAuditEntry(t, root)

		stderr := captureStderr(t, func() {
			// Startup must NOT fatal on a broken chain.
			p2, err := New(root)
			if err != nil {
				t.Fatalf("second New: %v", err)
			}
			if p2 == nil {
				t.Fatal("second New returned a nil platform")
			}
		})
		if !strings.Contains(stderr, "WARNING") || !strings.Contains(stderr, "tamper chain") {
			t.Fatalf("expected a loud tamper-chain warning on stderr, got: %q", stderr)
		}
	})

	t.Run("intact_chain_stays_silent", func(t *testing.T) {
		root := auditTestRoot(t)
		p1, err := New(root)
		if err != nil {
			t.Fatalf("first New: %v", err)
		}
		recordAuditEntries(t, p1)

		stderr := captureStderr(t, func() {
			if _, err := New(root); err != nil {
				t.Fatalf("second New: %v", err)
			}
		})
		if strings.Contains(stderr, "WARNING") {
			t.Fatalf("intact chain must not warn, got: %q", stderr)
		}
	})
}
