package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/relay"
	jsonschema "github.com/JayveerPrajapati/kern/internal/schema"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/verify"
	"os"
	"strconv"
	"strings"
)

func (s *Server) handleMaskPII(ctx context.Context, args map[string]any) (string, error) {
	{
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		var names []string
		for _, n := range strings.Split(argString(args, "mask_names"), ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		res := pii.MaskAllCustom(text, pii.DefaultPatterns, names)
		var parts []string
		for k, v := range res.ByLabel {
			parts = append(parts, fmt.Sprintf("%s %d", k, v))
		}
		summary := "masked " + strconv.Itoa(res.Replaced) + " secrets"
		if len(parts) > 0 {
			summary += ": " + strings.Join(parts, ", ")
		}
		return res.Text + "\n[kern] " + summary + "\n", nil

	}
}

func (s *Server) handleSecurity(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		var allow []string
		if s := argString(args, "severity"); s != "" {
			allow = strings.Split(s, ",")
		}
		max := 100
		if v := argString(args, "max"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("max: invalid integer %q", v)
			}
			if n > 0 {
				max = n
			}
		}
		findings, serr := sec.Scan(root)
		if serr != nil {
			return "", fmt.Errorf("security scan failed: %w", serr)
		}
		findings = sec.FilterBySeverity(findings, allow)
		if argString(args, "format") == "json" {
			var b strings.Builder
			if err := json.NewEncoder(&b).Encode(findings); err != nil {
				return "", fmt.Errorf("encode findings: %w", err)
			}
			return b.String(), nil
		}
		if len(findings) == 0 {
			return "no security findings", nil
		}
		out := sec.Render(findings, max)
		counts := sec.Counts(findings)
		out += fmt.Sprintf("[kern] %d findings: %d error, %d warning, %d info\n",
			len(findings), counts["error"], counts["warning"], counts["info"])
		return out, nil
	}
}

// handleTaint implements kern_taint: taint-lite analysis over security
// findings. Each finding's containing function is marked tainted when it is
// transitively called by a framework entry point (Symbol.Entry) or its file
// contains a source expression; with generate=true, a deterministic test
// scaffold (go test for Go sinks, pytest for Python sinks, G-4) is appended
// per tainted sink for the caller to fill. The optional range argument
// scopes findings to the files changed in a "from..to" git range.
func (s *Server) handleTaint(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		fileFilter := argString(args, "file")
		generate := argBool(args, "generate")
		rng := argString(args, "range")

		findings, serr := sec.Scan(root)
		if serr != nil {
			return "", fmt.Errorf("security scan failed: %w", serr)
		}
		if fileFilter != "" {
			filtered := findings[:0]
			for _, f := range findings {
				if f.File == fileFilter {
					filtered = append(filtered, f)
				}
			}
			findings = filtered
		}
		// G-4: scope findings to the files changed in a git range
		// ("from..to", ".." = working tree). Combined with fileFilter the
		// two filters intersect.
		scopeNote := ""
		if rng != "" {
			parts := strings.Split(rng, "..")
			if len(parts) != 2 {
				return "", fmt.Errorf("invalid range %q: want <from>..<to>", rng)
			}
			files, gerr := intel.FilesForRange(root, parts[0], parts[1])
			if gerr != nil {
				return "", fmt.Errorf("range lookup failed: %w", gerr)
			}
			scope := fmt.Sprintf("range %s..%s", parts[0], parts[1])
			if parts[0] == "" && parts[1] == "" {
				scope = "worktree"
			}
			scopeNote = fmt.Sprintf("scope: %d file(s) changed in %s\n", len(files), scope)
			findings = sec.FilterByFiles(findings, files)
		}
		ix, _ := s.loadIndex(ctx, root)
		tainted := sec.TaintLite(ix, findings)
		if len(tainted) == 0 {
			return scopeNote + "no security findings", nil
		}
		var b strings.Builder
		b.WriteString(scopeNote)
		for _, tf := range tainted {
			fmt.Fprintf(&b, "%s:%d [%s] %s — %s\n", tf.File, tf.Line, tf.Severity, tf.Rule, tf.Message)
			if tf.Tainted {
				b.WriteString("  tainted: yes")
				if tf.EntryPoint != "" {
					fmt.Fprintf(&b, " (via %s: path %s)", tf.EntryPoint, strings.Join(tf.Path, " → "))
				}
				b.WriteString("\n")
			} else {
				b.WriteString("  tainted: no\n")
			}
			if generate && tf.Tainted {
				sc := sec.ScaffoldFor(tf)
				lang := "go"
				if strings.HasSuffix(strings.ToLower(tf.File), ".py") || strings.HasPrefix(tf.Rule, "py-") {
					lang = "python"
				}
				fmt.Fprintf(&b, "# write to: %s\n```%s\n%s\n```\n", sc.File, lang, sc.Code)
			}
		}
		return b.String(), nil
	}
}

