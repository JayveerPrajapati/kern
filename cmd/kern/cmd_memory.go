package main

import (
	"context"
	"fmt"
	"strings"
)

func runRemember(rest []string) {
	lesson := strings.Join(rest, " ")
	if lesson == "" {
		fatalUsage("usage: kern remember <lesson>")
	}
	if err := svc.Memory.Add(context.Background(), ".", lesson); err != nil {
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
			for _, e := range entries {
				fmt.Printf("%s  %s%s\n", e.Time.UTC().Format("2006-01-02 15:04"), label(e.Source), e.Text)
			}
			return
		case "recall":
			if len(args) < 2 || args[1] == "" {
				fatalUsage("usage: kern memory recall <prompt> [--root ROOT] [--limit N]")
			}
			k := f.limit
			if k <= 0 {
				k = 5
			}
			entries, err := svc.Memory.Recall(ctx, root, args[1], k)
			if err != nil {
				fatal("Memory: %v", err)
			}
			if f.json {
				printJSON(entries)
				return
			}
			for _, e := range entries {
				fmt.Printf("%s  %s%s\n", e.Time.UTC().Format("2006-01-02 15:04"), label(e.Source), e.Text)
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
	for _, e := range entries {
		fmt.Printf("%s  %s%s\n", e.Time.UTC().Format("2006-01-02 15:04"), label(e.Source), e.Text)
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
		k = 5
	}
	entries, err := svc.Memory.Recall(context.Background(), root, args[0], k)
	if err != nil {
		fatal("Recall: %v", err)
	}
	for _, e := range entries {
		fmt.Printf("%s  %s%s\n", e.Time.UTC().Format("2006-01-02 15:04"), label(e.Source), e.Text)
	}

}

// label prefixes an auto-captured entry (raw prompt/tool outcome) so `kern
// memory list` visibly distinguishes automatic session captures from deliberate
// lessons (report A17).
func label(source string) string {
	if source == "auto" {
		return "[auto] "
	}
	return ""
}
