package mcp

import (
	"context"

	mcpmemory "github.com/JayveerPrajapati/kern/internal/mcp/memory"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

func (s *Server) memoryHooks() mcpmemory.Hooks {
	return mcpmemory.Hooks{
		Add: func(ctx context.Context, root, lesson string) error {
			return memory.Add(root, lesson)
		},
		List: func(ctx context.Context, root string) ([]memory.Entry, error) {
			return memory.List(root), nil
		},
		Recall: func(ctx context.Context, root, prompt string, k int) ([]memory.Entry, error) {
			return memory.Recall(root, prompt, k), nil
		},
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
