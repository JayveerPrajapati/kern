package contextcli

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/classparity"
	"github.com/JayveerPrajapati/kern/internal/guardpaths"
)

// runGuardParity implements `kern guard parity [--json] [--root DIR]`.
//
// kern classifies commands in two independent places: the bash-guard oracle
// (guardrule, enforced by the PreToolUse hook and the opencode plugin) and the
// governed-exec oracle (execclassify, enforced by kern_exec and `kern build`).
// A command dangerous in only one of them is a bypass — route it through the
// other surface and it runs ungoverned. This command runs the shared corpus
// through both and reports every disagreement as a finding (exit 1).
func runGuardParity(rest []string) {
	jsonOut := false
	root := ""
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--root":
			if i+1 >= len(rest) {
				fatalUsage("guard parity: --root requires a value")
			}
			i++
			root = rest[i]
		case strings.HasPrefix(arg, "--root="):
			root = strings.TrimPrefix(arg, "--root=")
		case strings.HasPrefix(arg, "-"):
			fatalUsage("guard parity: unknown flag %s", arg)
		default:
			root = arg
		}
	}
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			fatal("guard parity: %v", err)
		}
		root = wd
	}
	checked, diffs := classparity.ClassifierParity(root, guardpaths.ExtraRootsFromEnv(os.Getenv))
	if jsonOut {
		printJSON(map[string]any{
			"root":        root,
			"checked":     checked,
			"divergences": diffs,
			"ok":          len(diffs) == 0,
		})
		if len(diffs) > 0 {
			fatal("guard parity: %d classifier divergence(s) — see JSON output above", len(diffs))
		}
		return
	}
	for _, d := range diffs {
		fmt.Println("DIVERGE " + d.String())
	}
	if len(diffs) > 0 {
		fatal("guard parity: %d of %d corpus commands diverge between the bash-guard and governed-exec classifiers", len(diffs), checked)
	}
	fmt.Printf("guard parity: %d corpus commands agree (bash-guard == governed-exec)\n", checked)
}
