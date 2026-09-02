package main

import (
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/rename"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"os"
	"strings"
)

func runSchema(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if f.schema == "" {
		fatalUsage("usage: kern schema <data.json|- for stdin> --schema <schema.json>\n  or: kern prompt <template> --schema <schema.json> to inject the schema")
	}
	sc, err := loadSchema(f.schema)
	if err != nil {
		fatal("%v", err)
	}
	var b []byte
	if len(args) > 0 && args[0] == "-" {
		b, err = readStdin()
	} else if len(args) > 0 {
		b, err = os.ReadFile(args[0])
	} else {
		b, err = readStdin()
	}
	if err != nil {
		fatal("%v", err)
	}
	violations := sc.Validate(b)
	if len(violations) == 0 {
		fmt.Println("schema OK: output conforms")
		return
	}
	fmt.Printf("schema violations (%d):\n", len(violations))
	for _, v := range violations {
		fmt.Println("  - " + v)
	}
	panic(exitError{code: 1})

}

func runMask(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	var in string
	if len(args) > 0 && args[0] != "" {
		in = args[0]
	}
	var b []byte
	if in == "" || in == "-" {
		b, err = readStdin()
	} else if st, serr := os.Stat(in); serr == nil && !st.IsDir() {
		b, err = os.ReadFile(in)
	} else {
		// Not an existing file: treat the argument as inline text to mask.
		b = []byte(in)
	}
	if err != nil {
		fatal("%v", err)
	}
	res := pii.MaskAllCustom(string(b), pii.DefaultPatterns, splitNames(f.names))
	fmt.Print(res.Text)
	if res.Replaced > 0 {
		fmt.Fprintf(os.Stderr, "\nkern: masked %d secrets: ", res.Replaced)
		var parts []string
		for k, v := range res.ByLabel {
			parts = append(parts, fmt.Sprintf("%s %d", k, v))
		}
		fmt.Fprint(os.Stderr, strings.Join(parts, ", "))
		fmt.Fprintln(os.Stderr)
	}

}

func runSec(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
	}
	max := f.max
	var allow []string
	if f.severity != "" {
		allow = strings.Split(f.severity, ",")
	}
	findings, serr := sec.Scan(root)
	if serr != nil {
		fatal("kern sec: %v", serr)
	}
	findings = sec.FilterBySeverity(findings, allow)
	counts := sec.Counts(findings)
	if f.json {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
			"schema_version": kernJSONContractVersion,
			"findings":       findings,
		}); err != nil {
			fatal("%v", err)
		}
	} else {
		fmt.Print(sec.Render(findings, max))
		fmt.Fprintf(os.Stderr, "kern sec: %d findings (%d error, %d warning, %d info)\n",
			len(findings), counts["error"], counts["warning"], counts["info"])
	}
	// The exit code must be the same in --json and text mode: error-severity
	// findings are a CI gate failure regardless of output format.
	if counts["error"] > 0 {
		panic(exitError{code: 1})
	}
}

// runTaint implements `kern taint [root] [--file f] [--range a..b] [--generate]`:
// taint-lite analysis over security findings. Each finding's containing
// function is marked tainted when it is transitively called by a framework
// entry point or its file contains a source expression; --generate appends a
// test scaffold per tainted sink (go test for Go sinks, pytest for Python
// sinks, G-4). --range scopes findings to the files changed in a git range
// (".." = working tree). The MCP tool kern_taint is the primary surface (this
// thin CLI form exists so the opencode plugin can reach the same check).
func runTaint(rest []string) {
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
	findings, serr := sec.Scan(root)
	if serr != nil {
		fatal("kern taint: %v", serr)
	}
	if f.file != "" {
		filtered := findings[:0]
		for _, fd := range findings {
			if fd.File == f.file {
				filtered = append(filtered, fd)
			}
		}
		findings = filtered
	}
	// G-4: --range scopes findings to the files changed in a git range.
	// Combined with --file the two filters intersect.
	if f.range_ != "" {
		from, to, rerr := parseTaintRange(f.range_)
		if rerr != nil {
			fatalUsage("kern taint: %v", rerr)
		}
		files, rerr := intel.FilesForRange(root, from, to)
		if rerr != nil {
			fatal("kern taint: %v", rerr)
		}
		scope := fmt.Sprintf("range %s..%s", from, to)
		if from == "" && to == "" {
			scope = "worktree"
		}
		fmt.Printf("scope: %d file(s) changed in %s\n", len(files), scope)
		findings = sec.FilterByFiles(findings, files)
	}
	ix, ierr := loadOrBuild(root)
	if ierr != nil {
		ix = nil
	}
	tainted := sec.TaintLite(ix, findings)
	if len(tainted) == 0 {
		fmt.Println("no security findings")
		return
	}
	for _, tf := range tainted {
		fmt.Printf("%s:%d [%s] %s — %s\n", tf.File, tf.Line, tf.Severity, tf.Rule, tf.Message)
		if tf.Tainted {
			fmt.Printf("  tainted: yes")
			if tf.EntryPoint != "" {
				fmt.Printf(" (via %s: path %s)", tf.EntryPoint, strings.Join(tf.Path, " → "))
			}
			fmt.Println()
		} else {
			fmt.Println("  tainted: no")
		}
		if f.generate && tf.Tainted {
			sc := sec.ScaffoldFor(tf)
			lang := "go"
			if strings.HasSuffix(strings.ToLower(tf.File), ".py") || strings.HasPrefix(tf.Rule, "py-") {
				lang = "python"
			}
			fmt.Printf("# write to: %s\n```%s\n%s\n```\n", sc.File, lang, sc.Code)
		}
	}
}

// parseTaintRange splits a "from..to" git range into its endpoints. An empty
// from and to ("..") means the working tree (G-4).
func parseTaintRange(r string) (from, to string, err error) {
	parts := strings.Split(r, "..")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid range %q: want <from>..<to>", r)
	}
	return parts[0], parts[1], nil
}

func runDelete(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern delete <symbol> [root] [--json]")
	}
	sym := args[0]
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 1 {
			root = args[1]
		}
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("%v", err)
	}
	r := intel.DeleteCheck(ix, sym)
	if f.json {
		printJSON(r)
	} else {
		fmt.Println(intel.RenderDelete(r))
	}
	// The exit code must be the same in --json and text mode: an unsafe
	// deletion is a gate failure regardless of output format.
	if !r.Safe {
		panic(exitError{code: 1})
	}

}

func runRename(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 2 {
		fatalUsage("usage: kern rename <old> <new> [root] [--apply] [--json]")
	}
	oldName, newName := args[0], args[1]
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 2 {
			root = args[2]
		}
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("%v", err)
	}
	rep, err := rename.Rename(ix, oldName, newName)
	if err != nil {
		fatal("%v", err)
	}
	if f.json {
		printJSON(rep)
		return
	}
	fmt.Println(rename.Render(rep))
	if f.apply {
		if _, err := rename.Apply(root, rep); err != nil {
			fatal("apply failed (files restored): %v", err)
		}
		fmt.Printf("kern rename: %d edits applied; index will rebuild automatically\n", len(rep.Edits))
		if rep.Backup != "" {
			fmt.Printf("kern rename: backup at %s\n", rep.Backup)
		}
	}

}
