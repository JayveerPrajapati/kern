package architecture

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// buildIndex builds an index over the module at root, failing the test on error.
func buildIndex(t *testing.T, root string) *index.Index {
	t.Helper()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	return ix
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load missing config: %v", err)
	}
	if cfg == nil || len(cfg.Rules) != 0 || len(cfg.Layers) != 0 {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestLoadParsesYAML(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
name: "fixture"
layers:
  - name: presentation
    paths: ["web/**"]
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != "1" || cfg.Name != "fixture" {
		t.Fatalf("version/name wrong: %+v", cfg)
	}
	if len(cfg.Layers) != 1 || cfg.Layers[0].Name != "presentation" {
		t.Fatalf("layers wrong: %+v", cfg.Layers)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].ID != "forbid-web-db" || cfg.Rules[0].From != "web" {
		t.Fatalf("rules wrong: %+v", cfg.Rules)
	}
}

func TestLoadAcceptsJSON(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.json", `{"version":"1","rules":[{"from":"web","to":"db","action":"forbid"}]}`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load json: %v", err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].From != "web" || cfg.Rules[0].Action != "forbid" {
		t.Fatalf("json config wrong: %+v", cfg.Rules)
	}
}

func TestLoadFailClosedOnUnparseable(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", "this is: : not valid: :yaml {{{\n  - broken")
	if _, err := Load(root); err == nil {
		t.Fatal("expected error for unparseable config (fail closed)")
	}
}

func TestLoadRejectsUnknownVersion(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", "version: \"99\"\nrules: []\n")
	if _, err := Load(root); err == nil {
		t.Fatal("expected error for unknown version")
	}
}

func TestLoadRejectsBadAction(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - from: web
    to: db
    action: maybe
`)
	if _, err := Load(root); err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestCheckForbiddenEdge(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
`)
	ix := buildIndex(t, root)
	cfg, _ := Load(root)
	vs := NewEngine(cfg).Check(ix, []string{"web/web.go"})
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].RuleID != "forbid-web-db" || vs[0].Severity != "error" {
		t.Fatalf("violation metadata wrong: %+v", vs[0])
	}
	if !strings.Contains(vs[0].CallerFile, "web/web.go") {
		t.Fatalf("caller wrong: %+v", vs[0])
	}
}

func TestAllowRuleOverridesForbid(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
  - id: allow-web-db
    from: web
    to: db
    action: allow
`)
	ix := buildIndex(t, root)
	cfg, _ := Load(root)
	if vs := NewEngine(cfg).Check(ix, []string{"web/web.go"}); len(vs) != 0 {
		t.Fatalf("allow rule should override forbid, got %+v", vs)
	}
}

func TestCheckWarningSeverity(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: warn-web-db
    from: web
    to: db
    action: forbid
    severity: warning
`)
	ix := buildIndex(t, root)
	cfg, _ := Load(root)
	vs := NewEngine(cfg).Check(ix, []string{"web/web.go"})
	if len(vs) != 1 || vs[0].Severity != "warning" {
		t.Fatalf("expected warning severity, got %+v", vs)
	}
}

func TestCheckLayerRules(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
layers:
  - name: presentation
    paths: ["web/**"]
  - name: data
    paths: ["db/**"]
rules:
  - id: layer-pres-data
    layer_from: presentation
    layer_to: data
    action: forbid
`)
	ix := buildIndex(t, root)
	cfg, _ := Load(root)
	vs := NewEngine(cfg).Check(ix, []string{"web/web.go"})
	if len(vs) != 1 {
		t.Fatalf("expected 1 layer violation, got %d: %+v", len(vs), vs)
	}
	if vs[0].RuleID != "layer-pres-data" {
		t.Fatalf("layer rule id wrong: %+v", vs[0])
	}
	if !strings.Contains(vs[0].RuleFrom, "presentation") || !strings.Contains(vs[0].RuleTo, "data") {
		t.Fatalf("layer rule endpoints wrong: %+v", vs[0])
	}
}

func TestCheckLayerDepends(t *testing.T) {
	// layer "web" may depend only on "api"; it reaches "db" -> violation.
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
layers:
  - name: frontend
    paths: ["web/**"]
    depends: ["api"]
  - name: backend
    paths: ["db/**"]
rules: []
`)
	ix := buildIndex(t, root)
	cfg, _ := Load(root)
	vs := NewEngine(cfg).Check(ix, []string{"web/web.go"})
	if len(vs) == 0 {
		t.Fatal("expected a layer.depends violation, got none")
	}
	if !strings.Contains(vs[0].RuleID, "layer.depends") {
		t.Fatalf("expected depends rule id, got %+v", vs[0])
	}
}

func TestCheckDeterministicOrdering(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
`)
	ix := buildIndex(t, root)
	cfg, _ := Load(root)
	eng := NewEngine(cfg)
	a := eng.Check(ix, []string{"web/web.go", "db/db.go"})
	b := eng.Check(ix, []string{"db/db.go", "web/web.go"})
	if len(a) != len(b) {
		t.Fatalf("determinism broken: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].CallerFile != b[i].CallerFile {
			t.Fatalf("ordering differs: %+v vs %+v", a, b)
		}
	}
}
