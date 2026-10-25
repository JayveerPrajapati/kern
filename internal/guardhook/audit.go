// audit.go appends the guard's audit records to ~/.kern/audit/bypass.jsonl,
// best-effort (a failed append never changes the verdict), matching the
// script's jq -c line shapes field-for-field.
package guardhook

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

func auditPath() string {
	home := env("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".kern", "audit", "bypass.jsonl")
}

func appendAuditLine(fields map[string]string, order []string) {
	p := auditPath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	var b []byte
	b = append(b, '{')
	first := true
	for _, k := range order {
		v, ok := fields[k]
		if !ok {
			continue
		}
		if !first {
			b = append(b, ',')
		}
		first = false
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(v)
		b = append(b, kb...)
		b = append(b, ':')
		b = append(b, vb...)
	}
	b = append(b, '}', '\n')
	_, _ = f.Write(b)
}

func auditDeny(tool, subject string) {
	appendAuditLine(map[string]string{
		"ts": timestamp(), "event": "kern-guard-deny", "tool": tool,
		"subject": subject, "pwd": cwd(),
	}, []string{"ts", "event", "tool", "subject", "pwd"})
}

func auditRedirect(tool, kernCmd, subject string) {
	appendAuditLine(map[string]string{
		"ts": timestamp(), "event": "kern-guard-redirect", "tool": tool,
		"kern_cmd": kernCmd, "subject": subject, "pwd": cwd(),
	}, []string{"ts", "event", "tool", "kern_cmd", "subject", "pwd"})
}

func auditBypass(tool, reason, mode string) {
	appendAuditLine(map[string]string{
		"ts": timestamp(), "event": "kern-guard-bypass", "tool": tool,
		"reason": reason, "mode": mode, "pwd": cwd(),
	}, []string{"ts", "event", "tool", "reason", "mode", "pwd"})
}

func auditAllowOnceConsumed(subject string) {
	appendAuditLine(map[string]string{
		"ts": timestamp(), "event": "kern-guard-allow-once-consumed",
		"hash": hashSubject(subject), "pwd": cwd(),
	}, []string{"ts", "event", "hash", "pwd"})
}

// auditPathWriteAllow records the path-aware write arm granting a local
// file-shape write (rm/mv/cp/mkdir/touch) with every target in-tree.
// The allow is safe by construction (root-bounded, .git/credential paths
// excluded) but it is a mutation of the working tree, so it lands in the
// audit log alongside allow-once consumption.
func auditPathWriteAllow(cmd string) {
	appendAuditLine(map[string]string{
		"ts": timestamp(), "event": "kern-guard-path-write-allow",
		"hash": hashSubject(cmd), "pwd": cwd(),
	}, []string{"ts", "event", "hash", "pwd"})
}

// auditGateGrantUse records the Ask Protocol arm allowing a GATE-class
// command because an active grant covered it (single-use approved record
// claimed, or a live prefix grant consumed one use — the governance store
// records the consumption itself; this line ties the tool-call allow to it).
func auditGateGrantUse(cmd string) {
	appendAuditLine(map[string]string{
		"ts": timestamp(), "event": "kern-guard-gate-grant-use",
		"hash": hashSubject(cmd), "pwd": cwd(),
	}, []string{"ts", "event", "hash", "pwd"})
}

func cwd() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

func hashSubject(s string) string {
	return governance.HashSubject(s)
}
