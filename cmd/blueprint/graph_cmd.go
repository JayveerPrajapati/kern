package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/graph"
)

// runGraph implements the `blueprint graph` command.
func runGraph(args []string) int {
	fs := flag.NewFlagSet("graph", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	repo := fs.String("repo", "", "repository root (default: current directory)")
	format := fs.String("format", "mermaid", "output format: mermaid|dot|json (default: mermaid)")
	output := fs.String("output", "", "write graph output to file instead of stdout")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	absRoot, code := resolveRepoRoot(*repo)
	if code != 0 {
		return code
	}

	g, err := graph.Load(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: cannot load boundary graph: %v\n", err)
		return 2
	}

	var content string
	switch strings.ToLower(*format) {
	case "mermaid":
		content = g.ToMermaid()
	case "dot", "graphviz":
		content = g.ToDOT()
	case "json":
		j, err := g.ToJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: serialize graph JSON: %v\n", err)
			return 2
		}
		content = j + "\n"
	default:
		fmt.Fprintf(os.Stderr, "blueprint: invalid --format %q (must be mermaid|dot|json)\n", *format)
		return 2
	}

	if *output != "" {
		if err := os.WriteFile(*output, []byte(content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: cannot write graph to %s: %v\n", *output, err)
			return 2
		}
		fmt.Printf("Architectural boundary graph written to %s (%s format)\n", *output, *format)
		return 0
	}

	fmt.Print(content)
	return 0
}
