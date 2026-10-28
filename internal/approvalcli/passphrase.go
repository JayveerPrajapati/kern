package approvalcli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/approvalsign"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// approvalPassphrase is test-injectable: production reads the operator's
// passphrase from the interactive terminal.
var approvalPassphrase = readApprovalPassphrase

// newApprovalSigner returns a signer that prompts at most once per command and
// reuses a COPY of the passphrase (SignApproval zeroes the slice it is given).
func newApprovalSigner() governance.ExecSigner {
	var once sync.Once
	var cached []byte
	var cachedErr error
	return governance.NewExecSigner(func(create bool) ([]byte, error) {
		once.Do(func() {
			cached, cachedErr = approvalPassphrase(create)
		})
		if cachedErr != nil {
			return nil, cachedErr
		}
		out := make([]byte, len(cached))
		copy(out, cached)
		return out, nil
	})
}

// readApprovalPassphrase prompts on the SAME interactive terminal that gated
// the decision. On first use it asks the operator to set and confirm a
// passphrase; the private key is encrypted with it at rest. A non-interactive
// caller cannot reach this path: approveTerminal already fails closed.
func readApprovalPassphrase(create bool) ([]byte, error) {
	tty, err := approveTerminal()
	if err != nil {
		return nil, err
	}
	if create {
		fmt.Fprint(tty, "Set approval-signing passphrase (min 12 chars, input hidden): ")
		p1, err := readSecretLine(tty)
		if err != nil {
			return nil, err
		}
		fmt.Fprint(tty, "\nConfirm passphrase: ")
		p2, err := readSecretLine(tty)
		fmt.Fprintln(tty)
		if err != nil {
			return nil, err
		}
		if string(p1) != string(p2) {
			return nil, fmt.Errorf("passphrases do not match")
		}
		if len(p1) < approvalsign.MinPassphraseLen {
			return nil, fmt.Errorf("passphrase must be at least %d characters", approvalsign.MinPassphraseLen)
		}
		return p1, nil
	}
	fmt.Fprint(tty, "Approval-signing passphrase (input hidden): ")
	p, err := readSecretLine(tty)
	fmt.Fprintln(tty)
	return p, err
}

// readLine reads one line without terminal-mode changes. Used for test
// buffers and platforms without an echo-off implementation.
func readLine(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return []byte(strings.TrimRight(line, "\r\n")), nil
}
