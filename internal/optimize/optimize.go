// Package optimize orchestrates prompt compression, log stripping, code
// summarization and build result compaction, recording before/after stats.
package optimize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/compress"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/semcache"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Result is the outcome of an optimization.
type Result struct {
	Output       string
	BeforeTokens int
	AfterTokens  int
	SavedTokens  int
	SavedPercent float64
	// FromCache is set when the result was served from the local response
	// cache instead of recomputing (or calling the LLM).
	FromCache bool
	// SemanticHit is set when the result came from the fuzzy cache: a prior
	// *similar* (not identical) input produced it. MatchedInput is that input
	// and Similarity its Jaccard overlap, so callers can show what was matched.
	SemanticHit  bool
	MatchedInput string
	Similarity   float64
	// BeforeBytes is the measured byte length of the raw input text (not an
	// estimate reconstructed from the output).
	BeforeBytes int
	// AfterBytes is the measured byte length of the output text.
	AfterBytes int
}

// Options for an optimization run.
type Options struct {
	Session string
	Model   string
	Source  string
	// LLM optionally names a local Ollama model for the prompt step. When
	// empty or unreachable, deterministic compression is used.
	LLM string
	// Mask strips secrets and PII from the prompt before it is compressed or
	// sent to an LLM, then restores placeholders in the returned output.
	Mask      bool
	MaskNames []string // extra client/project identifiers to mask as [MASKED_NAME_N]
	// Cache persists prompt->result in the local cache dir so identical
	// requests (same prompt, log, model) are served instantly without
	// recomputing or re-calling the LLM.
	Cache bool
	// FewShot injects the top recalled lessons from project memory as
	// "baseline examples" into the prompt before compression, so past analysis
	// of similar problems steers the current one.
	FewShot bool
	// Root selects the project memory store for FewShot. Defaults to the
	// current working directory.
	Root string
}

// Recorder is the stats sink. It is nilable for pure/dry-run usage.
var Recorder *stats.Recorder

// EnsureRecorder wires the shared stats recorder used by optimize operations.
// It is the canonical wiring — cmd/kern, mcp and other entry points delegate
// to it instead of calling stats.NewRecorder themselves.
func EnsureRecorder() error {
	rec, err := stats.NewRecorder()
	if err != nil {
		return err
	}
	Recorder = rec
	return nil
}

func record(op stats.Operation, opts Options, res Result) {
	if Recorder == nil {
		return
	}
	_ = Recorder.Record(stats.Entry{
		Session:      opts.Session,
		Operation:    op,
		Source:       opts.Source,
		Model:        modelOrDefault(opts.Model),
		BeforeTokens: res.BeforeTokens,
		AfterTokens:  res.AfterTokens,
		BeforeBytes:  res.BeforeBytes,
		AfterBytes:   res.AfterBytes,
	})
}

func modelOrDefault(m string) string {
	if m == "" {
		return stats.DefaultModel
	}
	return m
}

func finish(raw, out string, kind tokenize.Kind) Result {
	before := tokenize.CountKind(raw, kind)
	after := tokenize.CountKind(out, kind)
	return Result{
		Output:       out,
		BeforeTokens: before,
		AfterTokens:  after,
		SavedTokens:  before - after,
		SavedPercent: pct(before, after),
		BeforeBytes:  len([]byte(raw)),
		AfterBytes:   len([]byte(out)),
	}
}

func pct(before, after int) float64 {
	if before <= 0 {
		return 0
	}
	return float64(before-after) / float64(before) * 100
}

