// Package approvalkeycli implements `kern approval key status|rotate`: the
// lifecycle surface for the two machine-global approval keys. The signing key
// is the GRANT AUTHORITY (its private half is encrypted under an operator
// passphrase); the MAC key is tamper detection only and is same-user readable.
//
// The surface exists because key material was previously managed by deleting
// files found in an error message: rotation was silent, unrecorded, and every
// record stamped under the old key then read as hand-edited content.
package approvalkeycli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/JayveerPrajapati/kern/internal/approvalsign"
	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

const usage = "approval key requires a subcommand: kern approval key status [--root R] | kern approval key rotate [--signing|--mac|--both] [--yes]"

// Hooks are the surfaces this package does not own: the operator passphrase
// prompt (tty-gated by the caller, test-injectable) and the audit append (the
// caller's chain, which carries the root).
type Hooks struct {
	Actor      string
	Passphrase approvalsign.PassphraseFunc
	Audit      func(root, verb, id, actor, result, detail string)
	Out        io.Writer
}

type opts struct {
	root    string
	signing bool
	mac     bool
	both    bool
	yes     bool
}

func parse(rest []string) (opts, error) {
	var o opts
	p := cliflags.NewParser(rest)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&o.root, inline, hasInline)
		case "--signing":
			p.SetBool(&o.signing, "--signing", inline, hasInline)
		case "--mac":
			p.SetBool(&o.mac, "--mac", inline, hasInline)
		case "--both":
			p.SetBool(&o.both, "--both", inline, hasInline)
		case "--yes":
			p.SetBool(&o.yes, "--yes", inline, hasInline)
		default:
			p.Unknown(name)
		}
	}
	return o, p.Err()
}

