package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/council"
	"github.com/JayveerPrajapati/kern/internal/reviewpack"
)

// runReviewConsensus implements `kern review-consensus <pack.json>...
// [--json]` (blueprint KERN-P2-002): normalize two or more review packs
// into a consensus/divergence report — agreement is reported with its exact
// scope, disagreement is surfaced, and no majority winner is selected.
func runReviewConsensus(rest []string) int {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 2 {
		fatalUsage("usage: kern review-consensus <pack1.json> <pack2.json> [more packs...] [--json]")
	}
	packs := make([]*reviewpack.ReviewPack, 0, len(args))
	for _, path := range args {
		b, err := os.ReadFile(path)
		if err != nil {
			fatal("review-consensus: %v", err)
		}
		var p reviewpack.ReviewPack
		if err := json.Unmarshal(b, &p); err != nil {
			fatal("review-consensus: %s: %v", path, err)
		}
		packs = append(packs, &p)
	}
	report := council.Normalize(packs)
	out := council.RenderReport(report)
	if f.json {
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fatal("review-consensus: %v", err)
		}
		out = string(b)
	}
	fmt.Print(out)
	return 0
}
