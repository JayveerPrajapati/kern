package mcp

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/brief"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/pack"
	"os"
)

func (s *Server) handleCompact(ctx context.Context, args map[string]any) (string, error) {
	{
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("path is required")
		}
		root := argString(args, "root")
		abs, err := rootedPath(root, path)
		if err != nil {
			return "", err
		}
		content, err := code.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", path, err)
		}
		sum := code.Summarize(abs, content, 200)
		return sum.Render(), nil

	}
}

func (s *Server) handleBuddy(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		// Warm the index in the background so later calls render the fast path.
		go func() {
			if err := brief.Warm(root); err != nil {
				// best-effort: the digest still renders without index sections
			}
		}()
		out, err := brief.Build(root)
		if err != nil {
			return "", err
		}
		return out, nil

	}
}

func (s *Server) handleProjectMap(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		// Default to 0 (unlimited) so the full project map is returned; a
		// caller can still cap it with max_files. Previously hardcoded to 500,
		// which silently truncated repos larger than that (e.g. 758 files).
		maxFiles := 0
		if v := argString(args, "max_files"); v != "" {
			n, err := atoiArg(v, maxFiles)
			if err != nil {
				return "", err
			}
			maxFiles = n
		}
		p, err := code.BuildProject(root, maxFiles, 200)
		if err != nil {
			return "", err
		}
		return p.Render(), nil

	}
}

func (s *Server) handlePack(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		opts := pack.Options{}
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, opts.MaxTokens)
			if err != nil {
				return "", err
			}
			opts.MaxTokens = n
		} else {
			opts.MaxTokens = 8000
		}
		if v := argString(args, "instructions"); v != "" {
			opts.SkipInstructions = v == "false"
		}
		b, err := pack.Build(root, opts)
		if err != nil {
			return "", err
		}
		if argString(args, "format") == "json" {
			return b.JSON()
		}
		return b.Render(), nil

	}
}