// Prompt optimizes a raw user prompt, optionally compressing attached log text.
func Prompt(prompt string, attachedLog string, opts Options) (Result, error) {
	if strings.TrimSpace(prompt) == "" && strings.TrimSpace(attachedLog) == "" {
		return Result{}, errors.New("nothing to optimize")
	}
	if opts.Cache {
		// The key covers every option that can change the result: model, LLM
		// choice, FewShot/Root (memory context), and Mask/MaskNames
		// (placeholder remapping). Two calls differing in any of them must not
		// share a cached entry.
		kb, _ := json.Marshal(struct {
			Model     string
			LLM       string
			FewShot   bool
			Root      string
			Mask      bool
			MaskNames []string
			Prompt    string
			Attached  string
		}{
			Model:     modelOrDefault(opts.Model),
			LLM:       opts.LLM,
			FewShot:   opts.FewShot,
			Root:      opts.Root,
			Mask:      opts.Mask,
			MaskNames: opts.MaskNames,
			Prompt:    prompt,
			Attached:  attachedLog,
		})
		key := "queries/" + cache.Hash(kb)
		var cached Result
		if err := cache.Load(key, &cached); err == nil && cached.Output != "" {
			cached.FromCache = true
			return cached, nil
		}
		// Semantic cache: a *similar* prior query returns instantly. Only
		// deterministic results are stored/served (never LLM runs), and the
		// LLM path is skipped entirely so a fuzzy hit can never ship a wrong
		// model's answer.
		if opts.LLM == "" {
			var sem Result
			raw := prompt + "\x00" + attachedLog
			if matched, sim, hit, serr := semcache.Lookup("prompt", raw, &sem, 0); serr == nil && hit {
				sem.FromCache = true
				sem.SemanticHit = true
				sem.MatchedInput = matched
				sem.Similarity = sim
				return sem, nil
			}
		}
		res, err := promptUncached(prompt, attachedLog, opts)
		if err != nil {
			return res, err
		}
		_ = cache.Store(key, res)
		if opts.LLM == "" {
			_ = semcache.Store("prompt", prompt+"\x00"+attachedLog, res)
		}
		return res, nil
	}
	return promptUncached(prompt, attachedLog, opts)
}

func promptUncached(prompt string, attachedLog string, opts Options) (Result, error) {
	raw := prompt
	if attachedLog != "" {
		logPart := compress.CompressLog(attachedLog, compress.Options{MaxLines: 200, Cluster: true})
		raw = prompt + "\n\n--- attached log ---\n" + attachedLog
		prompt = prompt + "\n\n--- attached log (compressed) ---\n" + logPart
	}
	// Mask secrets by default when the prompt will be sent to a non-local LLM:
	// a remote OLLAMA_HOST, or any non-Ollama provider selected via
	// KERN_LLM_PROVIDER. Otherwise the compression step would ship PII off-box.
	// Explicit mask=false still wins.
	mask := opts.Mask
	if !mask && opts.LLM != "" {
		if p, perr := llm.NewProvider(); perr == nil {
			if _, isOllama := p.(*llm.OllamaProvider); isOllama {
				mask = !isLocalHost(llm.New(opts.LLM).Base)
			} else {
				mask = true // openai/anthropic/google are always remote
			}
		}
	}
	var masked pii.Result
	if mask {
		masked = pii.MaskNames(prompt, opts.MaskNames)
		prompt = masked.Text
	}
	if opts.FewShot {
		root := opts.Root
		if root == "" {
			root, _ = os.Getwd()
		}
		if ex := memory.Recall(root, prompt, 2); len(ex) > 0 {
			var b strings.Builder
			b.WriteString(prompt)
			b.WriteString("\n\nRelevant lessons already learned in this project (apply as baselines):\n")
			for i, e := range ex {
				fmt.Fprintf(&b, "[baseline %d] %s\n", i+1, e.Text)
			}
			prompt = b.String()
		}
	}
	out := compress.CompressPrompt(prompt)
	if opts.LLM != "" {
		// Provider-neutral compression: the factory selects the vendor via
		// KERN_LLM_PROVIDER (default Ollama), and opts.LLM overrides the model.
		if p, perr := llm.NewProvider(); perr == nil {
			if llmOut, err := llm.CompressVia(context.Background(), p, prompt, llm.Options{Model: opts.LLM}); err == nil && llmOut != "" {
				out = llmOut
			}
		}
	}
	if mask {
		out = masked.Unmask(out)
	}
	res := finish(raw, out, tokenize.KindGeneric)
	record(stats.OpOptimizePrompt, opts, res)
	return res, nil
}

