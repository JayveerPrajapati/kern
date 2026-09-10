package mcp

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

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

func memoryListText(entries []memory.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		marker := ""
		if e.Source == "auto" {
			marker = "[auto] "
		}
		fmt.Fprintf(&b, "%s  %s%s\n", e.Time.UTC().Format("2006-01-02 15:04"), marker, e.Text)
	}
	return strings.TrimSuffix(b.String(), "\n")
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
	return memoryListText(entries), nil

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
	k := 5
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
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil

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
		return memoryListText(entries), nil
	case "recall":
		prompt := argString(args, "prompt")
		if prompt == "" {
			return "", fmt.Errorf("prompt is required for action 'recall'")
		}
		entries, err := s.svc.Memory.Recall(ctx, root, prompt, 5)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, e := range entries {
			fmt.Fprintf(&b, "%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil
	default:
		return "", fmt.Errorf("unknown memory action %q (want add, list, or recall)", action)
	}

}
