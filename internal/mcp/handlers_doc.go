package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/doc"
)

// The doc family lives in internal/mcp/doc. These adapters are the
// dispatch-table surface; Search injects the kernel hooks.

func (s *Server) handleDoc(ctx context.Context, args map[string]any) (string, error) {
	// F11: kern_doc with no root must search the project this server serves,
	// not whatever directory the process happened to start in. docsearch
	// used to resolve an empty root via the process cwd — a root-bound
	// server (NewServerForRoot) or one launched from a multi-project
	// container directory then walked every project under the cwd into ONE
	// cross-project doc index, so kern_doc returned hits from other repos.
	// Default the root to the server's workspace root (the confinement gate
	// already rejects roots outside it).
	if argString(args, "root") == "" && len(s.roots) > 0 {
		args["root"] = s.roots[0]
	}
	return doc.Tool(ctx, doc.Hooks{
		LoadIndex:   s.loadIndex,
		NewGovernor: s.newGovernor,
	}, args)
}

func (s *Server) handleCommitmsg(ctx context.Context, args map[string]any) (string, error) {
	return doc.Commitmsg(ctx, args)
}

func (s *Server) handlePrecache(ctx context.Context, args map[string]any) (string, error) {
	return doc.Precache(ctx, args)
}
