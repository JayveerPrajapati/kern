package mcp

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

func (s *Server) handleMemoryAdd(ctx context.Context, args map[string]any) (string, error) {
	lesson := argString(args, "lesson")
	if lesson == "" {
		return "", fmt.Errorf("lesson is required")
	}
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	if err := s.svc.Memory.Add(ctx, root, lesson); err != nil {
		return "", err
	}
	return "remembered.", nil

}

func (s *Server) handleMemoryList(ctx context.Context, args map[string]any) (string, error) {
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	entries, err := s.svc.Memory.List(ctx, root)
	if err != nil {
		return "", err
	}
	return memory.FormatEntries(entries), nil

}

func (s *Server) handleMemoryRecall(ctx context.Context, args map[string]any) (string, error) {
	prompt := argString(args, "prompt")
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	k := memory.DefaultRecallLimit
	limitStr := argString(args, "limit")
	if limitStr == "" {
		limitStr = argString(args, "k") // backward-compat alias for pre-rename prompts
	}
	if v := limitStr; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("limit: invalid integer %q", v)
		}
		if n > 0 {
			k = n
		}
	}
	entries, err := s.svc.Memory.Recall(ctx, root, prompt, k)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return memory.NoRecallMatch, nil
	}
	return memory.FormatEntries(entries), nil

}

func (s *Server) handleMemory(ctx context.Context, args map[string]any) (string, error) {
	action := argString(args, "action")
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	switch action {
	case "add":
		lesson := argString(args, "lesson")
		if lesson == "" {
			return "", fmt.Errorf("lesson is required for action 'add'")
		}
		if err := s.svc.Memory.Add(ctx, root, lesson); err != nil {
			return "", err
		}
		return "remembered.", nil
	case "list":
		entries, err := s.svc.Memory.List(ctx, root)
		if err != nil {
			return "", err
		}
		return memory.FormatEntries(entries), nil
	case "recall":
		prompt := argString(args, "prompt")
		if prompt == "" {
			return "", fmt.Errorf("prompt is required for action 'recall'")
		}
		entries, err := s.svc.Memory.Recall(ctx, root, prompt, memory.DefaultRecallLimit)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return memory.NoRecallMatch, nil
		}
		return memory.FormatEntries(entries), nil
	default:
		return "", fmt.Errorf("unknown memory action %q (want add, list, or recall)", action)
	}

}
