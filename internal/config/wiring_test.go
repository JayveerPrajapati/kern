package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/llm"
)

// TestMigratedSitesHonorConfigFile is the wiring test: with every migrated
// env var unset, four migrated sites (llm provider selection, model routing,
// context cost metric, deployer factory) resolve their values from a
// .kern/config.json file — proving the file tier actually reaches the code.
func TestMigratedSitesHonorConfigFile(t *testing.T) {
	dir := t.TempDir()
	kernDir := filepath.Join(dir, ".kern")
	if err := os.MkdirAll(kernDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{
	  "llm": {"provider": "anthropic", "model": "claude-file",
	          "model_roles": {"planner": "planner-file"}},
	  "cost_per_token": 0.5,
	  "deploy": {"command": "make deploy", "timeout": "30s"}
	}`
	if err := os.WriteFile(filepath.Join(kernDir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Every migrated env var unset (empty) so only the file tier can supply
	// values. ANTHROPIC_API_KEY is an excluded secret and is required to
	// construct the anthropic provider — it stays env-only.
	for _, v := range []string{
		"KERN_LLM_PROVIDER", "KERN_MODEL", "KERN_MODEL_PLANNER",
		"KERN_COST_PER_TOKEN", "KERN_DEPLOY_COMMAND", "KERN_DEPLOY_TIMEOUT",
	} {
		t.Setenv(v, "")
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	}()
	config.Reset() // sites resolve root "" = process cwd at call time

	// 1. llm provider selection honors llm.provider from the file.
	p, err := llm.NewProvider()
	if err != nil {
		t.Fatalf("llm.NewProvider: %v", err)
	}
	if _, ok := p.(*llm.AnthropicProvider); !ok {
		t.Fatalf("llm.NewProvider() = %T, want *llm.AnthropicProvider (llm.provider from file)", p)
	}

	// 1b. env still wins over the file at the same site.
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	p2, err := llm.NewProvider()
	if err != nil {
		t.Fatalf("llm.NewProvider (env): %v", err)
	}
	if _, ok := p2.(*llm.OllamaProvider); !ok {
		t.Fatalf("llm.NewProvider() = %T, want *llm.OllamaProvider (env must beat file)", p2)
	}
	t.Setenv("KERN_LLM_PROVIDER", "")

	// 2. model routing honors llm.model_roles.planner from the file.
	if got := agents.ModelOverride(agents.RolePlanner); got != "planner-file" {
		t.Fatalf("agents.ModelOverride(planner) = %q, want planner-file (llm.model_roles from file)", got)
	}

	// 3. context cost metric honors cost_per_token from the file.
	if got := context.CostPerToken(); got != 0.5 {
		t.Fatalf("context.CostPerToken() = %v, want 0.5 (cost_per_token from file)", got)
	}

	// 4. the deployer factory honors deploy.command + deploy.timeout.
	d := deployment.NewDeployerFromEnv()
	sd, ok := d.(*deployment.ShellDeployer)
	if !ok {
		t.Fatalf("NewDeployerFromEnv() = %T, want *deployment.ShellDeployer (deploy.command from file)", d)
	}
	if sd.Command != "make deploy" || sd.Timeout != 30*time.Second {
		t.Fatalf("ShellDeployer = %+v, want command=make deploy timeout=30s (deploy.* from file)", sd)
	}
}
