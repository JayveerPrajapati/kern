package main

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"strings"
)

func runRemember(rest []string) {
	lesson := strings.Join(rest, " ")
	if lesson == "" {
		fatalUsage("usage: kern remember <lesson>")
	}
	if err := memory.Add(".", lesson); err != nil {
		fatal("%v", err)
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
	// Sub-dispatch on the first positional: `kern memory add|list|recall ...`.
	// Otherwise preserve the classic forms (`kern memory` = list, `--clear`).
	if len(args) > 0 {
		switch args[0] {
		case "add":
			lesson := strings.Join(args[1:], " ")
			if lesson == "" {
				fatalUsage("usage: kern memory add <lesson>")
			}
			if err := memory.Add(root, lesson); err != nil {
				fatal("%v", err)
			}
			fmt.Println("remembered.")
			return
		case "list":
			for _, e := range memory.List(root) {
				fmt.Printf("%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
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
			for _, e := range memory.Recall(root, args[1], k) {
				fmt.Printf("%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
			}
			return
		}
	}
	if f.clear {
		if err := memory.Clear(root); err != nil {
			fatal("%v", err)
		}
		fmt.Println("project memory cleared.")
		return
	}
	for _, e := range memory.List(root) {
		fmt.Printf("%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
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
	for _, e := range memory.Recall(root, args[0], k) {
		fmt.Printf("%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
	}

}
