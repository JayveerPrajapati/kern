package risk

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

func changeRequest(source domain.Source, files ...domain.FileChange) domain.ChangeRequest {
	return domain.ChangeRequest{Source: source, Files: files}
}

func fileChange(path string, op domain.Operation, added, removed int) domain.FileChange {
	fc := domain.FileChange{Path: path, Op: op}
	for i := 0; i < added; i++ {
		fc.Added = append(fc.Added, "L")
	}
	for i := 0; i < removed; i++ {
		fc.Removed = append(fc.Removed, "L")
	}
	return fc
}

func TestClassifyLowRisk(t *testing.T) {
	req := changeRequest(domain.SourceHuman,
		fileChange("internal/app/handler.go", domain.OpEdit, 10, 2))
	as := Classify(req, Config{})
	if as.Level != LevelLow {
		t.Fatalf("Level = %q, want %q (reasons: %v)", as.Level, LevelLow, as.Reasons)
	}
	if len(as.Indicators) != 0 {
		t.Fatalf("Indicators = %v, want none", as.Indicators)
	}
}

func TestClassifyLowRiskAgent(t *testing.T) {
	// A plain edit from an agent is still low risk: source alone does not
	// escalate; the surface does.
	req := changeRequest(domain.SourceAgent,
		fileChange("internal/app/handler.go", domain.OpEdit, 3, 1))
	if as := Classify(req, Config{}); as.Level != LevelLow {
		t.Fatalf("Level = %q, want low", as.Level)
	}
}

func TestClassifySensitivePath(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		src   domain.Source
		level Level
	}{
		{"kern dir human", ".kern/boundaries.json", domain.SourceHuman, LevelMedium},
		{"kern dir agent", ".kern/boundaries.json", domain.SourceAgent, LevelHigh},
		{"pem nested agent", "certs/prod/server.pem", domain.SourceAgent, LevelHigh},
		{"key nested", "secrets/private.key", domain.SourceAgent, LevelHigh},
		{"auth dir", "auth/jwt-keys.json", domain.SourceAgent, LevelHigh},
		{"credentials", "config/credentials.yml", domain.SourceHuman, LevelMedium},
		{"secrets file", "config/secrets.local", domain.SourceHuman, LevelMedium},
		{"nested under auth", "api/auth/tokens.json", domain.SourceAgent, LevelHigh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := changeRequest(tc.src, fileChange(tc.path, domain.OpEdit, 1, 0))
			as := Classify(req, Config{})
			if as.Level != tc.level {
				t.Fatalf("Level = %q, want %q (reasons: %v)", as.Level, tc.level, as.Reasons)
			}
			found := false
			for _, ind := range as.Indicators {
				if ind.Kind == "sensitive-path" && ind.File == tc.path {
					found = true
				}
			}
			if !found {
				t.Errorf("missing sensitive-path indicator for %q: %+v", tc.path, as.Indicators)
			}
		})
	}
}

func TestClassifyLargeDiff(t *testing.T) {
	// 501 added lines exceeds the default 500 threshold.
	req := changeRequest(domain.SourceAgent,
		fileChange("internal/app/handler.go", domain.OpEdit, 501, 0))
	as := Classify(req, Config{})
	if as.Level != LevelHigh {
		t.Fatalf("Level = %q, want high (agent + large diff)", as.Level)
	}
	// Human with the same diff is medium.
	req = changeRequest(domain.SourceHuman,
		fileChange("internal/app/handler.go", domain.OpEdit, 501, 0))
	if as := Classify(req, Config{}); as.Level != LevelMedium {
		t.Fatalf("Level = %q, want medium (human + large diff)", as.Level)
	}
	// Exactly at the threshold is not large.
	req = changeRequest(domain.SourceAgent,
		fileChange("internal/app/handler.go", domain.OpEdit, 500, 0))
	if as := Classify(req, Config{}); as.Level != LevelLow {
		t.Fatalf("Level = %q, want low (500 lines is not large)", as.Level)
	}
	// Custom threshold.
	cfg := Config{MaxDiffLines: 10}
	req = changeRequest(domain.SourceAgent,
		fileChange("internal/app/handler.go", domain.OpEdit, 11, 0))
	if as := Classify(req, cfg); as.Level != LevelHigh {
		t.Fatalf("Level = %q, want high with custom threshold", as.Level)
	}
}

