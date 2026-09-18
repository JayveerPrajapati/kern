package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LocalCliProvider runs a locally-installed agent CLI (Claude Code, Codex,
// Gemini CLI, Qwen Code) to generate text. It is the local-machine fallback
// when no Ollama server is running: kern is already wired into these agents
// via `kern setup`, so their CLIs can serve LLM-dependent features (prompt
// compression, ...) with zero extra configuration — every byte stays on the
// machine and the agent's own auth/session is reused.
type LocalCliProvider struct {
	name    string
	argsFor func(prompt string) []string
	timeout time.Duration
}

// localCliAgents maps agent name -> argv template. The prompt is appended as
// the final argument; each agent's print-mode flag selects non-interactive
// text output.
var localCliAgents = map[string]func(bin string) []string{
	"claude":      func(bin string) []string { return []string{bin, "-p", "--output-format", "text"} },
	"opencode":    func(bin string) []string { return []string{bin, "run"} },
	"codex":       func(bin string) []string { return []string{bin, "exec", "--skip-git-repo-check"} },
	"gemini":      func(bin string) []string { return []string{bin, "-p"} },
	"qwen":        func(bin string) []string { return []string{bin, "-p"} },
	"agy":         func(bin string) []string { return []string{bin, "run"} },
	"antigravity": func(bin string) []string { return []string{bin, "run"} },
}

// cliTimeout returns the per-call timeout for CLI providers
// (KERN_LLM_CLI_TIMEOUT seconds; default 180).
func cliTimeout() time.Duration {
	if v := os.Getenv("KERN_LLM_CLI_TIMEOUT"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 180 * time.Second
}

// NewLocalCliProvider returns a provider that shells out to the named agent
// CLI (claude|opencode|codex|gemini|qwen|agy|antigravity). It never errors: a provider whose binary is
// missing fails loudly at Generate time with an actionable message. Binary
// presence is probed once per call so PATH changes take effect.
func NewLocalCliProvider(name string) *LocalCliProvider {
	tmpl, ok := localCliAgents[name]
	if !ok {
		name = "claude"
		tmpl = localCliAgents[name]
	}
	return &LocalCliProvider{name: name, argsFor: tmpl, timeout: cliTimeout()}
}

// AvailableLocalAgents lists the locally-installed agent CLIs that can serve
// as LLM providers (claude, opencode, codex, gemini, qwen, agy, antigravity), in preference
// order. Opencode and agy are first-class coding agents alongside Claude Code; their
// `run` command is non-interactive and prints the model's answer.
func AvailableLocalAgents() []string {
	var out []string
	for _, name := range []string{"claude", "opencode", "codex", "gemini", "qwen", "agy", "antigravity"} {
		if p, err := exec.LookPath(name); err == nil && p != "" {
			out = append(out, name)
		}
	}
	return out
}

// Installed reports whether the agent's CLI binary is present on PATH.
func (c *LocalCliProvider) Installed() bool {
	_, err := exec.LookPath(c.name)
	return err == nil
}

// Generate shells out to the agent CLI in print mode. The system and user
// prompts are joined (CLI agents have no separate system role). Output is the
// CLI's stdout; a non-zero exit surfaces stderr in the error.
func (c *LocalCliProvider) Generate(ctx context.Context, system, user string, opts Options) (string, error) {
	prompt := user
	if system != "" {
		prompt = system + "\n\n" + user
	}
	argv := append(c.argsFor(c.name), prompt)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	setProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if _, lookErr := exec.LookPath(c.name); lookErr != nil {
			return "", fmt.Errorf("llm: local agent %q is not installed on this machine (install it or set KERN_LLM_PROVIDER; no Ollama server is running either)", c.name)
		}
		return "", fmt.Errorf("llm: %s: %v", c.name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		// Kill the whole process group, not just the direct child: agent
		// CLIs like `opencode run` spawn their own children, and killing
		// only the parent orphaned them (observed PPID 1, CPU-burning,
		// editing files).
		killProcessGroup(cmd)
		<-done
		return "", fmt.Errorf("llm: %s: %v", c.name, ctx.Err())
	case err := <-done:
		if err != nil {
			// Surface whichever stream carries the useful message: agents
			// like claude print "Not logged in · Please run /login" to
			// stdout even on failure.
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = strings.TrimSpace(stdout.String())
			}
			if len(msg) > 300 {
				msg = msg[:300] + "…"
			}
			return "", fmt.Errorf("llm: %s: %v: %s", c.name, err, msg)
		}
	case <-time.After(c.timeout):
		// The select previously had NO timeout case: an agent CLI that
		// hangs (e.g. `opencode run` waiting on a session) blocked the whole
		// chain forever — `kern do` produced zero output for 300s+ (audit
		// M2). Kill the process group and let the chain fall through to the
		// next provider.
		killProcessGroup(cmd)
		<-done
		return "", fmt.Errorf("llm: %s: timed out after %v (set KERN_LLM_CLI_TIMEOUT to adjust)", c.name, c.timeout)
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", fmt.Errorf("llm: %s: empty output", c.name)
	}
	return out, nil
}

// Embed is unsupported: agent CLIs expose no embedding endpoint.
func (c *LocalCliProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("llm: %s: embeddings not supported", c.name)
}

// Stream is unsupported: agent CLIs print complete answers.
func (c *LocalCliProvider) Stream(ctx context.Context, system, user string, opts Options) (*Stream, error) {
	return nil, fmt.Errorf("llm: %s: streaming not supported", c.name)
}

// Capabilities reports Generate-only.
func (c *LocalCliProvider) Capabilities() Capabilities {
	return Capabilities{Generate: true}
}
