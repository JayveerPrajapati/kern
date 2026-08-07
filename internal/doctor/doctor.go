// Package doctor runs diagnostics: agent wiring, index health, Ollama
// reachability and stats writability. It reports ok/warn/fail per check.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/setup"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

// Finding is a single diagnostic result.
type Finding struct {
	Check  string
	Level  string // ok, warn, fail
	Detail string
}

// Run executes all checks against the current project root.
func Run(root string) []Finding {
	var out []Finding
	out = append(out, checkBinary())
	out = append(out, checkPath())
	out = append(out, checkExec())
	out = append(out, checkEnv())
	out = append(out, checkWiring(root)...)
	out = append(out, checkIndex(root))
	out = append(out, checkOllama())
	out = append(out, checkStats())
	return out
}

func checkBinary() Finding {
	exe, err := os.Executable()
	if err != nil {
		return Finding{Check: "binary", Level: "fail", Detail: err.Error()}
	}
	return Finding{Check: "binary", Level: "ok", Detail: exe}
}

func checkPath() Finding {
	bin := setup.Bin()
	if _, err := os.Stat(bin); err == nil {
		return Finding{Check: "kern-mcp", Level: "ok", Detail: bin}
	}
	return Finding{Check: "kern-mcp", Level: "warn", Detail: bin + " missing — agents may not find it"}
}

// checkExec actually runs the binary instead of trusting os.Stat. On macOS an
// unsigned/ad-hoc-broken binary passes os.Stat but is killed by Gatekeeper
// with SIGKILL (exit 137); executing it surfaces that immediately. kern-mcp
// has no subcommands, so -h is used: it prints usage and exits 0 without
// reading stdin.
func checkExec() Finding {
	bin := setup.Bin()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-h")
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 137 {
			return Finding{Check: "binary-exec", Level: "fail", Detail: bin + " was killed with SIGKILL (exit 137) — on macOS this is Gatekeeper/codesign; re-sign with `codesign --force --sign -` or reinstall"}
		}
		return Finding{Check: "binary-exec", Level: "fail", Detail: bin + " failed to run: " + err.Error()}
	}
	detail := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if detail == "" {
		detail = "binary responds"
	}
	return Finding{Check: "binary-exec", Level: "ok", Detail: bin + " runs (" + detail + ")"}
}

func checkEnv() Finding {
	var parts []string
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		parts = append(parts, "XDG_CACHE_HOME="+x)
	}
	if m := os.Getenv("KERN_MODEL"); m != "" {
		parts = append(parts, "KERN_MODEL="+m)
	}
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		parts = append(parts, "OLLAMA_HOST="+h)
	}
	if len(parts) == 0 {
		return Finding{Check: "env", Level: "ok", Detail: "defaults in use"}
	}
	return Finding{Check: "env", Level: "ok", Detail: strings.Join(parts, " · ")}
}

func checkWiring(root string) []Finding {
	var out []Finding
	sts := setup.Check(root)
	for _, s := range sts {
		lvl := "ok"
		if !s.Installed {
			lvl = "warn"
		}
		out = append(out, Finding{Check: s.Agent, Level: lvl, Detail: s.Note})
	}
	return out
}

func checkIndex(root string) Finding {
	files, err := os.ReadDir(cache.Path("index"))
	n := 0
	if err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				n++
			}
		}
	}
	detail := fmt.Sprintf("%d cached project indexes", n)
	if ix, err := index.Load(root); err == nil && ix != nil {
		detail = fmt.Sprintf("%d symbols, %d files, %d cached projects", len(ix.Symbols), len(ix.FileHashes), n)
		return Finding{Check: "index", Level: "ok", Detail: detail}
	}
	if ix, err := index.Build(root); err == nil && len(ix.Symbols) > 0 {
		return Finding{Check: "index", Level: "warn", Detail: "no cached index for this project — run `kern index .`"}
	}
	return Finding{Check: "index", Level: "fail", Detail: "no source files indexed in this project"}
}

func checkOllama() Finding {
	c := llm.New("")
	if c.Available() {
		return Finding{Check: "ollama", Level: "ok", Detail: c.Base + " reachable, model " + c.Model}
	}
	return Finding{Check: "ollama", Level: "warn", Detail: c.Base + " not reachable (optional; deterministic compression still works)"}
}

func checkStats() Finding {
	rec, err := stats.NewRecorder()
	if err != nil {
		return Finding{Check: "stats", Level: "fail", Detail: err.Error()}
	}
	s, err := rec.Summarize(7, "")
	if err != nil {
		return Finding{Check: "stats", Level: "warn", Detail: err.Error()}
	}
	return Finding{Check: "stats", Level: "ok", Detail: fmt.Sprintf("%d ops, %d tokens saved (%.1f%%)", s.Operations, s.SavedTotal, s.SavedPct)}
}

// Render formats the findings as a report.
func Render(root string, findings []Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# kern doctor — %s\n\n", root)
	worst := 0 // 0 ok, 1 warn, 2 fail
	rows := make([]string, 0, len(findings))
	for _, f := range findings {
		level := 0
		switch f.Level {
		case "warn":
			level = 1
		case "fail":
			level = 2
		}
		if level > worst {
			worst = level
		}
		rows = append(rows, fmt.Sprintf("[%s] %-22s %s", f.Level, f.Check, f.Detail))
	}
	sort.Strings(rows)
	for _, r := range rows {
		b.WriteString(r)
		b.WriteString("\n")
	}
	verdict := "all good"
	switch worst {
	case 1:
		verdict = "warnings — mostly optional; run `kern setup` and `kern index .`"
	case 2:
		verdict = "failures — fix the [fail] items above"
	}
	fmt.Fprintf(&b, "\nverdict: %s\n", verdict)
	return b.String()
}