func (s *Server) handleSafeDelete(ctx context.Context, args map[string]any) (string, error) {
	{
		sym := argString(args, "symbol")
		if sym == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(ctx, argString(args, "root"))
		if err != nil {
			return "", err
		}
		r := intel.DeleteCheck(ix, sym)
		if argString(args, "format") == "json" {
			data, err := json.Marshal(r)
			if err != nil {
				return "", err
			}
			return string(data), nil
		}
		return intel.RenderDelete(r), nil

	}
}

func (s *Server) handleSchemaValidate(ctx context.Context, args map[string]any) (string, error) {
	{
		data := argString(args, "data")
		sc := argString(args, "schema")
		if data == "" || sc == "" {
			return "", fmt.Errorf("data and schema are required")
		}
		s, err := jsonschema.Parse(sc)
		if err != nil {
			return "", err
		}
		vs := s.Validate([]byte(data))
		if len(vs) == 0 {
			return "schema OK: output conforms", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "schema violations (%d):\n", len(vs))
		for _, v := range vs {
			fmt.Fprintln(&b, "  - "+v)
		}
		return b.String(), nil

	}
}

func (s *Server) handleVerifyOutput(ctx context.Context, args map[string]any) (string, error) {
	{
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		root := argString(args, "root")
		ix, err := s.loadIndex(ctx, root)
		if err != nil {
			// Without a usable index the reference checks cannot run; surface
			// the error instead of emitting a false-positive MISS report.
			return "", fmt.Errorf("cannot verify: index unavailable for %q: %w", root, err)
		}
		rep := verify.Sorted(verify.Verify(ix, root, text))
		return verify.Render(rep), nil

	}
}

func (s *Server) handleCheckDraft(ctx context.Context, args map[string]any) (string, error) {
	{
		code := argString(args, "code")
		if code == "" {
			return "", fmt.Errorf("code is required")
		}
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		lang := argString(args, "lang")
		ix, err := s.loadIndex(ctx, root)
		if err != nil {
			// Without a usable index the symbol checks cannot run; surface the
			// error instead of emitting a false-positive clean verdict.
			return "", fmt.Errorf("cannot check draft: index unavailable for %q: %w", root, err)
		}
		findings := verify.CheckDraft(ix, root, []byte(code), lang)
		if len(findings) == 0 {
			return "OK: draft validates cleanly — no issues found", nil
		}
		var b strings.Builder
		for _, f := range findings {
			fmt.Fprintf(&b, "draft.go:%d [%s] %s\n", f.Line, f.Kind, f.Message)
		}
		fmt.Fprintf(&b, "%d issue(s) found", len(findings))
		return b.String(), nil

	}
}

func (s *Server) handleGuardCheck(ctx context.Context, args map[string]any) (string, error) {
	{
		changes, ix, err := s.changedContext(ctx, args)
		if err != nil {
			return "", err
		}
		if len(changes) == 0 {
			return "no changed files (use file= or range=, or make edits)", nil
		}
		files := make([]string, 0, len(changes))
		for _, c := range changes {
			files = append(files, c.File)
		}
		root := resolveRoot(argString(args, "root"))
		b, err := intel.LoadBoundaries(root)
		if err != nil {
			return "", err
		}
		violations, skipped := intel.CheckBoundariesPrecise(ix, b, files, false)
		// @pure mutability assertions are opt-in via "pure": true in
		// .kern/boundaries.json; a nil ruleset carries no Pure flag, so the
		// check is skipped when the guard is not configured.
		if b != nil && b.Pure {
			violations = append(violations, intel.CheckPurity(ix, files)...)
		}
		// Publish guard outcomes exactly like the CLI guard check: persisted
		// to .kern/events.jsonl for replay and, when a relay owns the socket,
		// emitted live to watchers. Best-effort; never changes the verdict.
		relay.PublishPersisted(root, intel.GuardEvents(violations, skipped["boundaries-not-configured"] > 0))
		threshold := 0
		if v := argString(args, "threshold"); v != "" {
			n, err := atoiArg(v, threshold)
			if err != nil {
				return "", err
			}
			threshold = n
		}
		// threshold=-1 means "never reject" (audit only).
		if threshold >= 0 && len(violations) > threshold {
			return "", fmt.Errorf("REJECT: %d boundary violations exceed threshold %d", len(violations), threshold)
		}
		if argString(args, "format") == "sarif" {
			return intel.RenderViolationsSARIF(violations, serverVersion), nil
		}
		out := intel.RenderViolations(violations)
		// A missing boundaries file is not a silent pass: surface the gap as a
		// visible WARN ahead of the verdict.
		if n := skipped["boundaries-not-configured"]; n > 0 {
			out = fmt.Sprintf("WARN: no boundary rules configured (.kern/boundaries.json not found) — architecture guard NOT enforced; %d files unchecked\n%s", n, out)
		}
		return out, nil

	}
}
