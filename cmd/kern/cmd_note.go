package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/note"
)

// runNote implements `kern note`: governed decision records at
// docs/notes/{lifecycle}/{class}/yyyy-mm-dd-topic-title.md. Subcommands:
// new (create a note skeleton), list (inventory), status (move between
// lifecycle folders), validate (gate: report format violations).
func runNote(rest []string) {
	if len(rest) == 0 {
		noteHelp()
		return
	}
	switch rest[0] {
	case "new":
		noteNew(rest[1:])
	case "list":
		noteList(rest[1:])
	case "status":
		noteStatus(rest[1:])
	case "validate":
		noteValidate(rest[1:])
	case "help", "-h", "--help":
		noteHelp()
	default:
		fatalUsage("unknown note subcommand %q (try: new, list, status, validate)", rest[0])
	}
}

func noteNew(args []string) {
	var lifecycle = note.Proposed
	var class note.Class
	date := time.Now().Format("2006-01-02")
	root := "."
	var titleWords []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--lifecycle", "-l":
			if i+1 < len(args) {
				lifecycle = note.Lifecycle(args[i+1])
				i++
			}
		case "--class", "-c":
			if i+1 < len(args) {
				class = note.Class(args[i+1])
				i++
			}
		case "--date", "-d":
			if i+1 < len(args) {
				date = args[i+1]
				i++
			}
		case "--root", "-r":
			if i+1 < len(args) {
				root = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fatalUsage("unknown note new flag %q", args[i])
			}
			titleWords = append(titleWords, args[i])
		}
	}
	title := strings.TrimSpace(strings.Join(titleWords, " "))
	if title == "" {
		fatalUsage("usage: kern note new --class <class> [--lifecycle proposed|implemented] [--date yyyy-mm-dd] \"<title>\"")
	}
	rel, err := note.PathFor(lifecycle, class, date, title)
	if err != nil {
		fatalUsage("%v", err)
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Stat(abs); err == nil {
		fmt.Fprintf(os.Stderr, "Error: note already exists at %s\n", rel)
		panic(exitError{code: 1})
	}
	body := fmt.Sprintf("Record the decision for this %s change. Describe the problem, what was decided, and what was given up.", class)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	if err := os.WriteFile(abs, []byte(note.Skeleton(lifecycle, title, body)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	fmt.Printf("created %s\n", rel)
}

// restWords returns the trailing positional words of args (the title).
func restWords(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func noteList(args []string) {
	root := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root", "-r":
			if i+1 < len(args) {
				root = args[i+1]
				i++
			}
		}
	}
	paths, err := note.WalkNotes(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	if len(paths) == 0 {
		fmt.Println("no notes under docs/notes/")
		return
	}
	for _, p := range paths {
		rel, _ := filepath.Rel(root, p)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 4 {
			fmt.Printf("  %s\n", filepath.ToSlash(rel))
			continue
		}
		lc, cls, name := parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
		title := name
		if data, err := os.ReadFile(p); err == nil {
			lines := strings.Split(string(data), "\n")
			if len(lines) > 0 && strings.HasPrefix(lines[0], "# Agent Note: ") {
				title = strings.TrimPrefix(lines[0], "# Agent Note: ")
			}
		}
		fmt.Printf("  [%s/%s] %s — %s\n", lc, cls, title, filepath.ToSlash(rel))
	}
}

func noteStatus(args []string) {
	root := "."
	var target note.Lifecycle
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--set", "-s":
			if i+1 < len(args) {
				target = note.Lifecycle(args[i+1])
				i++
			}
		case "--root", "-r":
			if i+1 < len(args) {
				root = args[i+1]
				i++
			}
		default:
			positionals = append(positionals, args[i])
		}
	}
	file := ""
	var reasonWords []string
	for i, p := range positionals {
		if i == 0 {
			file = p
			continue
		}
		reasonWords = append(reasonWords, p)
	}
	if file == "" || target == "" {
		fatalUsage("usage: kern note status <file> --set proposed|implemented|rejected|archived [--root <dir>]")
	}
	reason := strings.TrimSpace(strings.Join(reasonWords, " "))
	newRel, err := note.Move(root, file, target, reason, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	fmt.Printf("moved %s -> %s\n", file, newRel)
}

func noteValidate(args []string) {
	root := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root", "-r":
			if i+1 < len(args) {
				root = args[i+1]
				i++
			}
		default:
			fatalUsage("unknown note validate flag %q", args[i])
		}
	}
	violations, err := note.ValidateTree(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	if len(violations) == 0 {
		fmt.Println("note tree valid")
		return
	}
	for _, v := range violations {
		fmt.Printf("  ! %s: %s\n", v.Path, v.Message)
	}
	panic(exitError{code: 1})
}

func noteHelp() {
	fmt.Println("usage: kern note <subcommand>")
	fmt.Println()
	fmt.Println("Governed decision records at docs/notes/{lifecycle}/{class}/yyyy-mm-dd-topic-title.md")
	fmt.Println("  new <title> --class <class> [--lifecycle proposed|implemented] [--date yyyy-mm-dd] [--root <dir>]")
	fmt.Println("  list [--root <dir>]")
	fmt.Println("  status <file> --set proposed|implemented|rejected|archived [--root <dir>]")
	fmt.Println("  validate [--root <dir>]")
	fmt.Println()
	fmt.Println("Lifecycles: proposed -> implemented -> archived (rejected is terminal)")
	fmt.Println("Classes: feature | bug-fix | simplification | architecture | process | testing")
}
