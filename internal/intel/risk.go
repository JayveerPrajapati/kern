package intel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Risk thresholds shared by kern pre_edit and every mutation gate (P2):
// rename/delete-apply, heal rounds, sandbox keeps. One definition — the
// verdict an agent sees in the pre-edit report is the verdict enforced at
// apply time.
const (
	riskHighDirect     = 10
	riskHighTransitive = 25
	riskMedDirect      = 3
	riskMedTransitive  = 8
)

// RiskVerdict is the pre-edit impact verdict for a set of target symbols:
// HIGH / MEDIUM / LOW with the direct and transitive caller counts behind
// it. Callers include test callers (conservative: a broad test surface is
// itself blast radius an edit must survive).
type RiskVerdict struct {
	Risk       string   `json:"risk"`
	Targets    []string `json:"targets"`
	Direct     []string `json:"direct_callers"`
	Transitive []string `json:"transitive_callers"`
}

// AssessEditRisk computes the verdict for one edit target: a symbol name
// (bare or FullName) and/or a root-relative file path. Either may be empty.
// Symbol matching, the search fallback, and caller counting mirror
// handlePreEdit exactly so the report and the gate cannot disagree.
func AssessEditRisk(ix *index.Index, file, symbol string) RiskVerdict {
	var targets []index.Symbol
	cleanFile := ""
	if file != "" {
		cleanFile = filepath.Clean(file)
	}
	for _, sym := range ix.Symbols {
		if symbol != "" && (sym.Name == symbol || sym.FullName() == symbol) {
			targets = append(targets, sym)
			continue
		}
		if cleanFile != "" && (filepath.Clean(sym.File) == cleanFile || strings.HasSuffix(filepath.Clean(sym.File), cleanFile)) {
			targets = append(targets, sym)
		}
	}
	// Same fallback as the report: an unmatched symbol resolves through
	// ranked search so renames of near-miss names still gate.
	if len(targets) == 0 && symbol != "" {
		if matches := ix.Search(symbol, 5); len(matches) > 0 {
			targets = append(targets, matches[0])
		}
	}
	return AssessTargets(ix, targets)
}

// AssessEditFiles computes the verdict for a set of root-relative files
// (heal repair targets, sandbox manifest paths): the union over every
// symbol each file defines, worst risk wins.
func AssessEditFiles(ix *index.Index, files []string) RiskVerdict {
	var targets []index.Symbol
	for _, f := range files {
		clean := filepath.Clean(f)
		for _, sym := range ix.Symbols {
			if filepath.Clean(sym.File) == clean || strings.HasSuffix(filepath.Clean(sym.File), clean) {
				targets = append(targets, sym)
			}
		}
	}
	return AssessTargets(ix, targets)
}

func AssessTargets(ix *index.Index, targets []index.Symbol) RiskVerdict {
	directSet := map[string]bool{}
	transSet := map[string]bool{}
	names := make([]string, 0, len(targets))
	for _, sym := range targets {
		full := sym.FullName()
		names = append(names, full)
		for _, c := range ix.CallersOf(full) {
			directSet[c] = true
			transSet[c] = true
			for _, c2 := range ix.CallersOf(c) {
				transSet[c2] = true
			}
		}
	}
	sort.Strings(names)
	direct := make([]string, 0, len(directSet))
	for c := range directSet {
		direct = append(direct, c)
	}
	sort.Strings(direct)
	trans := make([]string, 0, len(transSet))
	for c := range transSet {
		trans = append(trans, c)
	}
	sort.Strings(trans)
	risk := "LOW"
	if len(direct) > riskHighDirect || len(trans) > riskHighTransitive {
		risk = "HIGH"
	} else if len(direct) > riskMedDirect || len(trans) > riskMedTransitive {
		risk = "MEDIUM"
	}
	return RiskVerdict{Risk: risk, Targets: names, Direct: direct, Transitive: trans}
}

// Refusal renders the fail-closed gate message when the verdict blocks a
// mutation (HIGH without force). Non-HIGH verdicts return "" — proceed.
func (v RiskVerdict) Refusal(what string) string {
	if v.Risk != "HIGH" {
		return ""
	}
	targets := strings.Join(v.Targets, ", ")
	if len(v.Targets) > 4 {
		targets = strings.Join(v.Targets[:4], ", ") + fmt.Sprintf(", +%d more", len(v.Targets)-4)
	}
	return fmt.Sprintf("refusing %s: HIGH pre-edit verdict on %s (%d direct, %d transitive callers); re-run with --force (CLI) or force=true (MCP) to override",
		what, targets, len(v.Direct), len(v.Transitive))
}
