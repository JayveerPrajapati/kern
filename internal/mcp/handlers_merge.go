package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/diff"
)

// handleSemanticMerge executes an AST-aware 3-way merge between base, local,
// and remote versions of source code. Cleanly combines non-overlapping struct
// fields, methods, functions, and imports, and flags precise semantic conflicts.
func (s *Server) handleSemanticMerge(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(argString(args, "root"))
	file := argString(args, "file")
	baseStr := argString(args, "base")
	localStr := argString(args, "local")
	remoteStr := argString(args, "remote")
	baseFile := argString(args, "base_file")
	localFile := argString(args, "local_file")
	remoteFile := argString(args, "remote_file")
	apply := argBool(args, "apply")
	format := argString(args, "format")

	readInput := func(content, fPath string) ([]byte, error) {
		if content != "" {
			p := content
			if !filepath.IsAbs(p) && root != "" {
				p = filepath.Join(root, p)
			}
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return os.ReadFile(p)
			}
			return []byte(content), nil
		}
		if fPath != "" {
			p := fPath
			if !filepath.IsAbs(p) && root != "" {
				p = filepath.Join(root, p)
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
