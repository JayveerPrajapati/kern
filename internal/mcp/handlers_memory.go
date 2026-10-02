package mcp

import (
	"context"
	"strconv"

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
		Remove: func(ctx context.Context, root, id string) (memory.Entry, error) {
			if n, err := strconv.Atoi(id); err == nil {
				return memory.RemoveIndex(root, n)
			}
			return memory.RemovePrefix(root, id)
		},
		Clear: func(ctx context.Context, root string) error {
			return memory.Clear(root)
		},
	}
}

func (s *Server) handleMemory(ctx context.Context, args map[string]any) (string, error) {
	return mcpmemory.Tool(ctx, s.memoryHooks(), args)
}
