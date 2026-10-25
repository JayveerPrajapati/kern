package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AllowOnceEntry is one operator-granted, single-use allowance for a
// guarded subject (a file path, a search pattern, or a command string).
// D4-D (2026-10-08): the read/grep/bash deny surfaces keep their blocks,
// but the deny message names the subject hash — `kern guard allow <hash>`
// (tty-gated: only a human at an interactive terminal can mint an entry)
// records the allowance here, and the denying surface consumes it on the
// NEXT identical subject; after that the entry is inert. The registry is
// user-level so the opencode plugin, the shell guard, and the CLI agree
// regardless of project root. Consume races are accepted: worst case two
// concurrent retries both pass, and both are visible in the audit trail.
type AllowOnceEntry struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject,omitempty"`
	Created string `json:"created"`
	// Used is "" while unused (explicit, not omitempty: the shell guard and
	// the plugin match '"used":""' textually) and the consumption RFC3339
	// timestamp after the single use.
	Used string `json:"used"`
}

// HashSubject computes the canonical subject hash (lowercase sha256 hex).
// The subject is the exact string the denying surface names in its deny
// message, so the operator never computes anything: the deny shows the
// ready-made `kern guard allow <hash>` line.
func HashSubject(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// AllowOncePath is the registry location (KERN_GUARD_ALLOW_ONCE overrides,
// for tests and probes).
func AllowOncePath() (string, error) {
	if p := os.Getenv("KERN_GUARD_ALLOW_ONCE"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kern", "guard", "allow-once.jsonl"), nil
}

// AppendAllowOnce records a new operator-granted allowance.
func AppendAllowOnce(hash, subject string) error {
	if len(hash) != 64 {
		return fmt.Errorf("governance: allow-once hash must be 64 hex chars, got %d", len(hash))
	}
	p, err := AllowOncePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	e := AllowOnceEntry{Hash: strings.ToLower(hash), Subject: subject, Created: time.Now().UTC().Format(time.RFC3339)}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// ConsumeAllowOnce reports whether subjectHash has an UNUSED entry,
// marking it used when found. A second identical subject finds only used
// entries and is denied again — the allowance is single-use.
//
// The read→mark→rewrite window is serialized by a blocking flock on the
// registry (a `.lock` sidecar), so two concurrent consumers of the SAME
// unused entry cannot both pass (M7: this used to be a lock-free
// read-modify-rename — both goroutines could read the unused entry, both
// mark it used, and both succeed, defeating single-use). The lock is
// blocking because an allowance grant is rare and a grant/consume race
// should resolve by waiting, not by failing the bypass.
func ConsumeAllowOnce(subject string) bool {
	h := HashSubject(subject)
	p, err := AllowOncePath()
	if err != nil {
		return false
	}
	// Exclusive lock across read→decide→rename: a concurrent consumer
	// blocks here until this one finishes, then re-reads and sees the
	// Used mark. Lock failure fails closed (no consume). The helper is
	// platform-specific (blocking flock on Unix, exclusive-create with a
	// bounded timeout on Windows) so the single-use guarantee holds on
	// every OS kern supports.
	unlock, err := lockAllowOnceRegistry(p)
	if err != nil {
		return false
	}
	defer unlock()

	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var kept []string
	consumed := false
	now := time.Now().UTC().Format(time.RFC3339)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e AllowOnceEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			kept = append(kept, line) // foreign line: preserve, never touch
			continue
		}
		if !consumed && e.Used == "" && strings.EqualFold(e.Hash, h) {
			e.Used = now
			consumed = true
		}
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		kept = append(kept, string(b))
	}
	if !consumed {
		return false
	}
	// Rewrite via temp+rename so a concurrent reader never sees a torn file.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		return false
	}
	return os.Rename(tmp, p) == nil
}