// Run dispatches `kern approval key <status|rotate>`; exit codes follow the
// CLI contract: 0 success, 2 usage, 1 operational failure.
func Run(rest []string, h Hooks) int {
	out := h.Out
	if out == nil {
		out = os.Stdout
	}
	if len(rest) < 1 || rest[0] == "" {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	o, err := parse(rest[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "approval key: %v\n", err)
		return 2
	}
	if o.root == "" {
		o.root = "."
	}
	switch rest[0] {
	case "status":
		return status(o.root, out)
	case "rotate":
		return rotate(o, h, out)
	}
	fmt.Fprintf(os.Stderr, "approval key: unknown subcommand %q — use status or rotate\n", rest[0])
	return 2
}

// approvalIntegrity is the package seam over the governance verdict: the clear
// policy is tested by stubbing it, never by forging key material.
var approvalIntegrity = governance.ApprovalIntegrity

// ClearStale clears every pending record that cannot verify because the MAC
// key was rotated after it was created — the one integrity failure that is a
// key-lifecycle artifact rather than evidence. Tampered records are NEVER
// touched (they are evidence), and neither are decided records. It is the
// rotation's remediation step, so it lives with the key surface.
//
// Exit codes follow the CLI contract: 0 done, 2 unconfirmed, 1 failure.
func ClearStale(root, actor, reason string, confirmed bool) int {
	if actor == "" {
		actor = "cli-user"
	}
	store := governance.NewFileStore(root)
	pending, err := store.Pending()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern: approval clear --stale: %v\n", err)
		return 1
	}
	var ids []string
	for _, a := range pending {
		if approvalIntegrity(a, root).Verdict == governance.IntegrityKeyRotated {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		fmt.Println("no stale (key-rotated) pending approvals")
		return 0
	}
	if !confirmed {
		fmt.Fprintf(os.Stderr, "kern: approval clear --stale would clear %d pending record(s) that predate the current MAC key — pass --yes to confirm\n", len(ids))
		return 2
	}
	if reason == "" {
		reason = "stale: MAC key rotated after this record was created"
	}
	for _, id := range ids {
		if _, err := store.Clear(id, actor, reason); err != nil {
			fmt.Fprintf(os.Stderr, "kern: approval clear --stale: %s: %v\n", id, err)
			return 1
		}
		fmt.Printf("cleared: %s\n", id)
	}
	fmt.Printf("cleared %d stale approval(s)\n", len(ids))
	return 0
}

func signingKeyInfo() approvalsign.KeyInfo {
	p, err := governance.ExecApprovalSigningKeyPath()
	if err != nil {
		return approvalsign.KeyInfo{Problem: err.Error()}
	}
	return approvalsign.Inspect(p)
}

func status(root string, out io.Writer) int {
	sk := signingKeyInfo()
	fmt.Fprintln(out, "approval keys")
	fmt.Fprintln(out, "  signing — GRANT AUTHORITY (passphrase-encrypted; only it can approve)")
	fmt.Fprintf(out, "    path: %s\n", sk.Path)
	if !sk.Exists {
		fmt.Fprintf(out, "    state: %s\n", sk.Problem)
	} else {
		fmt.Fprintf(out, "    state: present, mode %04o, modified %s\n", sk.Mode, sk.Modified.Format(time.RFC3339))
		fmt.Fprintf(out, "    fingerprint: %s\n", sk.Fingerprint)
		if sk.Problem != "" {
			fmt.Fprintf(out, "    problem: %s\n", sk.Problem)
		}
		if w := sk.PermWarning(); w != "" {
			fmt.Fprintf(out, "    !! %s\n", w)
		}
	}
	fmt.Fprintln(out, "  mac — TAMPER DETECTION (not a grant; any same-user process can read it)")
	if mp, err := governance.ExecApprovalMACKeyPath(); err != nil {
		fmt.Fprintf(out, "    error: %v\n", err)
	} else if fi, err := os.Stat(mp); err == nil {
		fmt.Fprintf(out, "    path: %s\n    state: present, mode %04o, created %s\n", mp, fi.Mode().Perm(), fi.ModTime().Format(time.RFC3339))
	} else if os.IsNotExist(err) {
		fmt.Fprintf(out, "    path: %s\n    state: absent (created on the first governed command)\n", mp)
	} else {
		fmt.Fprintf(out, "    path: %s\n    state: unreadable: %v\n", mp, err)
	}
	counts, err := governance.ApprovalIntegrityCounts(root)
	if err != nil {
		fmt.Fprintf(out, "  records: unreadable: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "  records: %d ok, %d key-rotated, %d tampered, %d signature-unverifiable\n",
		counts[governance.IntegrityOK], counts[governance.IntegrityKeyRotated],
		counts[governance.IntegrityTampered], counts[governance.IntegritySignatureUnverifiable])
	if n := counts[governance.IntegrityKeyRotated]; n > 0 {
		fmt.Fprintf(out, "    %d record(s) predate the current MAC key — unverifiable, NOT tampering; clear with `kern approval clear --stale --yes`\n", n)
	}
	return 0
}

func rotate(o opts, h Hooks, out io.Writer) int {
	both := !o.signing && !o.mac
	if !o.yes {
		fmt.Fprintln(os.Stderr, "approval key rotate replaces key material and invalidates existing records — pass --yes: kern approval key rotate [--signing|--mac|--both] --yes")
		return 2
	}
	actor := h.Actor
	if actor == "" {
		actor = "cli-user"
	}
	if o.mac || both {
		backup, err := governance.RotateExecApprovalMACKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "approval key rotate: mac key: %v\n", err)
			return 1
		}
		if backup == "" {
			backup = "(none — no mac key existed)"
		}
		fmt.Fprintf(out, "rotated mac key\n  previous: %s\n  new: created on the next governed command\n", backup)
		if h.Audit != nil {
			h.Audit(o.root, "key-rotate", "mac", actor, "rotated", backup)
		}
		if counts, err := governance.ApprovalIntegrityCounts(o.root); err == nil && counts[governance.IntegrityKeyRotated] > 0 {
			fmt.Fprintf(out, "  effect: %d record(s) are now key-rotated (stale, not tampered) — `kern approval clear --stale --yes`\n", counts[governance.IntegrityKeyRotated])
		}
	}
	if o.signing || both {
		sk := signingKeyInfo()
		backup, err := approvalsign.RotateFile(sk.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "approval key rotate: signing key: %v\n", err)
			return 1
		}
		if _, err := approvalsign.EnsureKey(sk.Path, h.Passphrase); err != nil {
			prev := "there was no previous key"
			if backup != "" {
				prev = fmt.Sprintf("the old key is now %q", backup)
			}
			fmt.Fprintf(os.Stderr, "approval key rotate: %s and no new key was created: %v — re-run with a terminal attached, or the next approve creates it\n", prev, err)
			return 1
		}
		fmt.Fprintf(out, "rotated signing key\n  previous: %s\n  fingerprint: %s\n  effect: previously signed approvals no longer verify (they are not grants)\n",
			backup, signingKeyInfo().Fingerprint)
		if h.Audit != nil {
			h.Audit(o.root, "key-rotate", "signing", actor, "rotated", backup)
		}
	}
	return 0
}
