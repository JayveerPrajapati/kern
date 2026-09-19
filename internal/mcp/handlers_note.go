package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/mcp/note"
)

// handleNote implements kern_note: the model-facing face of the governed
// decision-record system.
func (s *Server) handleNote(ctx context.Context, args map[string]any) (string, error) {
	return note.Handle(ctx, args)
}
