package exec

import (
	"context"
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// Hooks provides dependencies from the owning MCP server. Only DiffFiles
// needs the session index loader (for compact-mode span annotations); a nil
// hook simply disables the annotations, matching a failed index load.
type Hooks struct {
	LoadIndex func(ctx context.Context, root string) (*index.Index, error)
}

// DiffFiles diffs two files, optionally anchored to a project root, as a
// unified or compact (index-annotated) view.
func DiffFiles(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	a := mcpargs.ArgString(args, "a")
	b := mcpargs.ArgString(args, "b")
	if a == "" || b == "" {
		return "", fmt.Errorf("a and b are required")
	}
	root := mcpargs.ArgString(args, "root")
	ap, err := rootedPath(root, a)
	if err != nil {
		return "", err
	}
	bp, err := rootedPath(root, b)
	if err != nil {
		return "", err
	}
	ab, err := os.ReadFile(ap)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", a, err)
	}
	bb, err := os.ReadFile(bp)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", b, err)
	}
	var u string
	if mcpargs.ArgBool(args, "compact") {
		// Compact mode: view-only diff with collapsed context runs,
		// annotated with the enclosing symbol when the index resolves it.
		// A failed index load just means no span annotations.
		var ix *index.Index
		if h.LoadIndex != nil {
			ix, _ = h.LoadIndex(ctx, root)
		}
		u = diff.Compact(a, b, strutil.Lines(string(ab)), strutil.Lines(string(bb)), diff.IndexSpanResolver(ix))
	} else {
		u = diff.Unified(a, b, strutil.Lines(string(ab)), strutil.Lines(string(bb)))
	}
	if u == "" {
		return "files identical", nil
	}
	return u, nil
}

// rootedPath resolves p for a file-reading tool. When root is given, the path
// must stay inside it (rejecting "..", absolute paths outside, and symlink
// escapes). A rootless call may only reference a path relative to the current
// working directory: an absolute path is rejected outright, since otherwise a
// caller could pass e.g. path=/etc/shadow and read any file on the system
// outside the confined workspace. Mirrors the mcp adapter's rootedPath.
func rootedPath(root, p string) (string, error) {
	return fsutil.RootedPath(root, p)
}

// withinRoot resolves file against root (absolute paths are used as-is) and
// requires the result to stay inside root, rejecting `..` escapes, absolute
// paths that point outside the project boundary, and symlink escapes (a
// symlink inside the project that points outside). It returns the resolved
// absolute path. Mirrors the mcp adapter's withinRoot.
func withinRoot(root, file string) (string, error) {
	return fsutil.WithinRoot(root, file)
}