func TestClassifyDestructiveOp(t *testing.T) {
	req := changeRequest(domain.SourceAgent,
		fileChange("internal/legacy/dead.go", domain.OpDelete, 0, 0))
	as := Classify(req, Config{})
	if as.Level != LevelHigh {
		t.Fatalf("Level = %q, want high (agent + delete)", as.Level)
	}
	kind := ""
	for _, ind := range as.Indicators {
		if ind.Kind == "destructive-op" {
			kind = ind.File
		}
	}
	if kind != "internal/legacy/dead.go" {
		t.Errorf("destructive-op indicator file = %q, want the deleted file", kind)
	}
}

func TestClassifyMultipleIndicators(t *testing.T) {
	// Agent deletes a pem: sensitive + destructive -> high, both reasons kept.
	req := changeRequest(domain.SourceAgent,
		fileChange("certs/old.pem", domain.OpDelete, 0, 0))
	as := Classify(req, Config{})
	if as.Level != LevelHigh {
		t.Fatalf("Level = %q, want high", as.Level)
	}
	kinds := map[string]bool{}
	for _, ind := range as.Indicators {
		kinds[ind.Kind] = true
	}
	if !kinds["sensitive-path"] || !kinds["destructive-op"] {
		t.Errorf("expected both sensitive-path and destructive-op indicators, got %+v", as.Indicators)
	}
}

func TestRequiresApproval(t *testing.T) {
	if !RequiresApproval(LevelHigh, []string{"high"}) {
		t.Error("high should require approval with default list")
	}
	if RequiresApproval(LevelMedium, []string{"high"}) {
		t.Error("medium must NOT require approval by default")
	}
	if RequiresApproval(LevelLow, []string{"high"}) {
		t.Error("low must never require approval by default")
	}
	if RequiresApproval(LevelHigh, []string{"medium"}) {
		t.Error("high must not require approval when the list does not include it")
	}
	if RequiresApproval(LevelHigh, nil) {
		t.Error("empty list must never require approval")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{".kern/**", ".kern/boundaries.json", true},
		{".kern/**", ".kern/nested/deep/file.json", true},
		{".kern/**", "kern/file.json", false},
		{".kern/**", ".kern", false},
		{"**/*.pem", "certs/prod/server.pem", true},
		{"**/*.pem", "server.pem", true},
		{"**/*.pem", "certs/prod/server.key", false},
		{"auth/**", "auth/tokens.json", true},
		{"auth/**", "api/auth/tokens.json", false}, // globMatch is anchored; matchesPath handles any-level
		{"auth/**", "auth", false},
		{"**/credentials*", "config/credentials.json", true},
		{"**/credentials*", "credentials.json", true},
		{"**/secrets*", "config/secrets.local", true},
		{"**/secrets*", "config/notsecrets.local", false},
		{"*.pem", "server.pem", true},
		{"*.pem", "certs/server.pem", false}, // no **, * does not cross /
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.name); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestMatchesPathAnyLevel(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"auth/**", "auth/tokens.json", true},
		{"auth/**", "api/auth/tokens.json", true},
		{"auth/**", "api/v1/deep/auth/x/y.json", true},
		{"auth/**", "api/v1/deep/notauth/x.json", false},
		{".kern/**", ".kern/boundaries.json", true},
		{".kern/**", "sub/project/.kern/boundaries.json", true},
		{"*.pem", "certs/server.pem", true}, // any-level via suffix matching (fail-safe)
		{"*.pem", "server.pem", true},
	}
	for _, tc := range cases {
		if got := matchesPath(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchesPath(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestWithDefaults(t *testing.T) {
	cfg := withDefaults(Config{})
	if len(cfg.SensitivePathPatterns) == 0 {
		t.Error("default sensitive paths must be non-empty")
	}
	if cfg.MaxDiffLines != 500 {
		t.Errorf("default MaxDiffLines = %d, want 500", cfg.MaxDiffLines)
	}
	if len(cfg.RequireApprovalFor) != 1 || cfg.RequireApprovalFor[0] != "high" {
		t.Errorf("default RequireApprovalFor = %v, want [high]", cfg.RequireApprovalFor)
	}
	// Explicit values survive.
	cfg = withDefaults(Config{MaxDiffLines: 1000})
	if cfg.MaxDiffLines != 1000 {
		t.Errorf("explicit MaxDiffLines overwritten: %d", cfg.MaxDiffLines)
	}
}
