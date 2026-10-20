package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

// memoryUsage is the single source of the `kern memory` help text: printed
// by bare `kern memory` (F4) and reused as the dispatch-table entry's usage
// field for `kern memory --help`.
const memoryUsage = "usage: kern memory add|list|recall|remove|prune <...>\n" +
	"  subcommands:\n" +
	"    add <lesson>        store a lesson ('remember' is an alias)\n" +
	"    list                list stored lessons\n" +
	"    recall <prompt>     ranked keyword recall\n" +
	"    remove <n|prefix>   delete one entry by 1-based list index or text prefix\n" +
	"    prune --auto        remove automatic captures from both stores and collapse exact duplicates\n" +
	"  options:\n" +
	"    --auto            tag the lesson as an automatic capture (add/remember); confirm auto prune\n" +
	"    --clear            clear/empty the whole store (v1 + typed)\n" +
	"    --json             emit JSON output\n" +
	"    --limit            cap results at N\n" +
	"    --root             project root (default: .)"

// memoryAdd stores a lesson in project memory. It is the shared body of
// `kern memory add` and its alias entry `kern remember`. Explicit lessons
// dual-write via memory.AddExplicit: the typed store (buddy's "Project
// memory") and the v1 store (recall/list). With auto=true the lesson is
// written as an automatic capture (memory.AddAuto, v1 Source "auto") — the
// plugin passes --auto in Wave 2 so session captures never enter the
// explicit lessons path.
func memoryAdd(ctx context.Context, root, lesson string, auto bool) {
	if auto {
		written, err := memory.AddAuto(root, lesson)
		if err != nil {
			fatal("Memory: %v", err)
		}
		if !written {
			fmt.Println("already remembered (auto) — identical capture within 60s, not re-stored.")
			return
		}
		fmt.Println("remembered (auto).")
		return
	}
	written, err := memory.AddExplicit(root, lesson)
	if err != nil {
		fatal("Memory: %v", err)
	}
	if !written {
		fmt.Println("already remembered — identical lesson within 60s, not re-stored.")
		return
	}
	fmt.Println("remembered.")
}

// memoryRecall renders the recall results for a prompt. It is the shared
// body of `kern memory recall` and its alias entry `kern recall`. k <= 0
// falls back to the default recall limit; json emits the raw entries.
func memoryRecall(ctx context.Context, root, prompt string, k int, json bool) {
	if k <= 0 {
		k = memory.DefaultRecallLimit
	}
	entries := memory.Recall(root, prompt, k)
	if json {
		printJSON(entries)
		return
	}
	if s := memory.FormatEntries(entries); s != "" {
		fmt.Println(s)
	} else {
		fmt.Println(memory.NoRecallMatch)
	}
}

func runRemember(rest []string) {
	// Parse flags first so an unknown flag (e.g. `kern remember --bogus`) is
	// rejected with a usage error (rc=2) instead of being stored as a lesson.
	f, args := parseFlagsOrDie(rest)
	lesson := strings.Join(args, " ")
	if lesson == "" {
		fatalUsage("usage: kern remember <lesson> [--auto]")
	}
	root := projectRoot(f)
	memoryAdd(context.Background(), root, lesson, f.auto)

}

func runMemory(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
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
			memoryAdd(ctx, root, lesson, f.auto)
			return
		case "list":
			entries := memory.List(root)
			if f.json {
				printJSON(entries)
				return
			}
			if s := memory.FormatEntries(entries); s != "" {
				fmt.Println(s)
			} else {
				// QA 6.3: an empty store printed an empty string (exit 0) —
				// friendly pointer instead, machine-readable --json unchanged.
				fmt.Println("no lessons stored yet (add one with: kern memory add <lesson>)")
			}
			return
		case "recall":
			if len(args) < 2 || args[1] == "" {
				fatalUsage("usage: kern memory recall <prompt> [--root ROOT] [--limit N]")
			}
			memoryRecall(ctx, root, args[1], f.limit, f.json)
			return
		case "remember":
			// Alias of "add": `kern memory remember <lesson>`.
			lesson := strings.Join(args[1:], " ")
			if lesson == "" {
				fatalUsage("usage: kern memory remember <lesson>")
			}
			memoryAdd(ctx, root, lesson, f.auto)
			return
		case "prune":
			// F2: bulk cleanup of automatic captures. Removes Source "auto"
			// (and auto-shaped) entries from BOTH stores and collapses exact
			// duplicates; genuine user lessons always survive.
			if !f.auto {
				fatalUsage("usage: kern memory prune --auto [--root ROOT] (prune only removes automatic captures; pass --auto to confirm)")
			}
			stats, perr := memory.PruneAuto(root)
			if perr != nil {
				fatal("memory prune: %v", perr)
			}
			fmt.Printf("pruned: %d auto captures removed (typed %d, v1 %d), %d exact duplicates collapsed (typed %d, v1 %d)\n",
				stats.TypedRemoved+stats.V1Removed, stats.TypedRemoved, stats.V1Removed,
				stats.TypedCollapsed+stats.V1Collapsed, stats.TypedCollapsed, stats.V1Collapsed)
			return
		case "remove":
			// QA F5: targeted deletion — by 1-based list index or by text
			// prefix; --clear stays the whole-store nuke.
			target := strings.TrimSpace(strings.Join(args[1:], " "))
			if target == "" {
				fatalUsage("usage: kern memory remove <n | text-prefix> [--root ROOT]")
			}
			var removed memory.Entry
			if n, aerr := strconv.Atoi(target); aerr == nil {
				removed, aerr = memory.RemoveIndex(root, n)
				if aerr != nil {
					fatal("memory remove: %v", aerr)
				}
			} else {
				var perr error
				removed, perr = memory.RemovePrefix(root, target)
				if perr != nil {
					fatal("memory remove: %v", perr)
				}
			}
			fmt.Printf("removed: %s\n", memory.FormatEntry(removed))
			return
		default:
			// Any other first positional is a usage error — never a silent
			// fall-through to the list path.
			fatalUsage("memory: unknown subcommand %q (usage: kern memory add|list|recall|remove <...> or kern remember <lesson>)", args[0])
		}
	}
	// F4: bare `kern memory` (no subcommand, no explicit request) silently
	// fell through to a (possibly empty) list print and exit 0 — print the
	// memory help instead. --clear/--json remain working explicit requests;
	// `kern memory recall` with no prompt stays a usage error. The help text
	// is the shared memoryUsage const (not printCommandHelp, which reads
	// commandTable and would form an initialization cycle with the dispatch
	// table entry that calls this handler).
	if !f.clear && !f.json {
		fmt.Println("kern memory — engineering memory ops")
		fmt.Println(memoryUsage)
		return
	}
	if f.clear {
		if err := memory.Clear(root); err != nil {
			fatal("Memory: %v", err)
		}
		fmt.Println("project memory cleared.")
		return
	}
	entries := memory.List(root)
	if f.json {
		printJSON(entries)
		return
	}
	if s := memory.FormatEntries(entries); s != "" {
		fmt.Println(s)
	}

}

func runRecall(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern recall \"<prompt>\" [root] [--limit N]")
	}
	root := "."
	if len(args) > 1 {
		root = args[1]
	}
	memoryRecall(context.Background(), root, args[0], f.limit, false)

}
