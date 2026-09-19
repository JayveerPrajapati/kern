// Package security owns the security-family MCP tool bodies (kern_mask_pii,
// kern_security, kern_taint, kern_safe_delete, kern_schema_validate,
// kern_verify_output, kern_check_draft, kern_guard_check) as plain functions.
package security

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/relay"
	jsonschema "github.com/JayveerPrajapati/kern/internal/schema"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/service"
	"github.com/JayveerPrajapati/kern/internal/verify"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	LoadIndex      func(ctx context.Context, root string) (*index.Index, error)
	ChangedContext func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error)
	SecuritySvc    service.SecurityService
	ServerVersion  string
}

func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// MaskPII sanitizes sensitive PII and secrets from text.
func MaskPII(ctx context.Context, secSvc service.SecurityService, args map[string]any) (string, error) {
	text := mcpargs.ArgString(args, "text")
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	var names []string
	for _, n := range strings.Split(mcpargs.ArgString(args, "mask_names"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	res, err := secSvc.Mask(ctx, text, names)
	if err != nil {
		return "", err
	}
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

// Scan performs a static security audit of source code files.
func Scan(ctx context.Context, secSvc service.SecurityService, args map[string]any) (string, error) {
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	var allow []string
	if s := mcpargs.ArgString(args, "severity"); s != "" {
		allow = strings.Split(s, ",")
	}
	maxN := 100
	if v := mcpargs.ArgString(args, "max"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("max: invalid integer %q", v)
		}
		if n > 0 {
			maxN = n
		}
	}
	findings, serr := secSvc.Scan(ctx, root)
	if serr != nil {
		return "", fmt.Errorf("security scan failed: %w", serr)
	}
	findings = secSvc.FilterBySeverity(findings, allow)
	if mcpargs.ArgString(args, "format") == "json" {
		var b strings.Builder
		if err := json.NewEncoder(&b).Encode(findings); err != nil {
			return "", fmt.Errorf("encode findings: %w", err)
		}
		return b.String(), nil
	}
	if len(findings) == 0 {
		return "no security findings", nil
	}
	out := secSvc.Render(findings, maxN)
	counts := sec.Counts(findings)
	out += fmt.Sprintf("[kern] %d findings: %d error, %d warning, %d info\n",
		len(findings), counts["error"], counts["warning"], counts["info"])
	return out, nil
}

// Taint tracks taint propagation from external inputs to sensitive sinks.
func Taint(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	fileFilter := mcpargs.ArgString(args, "file")
	generate := mcpargs.ArgBool(args, "generate")
	rng := mcpargs.ArgString(args, "range")

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
	ix, _ := h.LoadIndex(ctx, root)
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

// SafeDelete checks whether a symbol can be safely deleted without breaking callers.
func SafeDelete(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	sym := mcpargs.ArgString(args, "symbol")
	if sym == "" {
		return "", fmt.Errorf("symbol is required")
	}
	ix, err := h.LoadIndex(ctx, mcpargs.ArgString(args, "root"))
	if err != nil {
		return "", err
	}
	r := intel.DeleteCheck(ix, sym)
	if mcpargs.ArgString(args, "format") == "json" {
		data, err := json.Marshal(r)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return intel.RenderDelete(r), nil
}

// SchemaValidate validates structured JSON data against a JSON schema.
func SchemaValidate(ctx context.Context, args map[string]any) (string, error) {
	data := mcpargs.ArgString(args, "data")
	sc := mcpargs.ArgString(args, "schema")
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

// VerifyOutput checks symbol and reference consistency in agent prose.
func VerifyOutput(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	text := mcpargs.ArgString(args, "text")
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	root := mcpargs.ArgString(args, "root")
	ix, err := h.LoadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("cannot verify: index unavailable for %q: %w", root, err)
	}
	rep := verify.Sorted(verify.Verify(ix, root, text))
	return verify.Render(rep), nil
}

// CheckDraft validates draft code snippets for unexported accesses and undefined symbols.
func CheckDraft(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	code := mcpargs.ArgString(args, "code")
	if code == "" {
		return "", fmt.Errorf("code is required")
	}
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	lang := mcpargs.ArgString(args, "lang")
	ix, err := h.LoadIndex(ctx, root)
	if err != nil {
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

// GuardCheck verifies changes against configured architectural boundaries.
func GuardCheck(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	changes, ix, err := h.ChangedContext(ctx, args)
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
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	b, err := intel.LoadBoundaries(root)
	if err != nil {
		return "", err
	}
	unconfigured := b == nil
	if b == nil {
		b = intel.InferBoundaries(ix)
	}
	violations, skipped := intel.CheckBoundariesPrecise(ix, b, files, false)
	if b != nil && b.Pure {
		violations = append(violations, intel.CheckPurity(ix, files)...)
	}
	relay.PublishPersisted(root, intel.GuardEvents(violations, unconfigured || skipped["boundaries-not-configured"] > 0))
	threshold := 0
	if v := mcpargs.ArgString(args, "threshold"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", err
		}
		threshold = n
	}
	if threshold >= 0 && len(violations) > threshold {
		return "", fmt.Errorf("rejected: %d boundary violations exceed threshold %d", len(violations), threshold)
	}
	if mcpargs.ArgString(args, "format") == "sarif" {
		return intel.RenderViolationsSARIF(violations, h.ServerVersion), nil
	}
	out := intel.RenderViolations(violations)
	if n := skipped["boundaries-not-configured"]; n > 0 {
		out = fmt.Sprintf("WARN: no boundary rules configured (.kern/boundaries.json not found) — architecture guard NOT enforced; %d files unchecked\n%s", n, out)
	}
	return out, nil
}
