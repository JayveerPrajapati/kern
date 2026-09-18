package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

func runRemember(rest []string) {
	// Parse flags first so an unknown flag (e.g. `kern remember --bogus`) is
	// rejected with a usage error (rc=2) instead of being stored as a lesson.
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	lesson := strings.Join(args, " ")
	if lesson == "" {
		fatalUsage("usage: kern remember <lesson>")
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if err := svc.Memory.Add(context.Background(), root, lesson); err != nil {
		fatal("Remember: %v", err)
	}
	fmt.Println("remembered.")

}

func runMemory(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	ctx := context.Background()
	// Sub-dispatch on the first positional: `kern memory add|list|recall ...`.
	// Otherwise preserve the classic forms (`kern memory` = list, `--clear`).
	if len(args) > 0 {
		switch args[0] {
		case "add":
			lesson := strings.Join(args[1:], " ")
			if lesson == "" {
				fatalUsage("usage: kern memory add <lesson>")
			}
			if err := svc.Memory.Add(ctx, root, lesson); err != nil {
				fatal("Memory: %v", err)
			}
			fmt.Println("remembered.")
			return
		case "list":
			entries, err := svc.Memory.List(ctx, root)
			if err != nil {
				fatal("Memory: %v", err)
			}
			if f.json {
				printJSON(entries)
				return
			}
			if s := memory.FormatEntries(entries); s != "" {
				fmt.Println(s)
			}
			return
		case "recall":
			if len(args) < 2 || args[1] == "" {
				fatalUsage("usage: kern memory recall <prompt> [--root ROOT] [--limit N]")
			}
			k := f.limit
			if k <= 0 {
				k = memory.DefaultRecallLimit
			}
			entries, err := svc.Memory.Recall(ctx, root, args[1], k)
			if err != nil {
				fatal("Memory: %v", err)
			}
			if f.json {
				printJSON(entries)
				return
			}
			if s := memory.FormatEntries(entries); s != "" {
				fmt.Println(s)
			} else {
				fmt.Println(memory.NoRecallMatch)
			}
			return
		}
	}
	if f.clear {
		if err := svc.Memory.Clear(ctx, root); err != nil {
			fatal("Memory: %v", err)
		}
		fmt.Println("project memory cleared.")
		return
	}
	entries, err := svc.Memory.List(ctx, root)
	if err != nil {
		fatal("Memory: %v", err)
	}
	if f.json {
		printJSON(entries)
		return
	}
	if s := memory.FormatEntries(entries); s != "" {
		fmt.Println(s)
	}

}

func runRecall(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern recall \"<prompt>\" [root] [--limit N]")
	}
	root := "."
	if len(args) > 1 {
		root = args[1]
	}
	k := f.limit
	if k <= 0 {
		k = memory.DefaultRecallLimit
	}
	entries, err := svc.Memory.Recall(context.Background(), root, args[0], k)
	if err != nil {
		fatal("Recall: %v", err)
	}
	if s := memory.FormatEntries(entries); s != "" {
		fmt.Println(s)
	} else {
		fmt.Println(memory.NoRecallMatch)
	}

}
