package service

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

// MemoryService centralizes project-memory (lesson store) operations: adding
// a deliberate lesson, recalling relevant lessons for a prompt, and listing
// the stored lessons.
type MemoryService interface {
	// Add records a deliberate lesson in the project memory of root.
	Add(ctx context.Context, root, lesson string) error
	// List returns every stored lesson for root, newest-first.
	List(ctx context.Context, root string) ([]memory.Entry, error)
	// Recall returns the up-to-k lessons most relevant to prompt.
	Recall(ctx context.Context, root, prompt string, k int) ([]memory.Entry, error)
	// Clear wipes the project memory of root.
	Clear(ctx context.Context, root string) error
}

// memoryService is the default MemoryService implementation backed by the
// internal/memory engine.
type memoryService struct{}

func newMemoryService() *memoryService { return &memoryService{} }

func (s *memoryService) Add(ctx context.Context, root, lesson string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return memory.Add(resolveRoot(root), lesson)
}

func (s *memoryService) List(ctx context.Context, root string) ([]memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return memory.List(resolveRoot(root)), nil
}

func (s *memoryService) Recall(ctx context.Context, root, prompt string, k int) ([]memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if k <= 0 {
		k = 5
	}
	return memory.Recall(resolveRoot(root), prompt, k), nil
}

func (s *memoryService) Clear(ctx context.Context, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return memory.Clear(resolveRoot(root))
}
