package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/pii"
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
		summary := "masked " + itoa(res.Replaced) + " secrets"
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
