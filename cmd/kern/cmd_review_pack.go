package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/reviewpack"
)

// runReviewPack implements `kern review-pack [root] --task TASK
// [--lens L] [--max-tokens N] [--json] [--out PATH]`:
// one immutable, deterministic evidence packet that any
// reviewer (human, model, or council member) can be given verbatim.
func runReviewPack(rest []string) int {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) > 0 {
		root = args[0]
	}
	if f.task == "" {
		fatalUsage("usage: kern review-pack [root] --task TASK [--lens L] [--max-tokens N] [--json] [--out PATH]")
	}
	p, err := app.New(root)
	if err != nil {
		fatal("review-pack: %v", err)
	}
	pkt, _, err := p.Analyze(f.task)
	if err != nil {
		fatal("review-pack: %v", err)
	}
	pack, err := reviewpack.Build(root, f.task, &pkt, p.Index(), reviewpack.Options{
		Lens:      f.lens,
		MaxTokens: f.maxTokens,
	})
	if err != nil {
		fatal("review-pack: %v", err)
	}
	out := reviewpack.RenderPack(pack)
	if f.json {
		b, err := json.MarshalIndent(pack, "", "  ")
		if err != nil {
			fatal("review-pack: %v", err)
		}
		out = string(b)
	}
	fmt.Print(out)
	if f.out != "" {
		b, err := json.MarshalIndent(pack, "", "  ")
		if err != nil {
			fatal("review-pack: %v", err)
		}
		if err := os.WriteFile(f.out, b, 0o644); err != nil {
			fatal("write %s: %v", f.out, err)
		}
		fmt.Printf("\nwrote %s (content hash %s)\n", f.out, pack.ContentHash)
	}
	return 0
}
