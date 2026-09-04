package risk

import (
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
)

// LoadConfig maps the .blueprint/config.yaml approval section into the risk
// classification Config. Empty lists keep the built-in defaults (the risk
// package owns those defaults; the policy package owns the YAML surface).
// Explicit values replace the corresponding default wholesale.
func LoadConfig(ac policy.ApprovalConfig) Config {
	cfg := DefaultConfig()
	if len(ac.SensitivePaths) > 0 {
		cfg.SensitivePathPatterns = append([]string(nil), ac.SensitivePaths...)
	}
	if ac.MaxDiffLines > 0 {
		cfg.MaxDiffLines = ac.MaxDiffLines
	}
	if len(ac.RequireForRiskLevels) > 0 {
		cfg.RequireApprovalFor = append([]string(nil), ac.RequireForRiskLevels...)
	}
	if len(ac.RequireForSources) > 0 {
		cfg.RequireForSources = append([]string(nil), ac.RequireForSources...)
	}
	return cfg
}
