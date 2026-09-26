package main

import (
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/remove"
	"github.com/JayveerPrajapati/kern/internal/rename"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"os"
	"path/filepath"
	"strings"
)

func runSchema(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if f.schema == "" {
		fatalUsage("usage: kern schema <data.json|- for stdin> --schema <schema.json>\n  or: kern prompt <template> --schema <schema.json> to inject the schema")
	}
	sc, err := loadSchema(f.schema)
	if err != nil {
		fatal("schema: %v", err)
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
		fatal("schema: %v", err)
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
	fatal("schema: %d violation(s) — see output above", len(violations))

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
		fatal("mask: %v", err)
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	maxN := f.max
	// Default gate is error-only: warnings/info are triage material, not CI
	// failures. Pass --severity explicitly (or --severity all) to widen the lens.
	allow := []string{"error"}
	if f.severity != "" {
		if strings.ToLower(f.severity) == "all" {
			allow = nil
		} else {
			allow = strings.Split(f.severity, ",")
		}
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
			fatal("sec: %v", err)
		}
	} else {
		fmt.Print(sec.Render(findings, maxN))
		if f.severity == "" {
			fmt.Fprintf(os.Stderr, "kern sec: %d findings (%d error, %d warning, %d info) [use --severity error,warning,info to view all]\n",
				len(findings), counts["error"], counts["warning"], counts["info"])
		} else {
			fmt.Fprintf(os.Stderr, "kern sec: %d findings (%d error, %d warning, %d info)\n",
				len(findings), counts["error"], counts["warning"], counts["info"])
		}
	}
	// The exit code must be the same in --json and text mode: error-severity
	// findings are a policy outcome (exit 3, the review-family convention —
	// QA F2: `kern security` findings and `kern review` risk must agree;
	// a hard CI failure remains `kern verify --types security`'s FAIL → 1).
	// The summary line prints only in text mode: --json keeps stderr empty
	// so machine consumers can still tell findings (exit 3, empty stderr)
	// from tool errors (exit 1, stderr populated).
	if counts["error"] > 0 {
		if f.json {
			panic(exitError{code: 3})
		}
		fatalPolicy("sec: %d error-severity finding(s)", counts["error"])
	}
}

// runTaint implements `kern taint [root] [--file f] [--range a..b] [--generate]`:
// taint-lite analysis over security findings. Each finding's containing
// function is marked tainted when it is transitively called by a framework
// entry point or its file contains a source expression; --generate appends a
// test scaffold per tainted sink (go test for Go sinks, pytest for Python
// sinks). --range scopes findings to the files changed in a git range
// (".." = working tree). The MCP tool kern_taint is the primary surface (this
// thin CLI form exists so the opencode plugin can reach the same check).
func runTaint(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
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
		// F22: `kern taint --file /nonexistent` used to report "no security
		// findings" (rc=0) — a false clean bill for a path that does not
		// exist. When --file matches nothing, distinguish a missing file
		// (error rc=1) from a file that exists but is clean (rc=0, explicit
		// "no findings for <file>").
		if len(findings) == 0 {
			if !taintFileExists(root, f.file) {
				fatal("kern taint: file not found: %s", f.file)
			}
			fmt.Printf("no findings for %s\n", f.file)
			return
		}
	}
	// --range scopes findings to the files changed in a git range.
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
// from and to ("..") means the working tree.
func parseTaintRange(r string) (from, to string, err error) {
	parts := strings.Split(r, "..")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid range %q: want <from>..<to>", r)
	}
	return parts[0], parts[1], nil
}

// taintFileExists reports whether --file names a real file: present on disk
// under root (or absolute) or known to the index. Used to tell a false clean
// bill ("no security findings" for a path that does not exist) apart from a
// genuinely clean file (F22).
func taintFileExists(root, file string) bool {
	if file == "" {
		return false
	}
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, file)
	}
	if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
		return true
	}
	if ix, err := loadOrBuild(root); err == nil {
		for _, p := range ix.Pkgs {
			for _, f := range p.Files {
				if f == file {
					return true
				}
			}
		}
	}
	return false
}

func runDelete(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern delete <symbol> [root] [--apply] [--json]")
	}
	sym := args[0]
	root := projectRoot(f)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("delete: %v", err)
	}
	if f.apply {
		runDeleteApply(root, ix, sym, f.json, f.force)
		return
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
		fatal("delete: %s is not safe to delete — see output above", sym)
	}

}

// runDeleteApply commits the deletion when the gate sanctions it: the
// symbol's declaration and its test-only callers are removed, backed up
// under .kern/rename-backup/ (rename.Apply's transactional machinery), and
// the index rebuilds automatically on the next load. A HIGH pre-edit
// verdict blocks unless force is set.
func runDeleteApply(root string, ix *index.Index, sym string, asJSON bool, force bool) {
	if !force {
		if msg := intel.AssessEditRisk(ix, "", sym).Refusal(fmt.Sprintf("delete --apply of %s", sym)); msg != "" {
			fatal("%s", msg)
		}
	}
	plan, err := remove.Plan(ix, sym)
	if err != nil {
		fatal("delete: %v", err)
	}
	n, err := rename.Apply(root, plan)
	if err != nil {
		fatal("delete --apply: %v (all files restored)", err)
	}
	if asJSON {
		printJSON(plan)
		return
	}
	fmt.Printf("kern delete: %d edit(s) applied; backup at %s; index will rebuild automatically\n", n, plan.Backup)
}

func runRename(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 2 {
		fatalUsage("usage: kern rename <old> <new> [root] [--apply] [--json]")
	}
	oldName, newName := args[0], args[1]
	root := projectRoot(f)
	if f.root == "" && len(args) > 2 {
		root = args[2]
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("rename: %v", err)
	}
	rep, err := rename.Rename(ix, oldName, newName)
	if err != nil {
		fatal("rename: %v", err)
	}
	if !f.apply {
		// Preview: report only, nothing touches disk.
		if f.json {
			printJSON(rep)
			return
		}
		fmt.Println(rename.Render(rep))
		return
	}
	// Apply path. QA 2026-09-23 (P0): this used to set rep.Applied = true
	// before rendering, to suppress the "run with --apply to commit" hint —
	// but rename.Apply treats r.Applied as already-committed and returned
	// (0, nil) without touching disk, so every `kern rename --apply` was a
	// silent no-op with a success message. Order is now gate → Apply →
	// render; Apply itself sets rep.Applied on success, so Render shows the
	// renamed report, not the stale hint.
	if !f.force {
		if msg := intel.AssessEditRisk(ix, "", oldName).Refusal(fmt.Sprintf("rename --apply of %s", oldName)); msg != "" {
			fatal("%s", msg)
		}
	}
	n, err := rename.Apply(root, rep)
	if err != nil {
		fatal("apply failed (files restored): %v", err)
	}
	if f.json {
		printJSON(rep)
		return
	}
	fmt.Println(rename.Render(rep))
	fmt.Printf("kern rename: %d edits applied; index will rebuild automatically\n", n)
	if rep.Backup != "" {
		fmt.Printf("kern rename: backup at %s\n", rep.Backup)
	}
}
