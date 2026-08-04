// Package optimize orchestrates prompt compression, log stripping, code
// summarization and build result compaction, recording before/after stats.
package optimize

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/compress"
	"github.com/JayveerPrajapati/kern/internal/llm"
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
}

// Options for an optimization run.
type Options struct {
	Session string
	Model   string
	Source  string
	// LLM optionally names a local Ollama model for the prompt step. When
	// empty or unreachable, deterministic compression is used.
	LLM string
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
	raw := prompt
	if attachedLog != "" {
		logPart := compress.CompressLog(attachedLog, compress.Options{MaxLines: 200})
		raw = prompt + "\n\n--- attached log ---\n" + attachedLog
		prompt = prompt + "\n\n--- attached log (compressed) ---\n" + logPart
	}
	out := compress.CompressPrompt(prompt)
	if opts.LLM != "" {
		if llmOut, err := llm.New(opts.LLM).Compress(prompt); err == nil && llmOut != "" {
			out = llmOut
		}
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
	return res, nil
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
