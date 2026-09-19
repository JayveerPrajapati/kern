package mcp

import (
	"context"

	mcpmemory "github.com/JayveerPrajapati/kern/internal/mcp/memory"
)

func (s *Server) memoryHooks() mcpmemory.Hooks {
	return mcpmemory.Hooks{
		Add:    s.svc.Memory.Add,
		List:   s.svc.Memory.List,
		Recall: s.svc.Memory.Recall,
	}
}

func (s *Server) handleMemoryAdd(ctx context.Context, args map[string]any) (string, error) {
	return mcpmemory.Add(ctx, s.memoryHooks(), args)
}

func (s *Server) handleMemoryList(ctx context.Context, args map[string]any) (string, error) {
	return mcpmemory.List(ctx, s.memoryHooks(), args)
}

func (s *Server) handleMemoryRecall(ctx context.Context, args map[string]any) (string, error) {
	return mcpmemory.Recall(ctx, s.memoryHooks(), args)
}

func (s *Server) handleMemory(ctx context.Context, args map[string]any) (string, error) {
	return mcpmemory.Action(ctx, s.memoryHooks(), args)
}