// Log compresses log text to its essential lines.
func Log(text string, opts Options) (Result, error) {
	if strings.TrimSpace(text) == "" {
		return Result{}, errors.New("empty log")
	}
	if opts.Cache {
		key := "logs/" + cache.Hash([]byte(text))
		var cached Result
		if err := cache.Load(key, &cached); err == nil && cached.Output != "" {
			cached.FromCache = true
			return cached, nil
		}
		var sem Result
		if matched, sim, hit, serr := semcache.Lookup("log", text, &sem, 0); serr == nil && hit {
			sem.FromCache = true
			sem.SemanticHit = true
			sem.MatchedInput = matched
			sem.Similarity = sim
			return sem, nil
		}
		res := finish(text, compress.CompressLog(text, compress.Options{MaxLines: 200, Cluster: true}), tokenize.KindLog)
		record(stats.OpOptimizeLog, opts, res)
		_ = cache.Store(key, res)
		_ = semcache.Store("log", text, res)
		return res, nil
	}
	out := compress.CompressLog(text, compress.Options{MaxLines: 200, Cluster: true})
	res := finish(text, out, tokenize.KindLog)
	record(stats.OpOptimizeLog, opts, res)
	return res, nil
}

// RunBuild executes a command locally and returns only the compact result. The
// command runs through the platform shell so `&&`, pipes and quoting work as
// expected (sh -c on unix, cmd /c on Windows). The context cancels the child
// process (killing it) when the caller aborts, so a cancelled MCP request never
// leaks a background build.
func RunBuild(ctx context.Context, command string, dir string, opts Options) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, errors.New("empty command")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	shell, flag := "sh", "-c"
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/c"
	}
	cmd := exec.CommandContext(ctx, shell, flag, command)
	cmd.Dir = dir
	// The shell wraps the real command, so cancelling the context kills only
	// the shell; its children survive as orphans and hold the output pipes
	// open, which makes CombinedOutput block until they finish. Run the
	// command in its own process group and kill the whole group on timeout.
	setProcessGroup(cmd)
	pgidDone := make(chan struct{})
	defer close(pgidDone)
	go func() {
		select {
		case <-ctx.Done():
			killProcessGroup(cmd)
		case <-pgidDone:
		}
	}()
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if err != nil {
		outStr += "\n" + err.Error()
	}
	compacted := compactCommandOutput(outStr)
	// The command prefix is part of what the caller receives, so count it on
	// both sides of the token ledger: raw (before) and compacted (after).
	display := "cmd: " + command + "\n" + compacted
	res := finish("cmd: "+command+"\n"+outStr, display, tokenize.KindLog)
	record(stats.OpRunBuild, opts, res)
	return res, err
}

func compactCommandOutput(out string) string {
	lines := strings.Split(out, "\n")
	var keep []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "[info]") || strings.HasPrefix(low, "[debug]") ||
			strings.HasPrefix(low, "downloading") || strings.HasPrefix(low, "progress") {
			continue
		}
		if len(keep) < 60 || strings.Contains(low, "error") || strings.Contains(low, "fail") || strings.Contains(low, "warn") {
			keep = append(keep, t)
		}
	}
	// Always cap — and say so, so a truncated build log is never mistaken for
	// the full output.
	if len(keep) > 120 {
		omitted := len(keep) - 120
		keep = keep[:120]
		keep = append(keep, fmt.Sprintf("… (%d lines omitted)", omitted))
	}
	return strings.Join(keep, "\n")
}

// isLocalHost reports whether base (an Ollama base URL) points at the local
// machine. Anything else (LAN IP, remote host, tunnel) is treated as
// non-local so PII masking can be defaulted on.
func isLocalHost(base string) bool {
	host := base
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}
