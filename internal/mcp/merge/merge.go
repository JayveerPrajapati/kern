// Package merge owns the AST-aware 3-way merge and semantic diff MCP tool bodies
// as plain functions.
package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	LoadIndex func(ctx context.Context, root string) (*index.Index, error)
}

// confinePath resolves p against root (nearest-existing-ancestor symlink
// resolution, re-appending the remaining components) and rejects any path
// that escapes root — "..", absolute paths outside the root, and symlinked
// parents (root/link -> /etc) that would smuggle a read or write outside the
// workspace. It returns the absolute cleaned path on success.
func confinePath(root, p string) (string, error) {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = filepath.Clean(cwd)
		} else {
			root = "."
		}
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(root, p)
	}
	real, err := nearestExisting(abs)
	if err != nil {
		return "", err
	}
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		rr = root
	}
	rel, err := filepath.Rel(rr, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("kern_semantic_merge: file %q escapes workspace root", p)
	}
	return abs, nil
}

// nearestExisting resolves the real location of the nearest existing ancestor
// of abs (walking up until EvalSymlinks succeeds) and re-appends the
// remaining components, so a not-yet-existing file under a symlinked
// directory is judged by its real location.
func nearestExisting(abs string) (string, error) {
	var rem []string
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if len(rem) == 0 {
				return real, nil
			}
			return filepath.Join(append([]string{real}, rem...)...), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("kern_semantic_merge: cannot resolve %q", abs)
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
}

// SemanticMerge executes an AST-aware 3-way merge between base, local,
// and remote versions of source code. Cleanly combines non-overlapping struct
// fields, methods, functions, and imports, and flags precise semantic conflicts.
func SemanticMerge(ctx context.Context, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	file := mcpargs.ArgString(args, "file")
	baseStr := mcpargs.ArgString(args, "base")
	localStr := mcpargs.ArgString(args, "local")
	remoteStr := mcpargs.ArgString(args, "remote")
	baseFile := mcpargs.ArgString(args, "base_file")
	localFile := mcpargs.ArgString(args, "local_file")
	remoteFile := mcpargs.ArgString(args, "remote_file")
	apply := mcpargs.ArgBool(args, "apply")
	format := mcpargs.ArgString(args, "format")

	readInput := func(content, fPath string) ([]byte, error) {
		if content != "" {
			p := content
			if !filepath.IsAbs(p) && root != "" {
				p = filepath.Join(root, p)
			}
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				if _, cerr := confinePath(root, p); cerr != nil {
					return nil, cerr
				}
				return os.ReadFile(p)
			}
			return []byte(content), nil
		}
		if fPath != "" {
			p := fPath
			if !filepath.IsAbs(p) && root != "" {
				p = filepath.Join(root, p)
			}
			if _, cerr := confinePath(root, p); cerr != nil {
				return nil, cerr
			}
			return os.ReadFile(p)
		}
		return nil, fmt.Errorf("missing content or file path")
	}

	baseBytes, err := readInput(baseStr, baseFile)
	if err != nil {
		return "", fmt.Errorf("base version: %w", err)
	}
	localBytes, err := readInput(localStr, localFile)
	if err != nil {
		return "", fmt.Errorf("local version: %w", err)
	}
	remoteBytes, err := readInput(remoteStr, remoteFile)
	if err != nil {
		return "", fmt.Errorf("remote version: %w", err)
	}

	targetPath := file
	if targetPath == "" {
		targetPath = localFile
	}
	if targetPath == "" {
		targetPath = "merged.go"
	}

	res, err := diff.SemanticMerge3Way(targetPath, baseBytes, localBytes, remoteBytes)
	if err != nil {
		return "", fmt.Errorf("semantic merge: %w", err)
	}

	applied := false
	if apply && file != "" && res.Clean {
		p := file
		if !filepath.IsAbs(p) && root != "" {
			p = filepath.Join(root, p)
		}
		if _, cerr := confinePath(root, p); cerr != nil {
			return "", cerr
		}
		if werr := os.WriteFile(p, []byte(res.MergedCode), 0o644); werr != nil {
			return "", fmt.Errorf("write merged file: %w", werr)
		}
		applied = true
	}

	if format == "json" {
		type outType struct {
			Clean      bool            `json:"clean"`
			Applied    bool            `json:"applied"`
			MergedCode string          `json:"merged_code"`
			Conflicts  []diff.Conflict `json:"conflicts,omitempty"`
			Diff       string          `json:"diff,omitempty"`
		}
		out := outType{
			Clean:      res.Clean,
			Applied:    applied,
			MergedCode: res.MergedCode,
			Conflicts:  res.Conflicts,
			Diff:       res.Diff,
		}
		b, jerr := json.MarshalIndent(out, "", "  ")
		if jerr != nil {
			return "", jerr
		}
		return string(b), nil
	}

	var sb strings.Builder
	sb.WriteString("# Semantic 3-Way Merge Report\n\n")
	sb.WriteString(fmt.Sprintf("- **File**: `%s`\n", targetPath))
	sb.WriteString(fmt.Sprintf("- **Clean Merge**: `%v`\n", res.Clean))
	sb.WriteString(fmt.Sprintf("- **Applied to Disk**: `%v`\n", applied))
	if len(res.Conflicts) > 0 {
		sb.WriteString(fmt.Sprintf("- **Conflicts Detected**: %d\n\n", len(res.Conflicts)))
		sb.WriteString("### Conflicts\n")
		for _, c := range res.Conflicts {
			sb.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", c.Symbol, c.Kind, c.Message))
		}
		sb.WriteString("\n")
	}

	if res.Diff != "" {
		sb.WriteString("### Merged Diff\n```diff\n")
		sb.WriteString(res.Diff)
		sb.WriteString("```\n\n")
	}

	sb.WriteString("### Merged Result\n```go\n")
	sb.WriteString(res.MergedCode)
	sb.WriteString("\n```\n")

	return sb.String(), nil
}

// SemanticDiff returns an AST-level symbol diff instead of raw lines.
// Highlights changed functions, modified signatures, and newly impacted callers.
func SemanticDiff(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	from := mcpargs.ArgString(args, "from")
	to := mcpargs.ArgString(args, "to")

	if r := mcpargs.ArgString(args, "range"); r != "" {
		if parts := strings.SplitN(r, "..", 2); len(parts) == 2 {
			from = parts[0]
			to = parts[1]
		} else {
			from = r
		}
	}

	ix, err := h.LoadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("load index: %w", err)
	}

	report, err := intel.SemanticDiff(ix, root, from, to)
	if err != nil {
		return "", err
	}

	return report.Render(), nil
}
