package governance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// ReadAuditTrail returns the governance audit trail for root, optionally
// filtered to one task (taskID "" = all entries). It reads the tamper-evident
// .kern/audit chain; malformed entries are skipped, never fatal to the whole
// trail.
//
// A missing root is an error, not an empty trail: blueprint verify-receipt
// relies on the subprocess exit code to distinguish "chain unreadable" (soft
// skip, warn) from "chain readable but hash absent" (hard failure). Without
// this check `kern audit --root <missing>` would exit 0 with "no audit
// entries" and a receipt whose worktree was cleaned up would be wrongly
// invalidated.
func ReadAuditTrail(ctx context.Context, root, taskID string) ([]AuditEntry, error) {
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("audit: root directory does not exist: %s", root)
	}
	store := storage.NewLog(filepath.Join(root, ".kern", "audit"))
	entries, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []AuditEntry
	for _, e := range entries {
		var entry AuditEntry
		if err := storage.UnmarshalValue(e.Value, &entry); err != nil {
			continue // malformed entry — skip, never fail the whole trail
		}
		if taskID != "" && entry.TaskID != taskID {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}
