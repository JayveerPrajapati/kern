// Package optimize orchestrates prompt compression, log stripping, code
// summarization and build result compaction, recording before/after stats.
package optimize

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/compress"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/pii"
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
	// "baseline examples" into the prompt before compression, so past
	// analysis of similar problems steers the current one (#6).
	FewShot bool
	// Root selects the project memory store for FewShot. Defaults to the
	// current working directory.
	Root string
}

// Recorder is the stats sink. It is nilable for pure/dry-run usage.
var Recorder *stats.Recorder

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
		BeforeBytes:  len([]byte(res.Output)) + res.BeforeTokens*4,
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
		key := "queries/" + cache.Hash([]byte(modelOrDefault(opts.Model)+"\x00"+prompt+"\x00"+attachedLog))
		var cached Result
		if err := cache.Load(key, &cached); err == nil && cached.Output != "" {
			cached.FromCache = true
			return cached, nil
		}
		res, err := promptUncached(prompt, attachedLog, opts)
		if err != nil {
			return res, err
		}
		_ = cache.Store(key, res)
		return res, nil
	}
	return promptUncached(prompt, attachedLog, opts)
}

func promptUncached(prompt string, attachedLog string, opts Options) (Result, error) {
	raw := prompt
	if attachedLog != "" {
		logPart := compress.CompressLog(attachedLog, compress.Options{MaxLines: 200})
		raw = prompt + "\n\n--- attached log ---\n" + attachedLog
		prompt = prompt + "\n\n--- attached log (compressed) ---\n" + logPart
	}
	var masked pii.Result
	if opts.Mask {
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
		if llmOut, err := llm.New(opts.LLM).Compress(prompt); err == nil && llmOut != "" {
			out = llmOut
		}
	}
	if opts.Mask {
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
	out := compress.CompressLog(text, compress.Options{MaxLines: 200})
	res := finish(text, out, tokenize.KindLog)
	record(stats.OpOptimizeLog, opts, res)
	return res, nil
}

// RunBuild executes a command locally and returns only the compact result.
func RunBuild(command string, dir string, opts Options) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, errors.New("empty command")
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if err != nil {
		outStr += "\n" + err.Error()
	}
	compacted := compactCommandOutput(outStr)
	res := finish(outStr, compacted, tokenize.KindLog)
	res.Output = "cmd: " + command + "\n" + compacted
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
	// Always cap.
	if len(keep) > 120 {
		keep = keep[:120]
	}
	return strings.Join(keep, "\n")
}
