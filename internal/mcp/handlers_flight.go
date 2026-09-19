package mcp

import (
	"context"

	mcpflight "github.com/JayveerPrajapati/kern/internal/mcp/flight"
)

// handleFlight serves kern_flight: the flight-recorder reader (Workflow E observability).
func (s *Server) handleFlight(ctx context.Context, args map[string]any) (string, error) {
	return mcpflight.Flight(ctx, args)
}
