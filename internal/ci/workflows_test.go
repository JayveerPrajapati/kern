package ci_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Structural guard for .github/workflows. It cannot replace a real Actions run
// or actionlint, but it catches the mistakes that pass YAML parsing and then
// fail (or silently never run) on GitHub: a `needs` naming a job that does not
// exist, an unpinned third-party action, a job with no timeout, an `if:` that
// reads an `env.` variable no enclosing scope defines, and a `ci-ok`
// aggregator that forgot a job.

type workflow struct {
	Env  map[string]any `yaml:"env"`
	Jobs map[string]job `yaml:"jobs"`
}

type job struct {
	Uses           string         `yaml:"uses"`
	RunsOn         any            `yaml:"runs-on"`
	Needs          any            `yaml:"needs"`
	TimeoutMinutes any            `yaml:"timeout-minutes"`
	Env            map[string]any `yaml:"env"`
	Steps          []step         `yaml:"steps"`
}

type step struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	If   string `yaml:"if"`
}

func workflowFiles(t *testing.T) map[string]workflow {
	t.Helper()
	dir := filepath.Join("..", "..", ".github", "workflows")
	paths, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil || len(paths) == 0 {
		t.Skip("no .github/workflows next to this package (not in the source tree)")
	}
	out := map[string]workflow{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var w workflow
		if err := yaml.Unmarshal(b, &w); err != nil {
			t.Fatalf("%s: invalid YAML: %v", filepath.Base(p), err)
		}
		out[filepath.Base(p)] = w
	}
	return out
}

func needsOf(j job) []string {
	switch v := j.Needs.(type) {
	case string:
		return []string{v}
	case []any:
		var s []string
		for _, x := range v {
			if str, ok := x.(string); ok {
				s = append(s, str)
			}
		}
		return s
	}
	return nil
}

func TestWorkflowsAreStructurallySound(t *testing.T) {
	sha := regexp.MustCompile(`@[0-9a-f]{40}(\s|$)`)
	envRef := regexp.MustCompile(`\benv\.([A-Za-z_][A-Za-z0-9_]*)`)
	for file, w := range workflowFiles(t) {
		if len(w.Jobs) == 0 {
			t.Errorf("%s: no jobs", file)
		}
		for name, j := range w.Jobs {
			where := file + " job " + name
			if j.Uses == "" && j.RunsOn == nil {
				t.Errorf("%s: needs runs-on or uses", where)
			}
			if j.Uses == "" && j.TimeoutMinutes == nil {
				t.Errorf("%s: missing timeout-minutes (a hung runner burns hours)", where)
			}
			for _, n := range needsOf(j) {
				if _, ok := w.Jobs[n]; !ok {
					t.Errorf("%s: needs unknown job %q", where, n)
				}
			}
			for _, s := range j.Steps {
				if s.Uses != "" && !strings.HasPrefix(s.Uses, "./") && !sha.MatchString(s.Uses+" ") {
					t.Errorf("%s: step %q uses %q, which is not pinned to a commit SHA", where, s.Name, s.Uses)
				}
				for _, m := range envRef.FindAllStringSubmatch(s.If, -1) {
					_, inWorkflow := w.Env[m[1]]
					_, inJob := j.Env[m[1]]
					if !inWorkflow && !inJob {
						t.Errorf("%s: step %q has `if: %s` but env.%s is defined at neither workflow nor job level (a step's own env is invisible to its if) — the step can never run",
							where, s.Name, s.If, m[1])
					}
				}
			}
		}
	}
}

// ci-ok is the single required status check; if it omits a job, that job
// stops gating merges without anyone noticing.
func TestCIAggregatorCoversEveryJob(t *testing.T) {
	w, ok := workflowFiles(t)["ci.yml"]
	if !ok {
		t.Skip("ci.yml not found")
	}
	agg, ok := w.Jobs["ci-ok"]
	if !ok {
		t.Fatal("ci.yml has no ci-ok aggregator job")
	}
	have := needsOf(agg)
	var want []string
	for name := range w.Jobs {
		if name != "ci-ok" {
			want = append(want, name)
		}
	}
	sort.Strings(have)
	sort.Strings(want)
	if strings.Join(have, ",") != strings.Join(want, ",") {
		t.Errorf("ci-ok needs %v, but ci.yml defines jobs %v", have, want)
	}
}
