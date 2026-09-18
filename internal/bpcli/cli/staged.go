package cli

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// fileChangesFromStatus parses `git diff --name-status` output
// (tab-separated "status\tpath[\toldpath]" lines) into []domain.FileChange,
// mapping status codes to operations (A→Write, M→Edit, D→Delete, R→Rename
// with the old path carried in OldPath, anything else→Edit), then attaches
// the per-file diff blocks from a combined `git diff --unified=0` output (by
// new path) with the REAL added/removed line numbers parsed from the hunk
// headers. skip, when non-nil, is applied per path before a change is
// created (the staged-changes discovery uses it to exclude kern/blueprint
// runtime artifacts). Shared by discoverStagedChanges and
// discoverWorkingTreeChanges.
func fileChangesFromStatus(nameStatus, unified string, skip func(path string) bool) []domain.FileChange {
	var changes []domain.FileChange
	for _, line := range strings.Split(strings.TrimSpace(nameStatus), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		statusCode := parts[0]
		filePath := parts[1]
		if skip != nil && skip(filePath) {
			continue
		}
		fc := domain.FileChange{Path: filePath}

		// Map git status code to Operation.
		switch {
		case strings.HasPrefix(statusCode, "A"):
			fc.Op = domain.OpWrite
		case strings.HasPrefix(statusCode, "M"):
			fc.Op = domain.OpEdit
		case strings.HasPrefix(statusCode, "D"):
			fc.Op = domain.OpDelete
		case strings.HasPrefix(statusCode, "R"):
			fc.Op = domain.OpRename
			if len(parts) >= 3 {
				fc.OldPath = parts[1]
				fc.Path = parts[2]
			}
		default:
			fc.Op = domain.OpEdit
		}

		changes = append(changes, fc)
	}

	// Attach the per-file diff blocks (by new path) to the matching FileChange.
	byPath := make(map[string]*domain.FileChange, len(changes))
	for i := range changes {
		byPath[changes[i].Path] = &changes[i]
	}
	for path, block := range splitDiffBlocks(unified) {
		fc, ok := byPath[path]
		if !ok {
			continue
		}
		fc.Diff = block
		if isBinaryDiffBlock(block) {
			// Binary files have no textual hunks: keep the block as Diff but
			// attach no line numbers.
			continue
		}
		fc.Added, fc.Removed = parseDiffLineNumbers(block)
	}
	return changes
}
