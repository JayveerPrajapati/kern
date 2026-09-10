package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/pii"
	jsonschema "github.com/JayveerPrajapati/kern/internal/schema"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/validate"
)

// SecurityService centralizes security operations: scanning a tree for
// hardcoded secrets / dynamic SQL / command injection / weak crypto, masking
// secrets and PII in text, and validating project builds plus JSON output
// against schemas.
type SecurityService interface {
	// Scan runs the security scanner over root and returns every finding.
	Scan(ctx context.Context, root string) ([]sec.Finding, error)
	// FilterBySeverity keeps only findings whose severity is in allow
	// ("error", "warning", "info"). An empty allow keeps everything.
	FilterBySeverity(findings []sec.Finding, allow []string) []sec.Finding
	// Render formats findings for a human reader, capped at max entries.
	Render(findings []sec.Finding, max int) string
	// Mask replaces secrets/PII in text with [MASKED_*] placeholders.
	// names is an optional list of extra identifiers to mask.
	Mask(ctx context.Context, text string, names []string) (pii.Result, error)
	// MaskFile masks the contents of the file at path.
	MaskFile(ctx context.Context, path string) (pii.Result, error)
	// Validate detects (or uses opts.Command) and runs the project's
	// build/test/syntax check under the governance firewall.
	Validate(ctx context.Context, root string, opts ValidateOptions) (*validate.Result, error)
	// SchemaValidate deterministically validates data against the JSON
	// schema spec, returning the list of violations (empty = conforms).
	SchemaValidate(ctx context.Context, data []byte, schemaSpec string) ([]string, error)
}

// ValidateOptions configures SecurityService.Validate.
type ValidateOptions struct {
	// Command overrides auto-detection with an explicit command line
	// (e.g. "go test ./..."). Empty means auto-detect from the tree.
	Command string
	// Timeout bounds the validation run. Zero uses the engine default.
	Timeout time.Duration
}

// securityService is the default SecurityService implementation backed by the
// internal/sec, internal/pii, internal/schema and internal/validate engines.
type securityService struct{}

func newSecurityService() *securityService { return &securityService{} }

func (s *securityService) Scan(ctx context.Context, root string) ([]sec.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sec.Scan(resolveRoot(root))
}

func (s *securityService) FilterBySeverity(findings []sec.Finding, allow []string) []sec.Finding {
	return sec.FilterBySeverity(findings, allow)
}

func (s *securityService) Render(findings []sec.Finding, max int) string {
	return sec.Render(findings, max)
}

func (s *securityService) Mask(ctx context.Context, text string, names []string) (pii.Result, error) {
	if err := ctx.Err(); err != nil {
		return pii.Result{}, err
	}
	return pii.MaskAllCustom(text, pii.DefaultPatterns, names), nil
}

func (s *securityService) MaskFile(ctx context.Context, path string) (pii.Result, error) {
	if err := ctx.Err(); err != nil {
		return pii.Result{}, err
	}
	return pii.MaskFile(path)
}

func (s *securityService) Validate(ctx context.Context, root string, opts ValidateOptions) (*validate.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Validation runs the detected or user-supplied command (arbitrary host
	// code); it must pass the governance firewall, fail closed.
	if err := governance.CheckExec(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	var c *validate.Command
	if opts.Command != "" {
		parts := strings.Fields(opts.Command)
		if len(parts) == 0 {
			return nil, fmt.Errorf("validate: empty --cmd")
		}
		c = &validate.Command{Name: parts[0], Cmd: parts[0], Args: parts[1:]}
	} else {
		var err error
		c, err = validate.Detect(root)
		if err != nil {
			return nil, err
		}
	}
	return validate.Run(ctx, root, c, opts.Timeout), nil
}

func (s *securityService) SchemaValidate(ctx context.Context, data []byte, schemaSpec string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sc, err := jsonschema.Parse(schemaSpec)
	if err != nil {
		return nil, err
	}
	return sc.Validate(data), nil
}
