package main

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/host"
)

// runHost implements `kern host`: the silent host-adapter pipeline. It
// injects a compact context block derived from an analyzed change into every
// detected host instruction file (CLAUDE.md, AGENTS.md, .cursor rules, GitHub
// copilot instructions), or dry-runs / checks / uninstalls that injection.
func runHost(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	pipeline := host.NewPipeline(host.NewRegistry())

	switch {
	case f.dryRun:
		var pkt *domain.ContextPacket
		if f.task != "" {
			p, err := app.New(root)
			if err != nil {
				fatal("Host: %v", err)
			}
			pp, _, err := p.Analyze(f.task)
			if err != nil {
				fatal("Host: %v", err)
			}
			pkt = &pp
		}
		fmt.Print(pipeline.DryRun(root, pkt, f.budget))
		return

	case f.check:
		fmt.Print(pipeline.Check(root))
		return

	case f.hostUninstall:
		warns := pipeline.UninstallAll(root)
		for _, w := range warns {
			fmt.Printf("warning: %s: %s\n", w.Adapter, w.Message)
		}
		fmt.Printf("removed from %d adapters\n", len(pipeline.Reg.Select(root))-len(warns))
		return
	}

	if f.task == "" {
		fatal("--task is required (or use --dry-run/--check/--uninstall)")
	}
	p, err := app.New(root)
	if err != nil {
		fatal("Host: %v", err)
	}
	pkt, _, err := p.Analyze(f.task)
	if err != nil {
		fatal("Host: %v", err)
	}
	detected := pipeline.Reg.Select(root)
	warns := pipeline.InjectAll(root, &pkt, f.budget)
	warned := map[string]bool{}
	for _, w := range warns {
		warned[w.Adapter] = true
		fmt.Printf("warning: %s: %s\n", w.Adapter, w.Message)
	}
	n := 0
	for _, a := range detected {
		if warned[a.Name()] {
			continue
		}
		blk, _ := a.Extract(root)
		n++
		fmt.Printf("injected %s (%s) %d bytes\n", a.Name(), a.FilePath(root), len(blk))
	}
	fmt.Printf("injected into %d adapters (of %d detected)\n", n, len(detected))
}
