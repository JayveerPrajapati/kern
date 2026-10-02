package sec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/secscan"
)

// EngineStatus is the outcome of an external security engine run.
type EngineStatus string

const (
	// EngineStatusRan: the engine executed and its output parsed (findings may be empty).
	EngineStatusRan EngineStatus = "ran"
	// EngineStatusSkipped: the binary was missing, its output could not be
	// parsed, or it timed out. Skipped is never a hard failure.
	EngineStatusSkipped EngineStatus = "skipped"
)

// GosecResult is the outcome of one gosec run: mapped findings plus the
// engine status. A SKIPPED result carries an actionable Detail.
type GosecResult struct {
	Findings []secscan.Finding
	Status   EngineStatus
	Detail   string
}

const (
	// gosecInstallHint is the Detail stamped when the binary is missing.
	gosecInstallHint = "gosec not installed — go install github.com/securego/gosec/v2/cmd/gosec@latest"
	// gosecDefaultTimeout bounds a run unless KERN_GOSEC_TIMEOUT overrides it.
	gosecDefaultTimeout = 30 * time.Second
)

// GosecAvailable reports whether a gosec binary resolves (KERN_GOSEC or
// PATH). The default "all" engine mode uses it to run gosec only when present.
func GosecAvailable() bool {
	if os.Getenv("KERN_GOSEC") != "" {
		return true
	}
	_, err := exec.LookPath("gosec")
	return err == nil
}

// RunGosec runs `gosec -quiet -json ./...` in root and maps its issues to
// sec.Finding. gosec is an optional external engine (Model C): missing
// binary, unparseable output or timeout degrade to SKIPPED with an
// actionable Detail — never a hard error. HIGH/MEDIUM → warning, LOW → info.
func RunGosec(root string) GosecResult {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return GosecResult{Status: EngineStatusSkipped, Detail: "invalid root"}
	}
	if r, rerr := filepath.EvalSymlinks(absRoot); rerr == nil {
		absRoot = r // normalize /var -> /private/var so Rel is symlink-stable
	}
	bin := os.Getenv("KERN_GOSEC")
	if bin == "" {
		bin = "gosec"
	}
	timeout := gosecTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-quiet", "-json", "./...")
	cmd.Dir = absRoot
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return GosecResult{Status: EngineStatusSkipped, Detail: "timed out after " + timeout.String()}
	}
	// gosec exits 1 when it FINDS issues (stdout still parses). Only an empty-stdout start failure skips.
	if runErr != nil && strings.TrimSpace(stdout.String()) == "" {
		return GosecResult{Status: EngineStatusSkipped, Detail: gosecSkipDetail(stderr.String(), runErr)}
	}
	var out gosecOutput
	if err := json.Unmarshal([]byte(stdout.String()), &out); err != nil {
		return GosecResult{Status: EngineStatusSkipped, Detail: "gosec output could not be parsed: " + clipTail(err.Error(), 200)}
	}
	promoted := gosecPromoted()
	findings := make([]secscan.Finding, 0, len(out.Issues))
	for _, iss := range out.Issues {
		if r, rerr := filepath.EvalSymlinks(iss.File); rerr == nil {
			iss.File = r
		}
		rel, err := filepath.Rel(absRoot, iss.File)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue // outside root or unresolvable: not a root-relative finding
		}
		sev := secscan.SeverityInfo
		switch strings.ToUpper(iss.Severity) {
		case "HIGH", "MEDIUM":
			sev = secscan.SeverityWarning
		}
		if promoted[iss.RuleID] {
			sev = secscan.SeverityError
		}
		line, _ := strconv.Atoi(iss.Line)
		f := secscan.Finding{
			File:     filepath.ToSlash(rel),
			Line:     line,
			Rule:     "gosec:" + iss.RuleID,
			Severity: string(sev),
			Message:  iss.Details,
		}
		if iss.Code != "" {
			f.Snippet = iss.Code
		}
		findings = append(findings, f)
	}
	return GosecResult{Findings: findings, Status: EngineStatusRan}
}

func gosecTimeout() time.Duration {
	if v := os.Getenv("KERN_GOSEC_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return gosecDefaultTimeout
}

// gosecSkipDetail renders the SKIPPED reason (install hint when missing, else the stderr tail).
func gosecSkipDetail(stderr string, runErr error) string {
	if runErr != nil && errors.Is(runErr, exec.ErrNotFound) {
		return gosecInstallHint
	}
	if tail := strings.TrimSpace(stderr); tail != "" {
		return "gosec failed: " + clipTail(tail, 200)
	}
	if runErr != nil {
		return gosecInstallHint + " (gosec failed to start)"
	}
	return gosecInstallHint
}

func gosecPromoted() map[string]bool {
	set := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("KERN_SEC_PROMOTE"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			set[id] = true
		}
	}
	return set
}

// clipTail keeps the last n chars of s (the tail holds the actionable part).
func clipTail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// gosecOutput is the subset of gosec's `-json` document we consume.
type gosecOutput struct {
	Issues []gosecIssue `json:"Issues"`
}

type gosecIssue struct {
	Severity string `json:"severity"`
	RuleID   string `json:"rule_id"`
	Details  string `json:"details"`
	File     string `json:"file"`
	Code     string `json:"code"`
	Line     string `json:"line"`
}
