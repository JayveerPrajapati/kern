package verifycmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

const (
	defaultCommandTimeout = 10 * time.Minute
	maxCommandTimeout     = 30 * time.Minute
	maxCapturedOutput     = 8 << 20
)

// Verify is the server path for kern_verify command mode: same runner as
// VerifyCommand, but scoped to the server's workspace roots (root confinement
// and the command audit still apply). The MCP handler calls this directly.
func Verify(ctx context.Context, roots []string, args map[string]any) (string, error) {
	out, _, err := verifyCommandCore(ctx, roots, args)
	return out, err
}

// VerifyCommand is kern_verify's command mode for callers without a server
// (the CLI). It returns the shaped text and the command's own exit code.
func VerifyCommand(ctx context.Context, args map[string]any) (string, int, error) {
	return verifyCommandCore(ctx, nil, args)
}

func verifyCommandCore(ctx context.Context, roots []string, args map[string]any) (string, int, error) {
	mode := mcpargs.ArgString(args, "output")
	root, err := commandRoot(roots, mcpargs.ArgString(args, "root"))
	if err != nil {
		return "", 0, err
	}

	if id := strings.TrimSpace(mcpargs.ArgString(args, "anchor")); id != "" {
		full, err := loadRun(root, id)
		if err != nil {
			return "", 0, fmt.Errorf("anchor %s not found: rerun the command", id)
		}
		out, err := shapeOutput(full, 0, 0, id, mode)
		return out, 0, err
	}

	command := strings.TrimSpace(mcpargs.ArgString(args, "command"))
	if err := classifyCommand(command, detectToolchains(root)); err != nil {
		return "", 0, fmt.Errorf("kern_verify command refused: %w", err)
	}
	if err := governance.CheckExecCommand(command, root, "kern_verify"); err != nil {
		return "", 0, err
	}

	res := runCommand(ctx, root, command, commandTimeout(args))
	recordCommandAudit(root, command, res)

	anchor := optimize.StoreAnchor(res.output)
	saveRun(root, anchor, res.output)
	out, err := shapeOutput(res.output, res.exit, res.dur, anchor, mode)
	if err != nil {
		return "", 0, err
	}
	if res.timedOut {
		out = "TIMED OUT after " + res.dur.Round(time.Second).String() + "\n" + out
	}
	return out, res.exit, nil
}

const maxSavedRuns = 20

func runsDir(root string) string { return filepath.Join(root, ".kern", "runs") }

// saveRun keeps the full output on disk so an anchor outlives the server
// process and works from the CLI. Only the newest maxSavedRuns are kept.
func saveRun(root, anchor, output string) {
	if anchor == "" {
		return
	}
	dir := runsDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, anchor+".log"), []byte(output), 0o600)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= maxSavedRuns {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		a, _ := entries[i].Info()
		b, _ := entries[j].Info()
		return a != nil && b != nil && a.ModTime().Before(b.ModTime())
	})
	for _, e := range entries[:len(entries)-maxSavedRuns] {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// loadRun returns a kept run: the in-memory anchor first, then the saved file.
func loadRun(root, anchor string) (string, error) {
	if full, err := optimize.FetchAnchor(anchor); err == nil {
		return full, nil
	}
	if !strings.HasPrefix(anchor, "anchor-") || strings.ContainsAny(anchor, `/\.`) {
		return "", fmt.Errorf("invalid anchor")
	}
	b, err := os.ReadFile(filepath.Join(runsDir(root), anchor+".log"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// commandRoot resolves the directory a command runs in, confined to the
// server's workspace roots when any are configured.
func commandRoot(roots []string, arg string) (string, error) {
	root := strings.TrimSpace(arg)
	if root == "" && len(roots) > 0 {
		root = roots[0]
	}
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if arg != "" && len(roots) > 0 && !withinAny(abs, roots) {
		return "", fmt.Errorf("root %s is outside the workspace roots", abs)
	}
	return abs, nil
}

func withinAny(path string, roots []string) bool {
	for _, r := range roots {
		ar, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(ar, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func commandTimeout(args map[string]any) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(mcpargs.ArgString(args, "timeout")))
	if err != nil || secs <= 0 {
		return defaultCommandTimeout
	}
	d := time.Duration(secs) * time.Second
	if d > maxCommandTimeout {
		return maxCommandTimeout
	}
	return d
}

type commandRun struct {
	output   string
	exit     int
	dur      time.Duration
	timedOut bool
}

// capBuffer keeps at most maxCapturedOutput bytes and drops the rest.
type capBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	room := maxCapturedOutput - c.buf.Len()
	if room <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func runCommand(ctx context.Context, root, command string, timeout time.Duration) commandRun {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "sh", "-c", command)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	var out capBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	start := time.Now()
	err := cmd.Run()
	res := commandRun{output: out.buf.String(), dur: time.Since(start)}
	if out.truncated {
		res.output += "\n... output capped at 8 MiB"
	}
	switch {
	case err == nil:
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		res.exit, res.timedOut = -1, true
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.exit = ee.ExitCode()
		} else {
			res.exit = -1
			res.output += "\n" + err.Error()
		}
	}
	return res
}

// recordCommandAudit appends the run to the project's governance audit chain
// so every command kern_verify runs stays traceable. Best-effort.
func recordCommandAudit(root, command string, res commandRun) {
	if len(command) > 200 {
		command = command[:200] + "..."
	}
	auditDir := filepath.Join(root, ".kern", "audit")
	l := governance.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock"))
	_ = l.AppendExternal(governance.AuditEntry{
		AgentID:   "kern_verify",
		Action:    "exec",
		Resource:  command,
		Approved:  true,
		Result:    fmt.Sprintf("exit %d", res.exit),
		Policy:    "verify-command",
		Timestamp: time.Now(),
	})
}
