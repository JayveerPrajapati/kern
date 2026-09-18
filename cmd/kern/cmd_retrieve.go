package main

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/retrieval"
	"os"
	"strings"
)

// retrieveUsage is the canonical `kern retrieve` usage line, shared verbatim
// by the dispatch table help text and every error path (QA: the error paths
// used to print three divergent variants that contradicted the documented
// usage — single source of truth now).
const retrieveUsage = "usage: kern retrieve --task-type <type> --symbol <name> [root] [--max-tokens N]"

// parseRetrieveLevelCLI maps the string form of a disclosure level to the
// retrieval.Level constants, mirroring the MCP handler's parsing. Empty
// defaults to L2.
func parseRetrieveLevelCLI(v string) (retrieval.Level, error) {
	switch v {
	case "", "l2":
		return retrieval.L2, nil
	case "l1":
		return retrieval.L1, nil
	case "l3":
		return retrieval.L3, nil
	}
	return 0, fmt.Errorf("invalid level %q (want l1|l2|l3)", v)
}

// runRetrieve implements `kern retrieve`: progressive disclosure retrieval
// (L1=index summary, L2=neighborhood, L3=source). The CLI process-local
// DefaultRegistry mirrors the MCP server's registry lifecycle, so a handle
// resolved in the same process (kern retrieve → kern resolve) is valid; a
// fresh process requires re-running retrieve first.
func runRetrieve(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	level, err := parseRetrieveLevelCLI(f.level)
	if err != nil {
		fatal("Retrieve: %v", err)
	}
	if f.taskType != "" && f.level != "" {
		fatal("Retrieve: use only one of level/task_type")
	}
	query := f.query
	symbol := f.symbol
	if query == "" && symbol == "" && len(args) > 0 {
		// Positional fallback: a bare token is the query at l1, else the
		// symbol, matching the MCP tool's per-level requirement.
		if level == retrieval.L1 {
			query = args[0]
		} else {
			symbol = args[0]
		}
	}
	if f.taskType != "" {
		// Task-type retrieval: the disclosure level comes from the
		// planner policy for the task type (documentation→l1,
		// refactor→l3, everything else l2).
		if symbol == "" {
			fatalUsage(retrieveUsage)
		}
	} else {
		if level == retrieval.L1 && query == "" {
			fatalUsage(retrieveUsage)
		}
		if level != retrieval.L1 && symbol == "" {
			fatalUsage(retrieveUsage)
		}
	}
	root := f.root
	if root == "" {
		root = "."
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("Retrieve: %v", err)
	}
	limit := f.limit
	if limit <= 0 {
		limit = 10
	}
	lines := f.lines
	if lines <= 0 {
		lines = 12
	}
	depth := f.depth
	if depth < 0 {
		depth = 0
	}
	maxNodes := f.max
	if maxNodes < 0 {
		maxNodes = 0
	}
	opts := retrieval.Options{
		Query:     query,
		Symbol:    symbol,
		Level:     level,
		Limit:     limit,
		Depth:     depth,
		MaxNodes:  maxNodes,
		Lines:     lines,
		MaxTokens: f.maxTokens,
	}
	var res *retrieval.Result
	if f.taskType != "" {
		res, err = retrieval.RetrieveForTask(ix, symbol, f.taskType, f.maxTokens)
	} else {
		res, err = retrieval.Retrieve(ix, opts)
	}
	if err != nil {
		fatal("Retrieve: %v", err)
	}
	// Persist the handles this run registered so a later `kern resolve` in a
	// fresh process can map the printed handle ID back to its symbol. The
	// store is best-effort: a write failure must not fail retrieval itself.
	if err := retrieval.DefaultRegistry.Save(retrieval.HandleStorePath(root)); err != nil {
		fmt.Fprintf(os.Stderr, "kern: retrieve: %v\n", err)
	}
	if f.json {
		printJSON(res)
		return
	}
	fmt.Println(retrieval.Render(res))
}

// runResolve implements `kern resolve <handle-id>`: resolves a previously
// returned handle to L2 or L3 content. The handle must come from a kern
// retrieve run in the same process (the registry is process-local).
func runResolve(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern resolve <handle-id> [root] [--level l2|l3] [--max-tokens N]")
	}
	level, err := parseRetrieveLevelCLI(f.level)
	if err != nil {
		fatal("Resolve: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 1 {
			root = args[1]
		}
	}
	// Load handles persisted by earlier `kern retrieve` runs so a handle ID
	// printed by a previous process resolves here.
	retrieval.DefaultRegistry.Load(retrieval.HandleStorePath(root))
	h, ok := retrieval.DefaultRegistry.Resolve(args[0])
	if !ok {
		// Render displays an 8-char handle prefix; fall back to a prefix
		// match so a handle copied verbatim from `kern retrieve` output
		// resolves.
		for _, cand := range retrieval.DefaultRegistry.List() {
			if strings.HasPrefix(cand.ID, args[0]) {
				h = cand
				ok = true
				break
			}
		}
	}
	if !ok {
		fatal("Resolve: unknown handle %q (re-run kern retrieve to refresh the handle store)", args[0])
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("Resolve: %v", err)
	}
	res, err := retrieval.Retrieve(ix, retrieval.Options{
		Symbol:    h.Name,
		Level:     level,
		MaxTokens: f.maxTokens,
	})
	if err != nil {
		fatal("Resolve: %v", err)
	}
	if f.json {
		printJSON(res)
		return
	}
	fmt.Println(retrieval.Render(res))
}
