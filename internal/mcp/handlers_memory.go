package mcp

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"os"
	"strconv"
	"strings"
)

func (s *Server) handleMemoryAdd(ctx context.Context, args map[string]any) (string, error) {
	{
		lesson := argString(args, "lesson")
		if lesson == "" {
			return "", fmt.Errorf("lesson is required")
		}
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		if err := memory.Add(root, lesson); err != nil {
			return "", err
		}
		return "remembered.", nil

	}
}

func (s *Server) handleMemoryList(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		var b strings.Builder
		for _, e := range memory.List(root) {
			fmt.Fprintf(&b, "%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	}
}

func (s *Server) handleMemoryRecall(ctx context.Context, args map[string]any) (string, error) {
	{
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
		if v := argString(args, "k"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("k: invalid integer %q", v)
			}
			if n > 0 {
				k = n
			}
		}
		var b strings.Builder
		for _, e := range memory.Recall(root, prompt, k) {
			fmt.Fprintf(&b, "%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	}
}

func (s *Server) handleMemory(ctx context.Context, args map[string]any) (string, error) {
	{
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
			if err := memory.Add(root, lesson); err != nil {
				return "", err
			}
			return "remembered.", nil
		case "list":
			var b strings.Builder
			for _, e := range memory.List(root) {
				fmt.Fprintf(&b, "%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
			}
			return strings.TrimSuffix(b.String(), "\n"), nil
		case "recall":
			prompt := argString(args, "prompt")
			if prompt == "" {
				return "", fmt.Errorf("prompt is required for action 'recall'")
			}
			var b strings.Builder
			for _, e := range memory.Recall(root, prompt, 5) {
				fmt.Fprintf(&b, "%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
			}
			return strings.TrimSuffix(b.String(), "\n"), nil
		default:
			return "", fmt.Errorf("unknown memory action %q (want add, list, or recall)", action)
		}

	}
}
