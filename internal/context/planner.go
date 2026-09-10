package context

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// TaskType classifies an intent into a deterministic task kind so the planner
// can pick an evidence policy without an LLM.
type TaskType string

const (
	TaskFixBug            TaskType = "fix_bug"
	TaskBuildFailure      TaskType = "build_failure"
	TaskRefactor          TaskType = "refactor"
	TaskAddFeature        TaskType = "add_feature"
	TaskSecurityReview    TaskType = "security_review"
	TaskPerformanceReview TaskType = "performance_review"
	TaskDocumentation     TaskType = "documentation"
)

// EvidencePriority scores one evidence class under a task policy.
type EvidencePriority struct {
	Type   domain.EvidenceType
	Weight float64
	Reason string
}

// TaskPolicy is the evidence-selection policy for one task type: which
// evidence classes matter, how much, and the default token budget.
type TaskPolicy struct {
	Type           TaskType
	Priorities     []EvidencePriority
	MaxTokens      int
	RetrievalLevel string // "l1"|"l2"|"l3": default disclosure level for the task type
}

// ClassifyTask deterministically maps an intent string to a TaskType via
// word-boundary keyword matching on the lowercased text (first match wins).
// Keyword order is tuned so the planner's own examples classify correctly:
// build_failure (e.g. "compile error") and fix_bug ("fix ... panic") must win
// over later classes, and add_feature ("sqlite") must not trip the security
// "sqli" keyword.
func ClassifyTask(intent string) TaskType {
	switch {
	case containsKeyword(intent, "performance", "slow", "latency", "benchmark", "optimize", "throughput"):
		return TaskPerformanceReview
	case containsKeyword(intent, "document", "readme", "comment", "explain"):
		return TaskDocumentation
	case containsKeyword(intent, "build", "compile", "compilation"):
		return TaskBuildFailure
	case containsKeyword(intent, "bug", "fix", "crash", "panic", "broken", "wrong", "error"):
		return TaskFixBug
	case containsKeyword(intent, "security", "vuln", "injection", "secret", "xss", "sqli", "auth"):
		return TaskSecurityReview
	case containsKeyword(intent, "refactor", "restructure", "cleanup", "simplify", "rename"):
		return TaskRefactor
	case containsKeyword(intent, "feature", "implement", "support", "new"):
		return TaskAddFeature
	}
	return TaskAddFeature
}

// DefaultPolicies returns one TaskPolicy per TaskType with the canonical
// evidence priorities and token budgets.
func DefaultPolicies() []TaskPolicy {
	return []TaskPolicy{
		{Type: TaskFixBug, MaxTokens: 8000, RetrievalLevel: "l2", Priorities: []EvidencePriority{
			{Type: domain.EvidenceGraph, Weight: 1.0, Reason: "core graph context"},
			{Type: domain.EvidenceTest, Weight: 0.9, Reason: "test context"},
			{Type: domain.EvidenceGit, Weight: 0.8, Reason: "git context"},
			{Type: domain.EvidenceBuild, Weight: 0.7, Reason: "build context"},
			{Type: domain.EvidenceRuntime, Weight: 0.6, Reason: "runtime context"},
			{Type: domain.EvidenceMemory, Weight: 0.4, Reason: "memory context"},
			{Type: domain.EvidencePolicy, Weight: 0.3, Reason: "policy context"},
		}},
		{Type: TaskBuildFailure, MaxTokens: 6000, RetrievalLevel: "l2", Priorities: []EvidencePriority{
			{Type: domain.EvidenceBuild, Weight: 1.0, Reason: "core build context"},
			{Type: domain.EvidenceGit, Weight: 0.9, Reason: "git context"},
			{Type: domain.EvidenceTest, Weight: 0.8, Reason: "test context"},
			{Type: domain.EvidenceGraph, Weight: 0.6, Reason: "graph context"},
			{Type: domain.EvidenceRuntime, Weight: 0.5, Reason: "runtime context"},
			{Type: domain.EvidencePolicy, Weight: 0.4, Reason: "policy context"},
			{Type: domain.EvidenceMemory, Weight: 0.3, Reason: "memory context"},
		}},
		{Type: TaskRefactor, MaxTokens: 8000, RetrievalLevel: "l3", Priorities: []EvidencePriority{
			{Type: domain.EvidenceGraph, Weight: 1.0, Reason: "core graph context"},
			{Type: domain.EvidenceTest, Weight: 0.8, Reason: "test context"},
			{Type: domain.EvidenceMemory, Weight: 0.6, Reason: "memory context"},
			{Type: domain.EvidenceGit, Weight: 0.5, Reason: "git context"},
			{Type: domain.EvidencePolicy, Weight: 0.4, Reason: "policy context"},
			{Type: domain.EvidenceBuild, Weight: 0.3, Reason: "build context"},
			{Type: domain.EvidenceRuntime, Weight: 0.2, Reason: "runtime context"},
		}},
		{Type: TaskAddFeature, MaxTokens: 8000, RetrievalLevel: "l2", Priorities: []EvidencePriority{
			{Type: domain.EvidenceGraph, Weight: 0.9, Reason: "core graph context"},
			{Type: domain.EvidenceTest, Weight: 0.8, Reason: "test context"},
			{Type: domain.EvidenceMemory, Weight: 0.7, Reason: "memory context"},
			{Type: domain.EvidenceGit, Weight: 0.6, Reason: "git context"},
			{Type: domain.EvidencePolicy, Weight: 0.5, Reason: "policy context"},
			{Type: domain.EvidenceBuild, Weight: 0.4, Reason: "build context"},
			{Type: domain.EvidenceRuntime, Weight: 0.3, Reason: "runtime context"},
		}},
		{Type: TaskSecurityReview, MaxTokens: 10000, RetrievalLevel: "l2", Priorities: []EvidencePriority{
			{Type: domain.EvidencePolicy, Weight: 1.0, Reason: "core policy context"},
			{Type: domain.EvidenceGraph, Weight: 0.8, Reason: "graph context"},
			{Type: domain.EvidenceGit, Weight: 0.7, Reason: "git context"},
			{Type: domain.EvidenceTest, Weight: 0.6, Reason: "test context"},
			{Type: domain.EvidenceBuild, Weight: 0.5, Reason: "build context"},
			{Type: domain.EvidenceRuntime, Weight: 0.4, Reason: "runtime context"},
			{Type: domain.EvidenceMemory, Weight: 0.3, Reason: "memory context"},
		}},
		{Type: TaskPerformanceReview, MaxTokens: 10000, RetrievalLevel: "l2", Priorities: []EvidencePriority{
			{Type: domain.EvidenceRuntime, Weight: 1.0, Reason: "core runtime context"},
			{Type: domain.EvidenceGraph, Weight: 0.8, Reason: "graph context"},
			{Type: domain.EvidenceTest, Weight: 0.7, Reason: "test context"},
			{Type: domain.EvidenceBuild, Weight: 0.6, Reason: "build context"},
			{Type: domain.EvidenceGit, Weight: 0.5, Reason: "git context"},
			{Type: domain.EvidenceMemory, Weight: 0.4, Reason: "memory context"},
			{Type: domain.EvidencePolicy, Weight: 0.3, Reason: "policy context"},
		}},
		{Type: TaskDocumentation, MaxTokens: 4000, RetrievalLevel: "l1", Priorities: []EvidencePriority{
			{Type: domain.EvidenceMemory, Weight: 0.9, Reason: "core memory context"},
			{Type: domain.EvidenceGraph, Weight: 0.8, Reason: "graph context"},
			{Type: domain.EvidenceGit, Weight: 0.7, Reason: "git context"},
			{Type: domain.EvidenceTest, Weight: 0.5, Reason: "test context"},
			{Type: domain.EvidencePolicy, Weight: 0.5, Reason: "policy context"},
			{Type: domain.EvidenceBuild, Weight: 0.3, Reason: "build context"},
			{Type: domain.EvidenceRuntime, Weight: 0.3, Reason: "runtime context"},
		}},
	}
}

// PolicyFor looks up the policy for a task type, falling back to fix_bug's
// policy when the type is unknown.
func PolicyFor(t TaskType) TaskPolicy {
	for _, p := range DefaultPolicies() {
		if p.Type == t {
			return p
		}
	}
	for _, p := range DefaultPolicies() {
		if p.Type == TaskFixBug {
			return p
		}
	}
	return TaskPolicy{}
}

// RetrievalLevelFor returns the default retrieval disclosure level
// ("l1"|"l2"|"l3") for a task type, via PolicyFor. Unknown task types fall
// back to fix_bug's policy; a policy with no explicit level defaults to "l2"
// (deterministic middle ground).
func RetrievalLevelFor(t TaskType) string {
	if lvl := PolicyFor(t).RetrievalLevel; lvl != "" {
		return lvl
	}
	return "l2"
}

// Selection is one scored, explainable evidence item chosen for the plan.
type Selection struct {
	Type    domain.EvidenceType `json:"type"`
	Source  string              `json:"source"`  // "fact" | "runtime" | "risk" | "validation" | "memory"
	Weight  float64             `json:"weight"`  // policy weight for this type
	Reason  string              `json:"reason"`  // "evidence type <t> scored <w> for <tasktype>"
	Tokens  int                 `json:"tokens"`  // tokenize.Count of the item's content
	Content string              `json:"content"` // truncated to 200 chars for explainability
}

// Plan is the deterministic context plan: task type, policy, and the ranked,
// budget-fitted evidence selections.
type Plan struct {
	TaskType    TaskType    `json:"task_type"`
	Policy      TaskPolicy  `json:"policy"`
	Selections  []Selection `json:"selections"` // ranked, budget-fitted
	Budget      int         `json:"budget"`
	TotalTokens int         `json:"total_tokens"`
	Truncated   bool        `json:"truncated,omitempty"`
}

// SelectEvidence scores the packet's evidence-bearing items against the
// policy. Items whose EvidenceType has no policy priority get weight 0 and are
// still included so the plan is complete. Results are sorted by weight DESC,
// then tokens ASC (deterministic).
func SelectEvidence(pkt *domain.ContextPacket, policy TaskPolicy) []Selection {
	if pkt == nil {
		return nil
	}
	weights := map[domain.EvidenceType]float64{}
	for _, pr := range policy.Priorities {
		weights[pr.Type] = pr.Weight
	}
	var sels []Selection
	// Facts: each Claim counts as one item (source "fact").
	for _, c := range pkt.Facts {
		et := domain.EvidenceType("")
		if len(c.Evidence) > 0 {
			et = c.Evidence[0].Type
		}
		sels = append(sels, scoredSelection(et, "fact", weights, policy.Type, c.Statement))
	}
	// RuntimeEvidence (source "runtime").
	for _, ev := range pkt.RuntimeEvidence {
		sels = append(sels, scoredSelection(ev.Type, "runtime", weights, policy.Type, ev.Content))
	}
	// Risks (source "risk"): Risk has no text field, so the type name (its
	// RiskLevel) is the content.
	for _, r := range pkt.Risks {
		sels = append(sels, scoredSelection(domain.EvidencePolicy, "risk", weights, policy.Type, string(r.Level)))
	}
	// RequiredValidation (source "validation").
	for _, v := range pkt.RequiredValidation {
		sels = append(sels, scoredSelection(domain.EvidenceTest, "validation", weights, policy.Type, v))
	}
	// Memory (source "memory").
	for _, m := range pkt.Memory {
		sels = append(sels, scoredSelection(domain.EvidenceMemory, "memory", weights, policy.Type, m.Content))
	}
	sort.SliceStable(sels, func(i, j int) bool {
		if sels[i].Weight != sels[j].Weight {
			return sels[i].Weight > sels[j].Weight
		}
		return sels[i].Tokens < sels[j].Tokens
	})
	return sels
}

// scoredSelection builds a Selection for one item, scoring it against the
// policy's weight table and capping content at 200 runes.
func scoredSelection(t domain.EvidenceType, source string, weights map[domain.EvidenceType]float64, task TaskType, content string) Selection {
	w, has := weights[t]
	reason := fmt.Sprintf("no policy priority for %s", t)
	if has {
		reason = fmt.Sprintf("evidence type %s scored %g for %s", t, w, task)
	}
	return Selection{
		Type:    t,
		Source:  source,
		Weight:  w,
		Reason:  reason,
		Tokens:  tokenize.Count(content),
		Content: capRunes(content, 200),
	}
}

// FitToBudget greedily keeps selections by rank while total tokens stay within
// budget. The FIRST selection of each EvidenceType present in the ranked list
// is always kept (diversity), even when it pushes over budget; afterwards the
// lowest-ranked items that overflow are dropped. Truncated reports whether
// anything was dropped. budget <= 0 keeps everything.
func FitToBudget(sels []Selection, budget int) ([]Selection, bool) {
	if budget <= 0 {
		return sels, false
	}
	firstOfType := map[domain.EvidenceType]int{}
	for i, s := range sels {
		if _, ok := firstOfType[s.Type]; !ok {
			firstOfType[s.Type] = i
		}
	}
	var kept []Selection
	total := 0
	truncated := false
	for i, s := range sels {
		if total+s.Tokens > budget && firstOfType[s.Type] != i {
			truncated = true
			continue
		}
		kept = append(kept, s)
		total += s.Tokens
	}
	return kept, truncated
}

// PlanPacket is the composition entry point: classify the intent, fetch the
// policy, score the packet's evidence, fit to budget, and stamp the envelope
// version (EnvelopeVersionV1 + SchemaVersion "1.0.0" when empty). A budget
// <= 0 uses the policy's MaxTokens.
func PlanPacket(pkt *domain.ContextPacket, intent string, budget int) *Plan {
	tt := ClassifyTask(intent)
	policy := PolicyFor(tt)
	b := budget
	if b <= 0 {
		b = policy.MaxTokens
	}
	sels, truncated := FitToBudget(SelectEvidence(pkt, policy), b)
	plan := &Plan{
		TaskType:    tt,
		Policy:      policy,
		Selections:  sels,
		Budget:      b,
		TotalTokens: selectionTokens(sels),
		Truncated:   truncated,
	}
	if pkt != nil {
		pkt.EnvelopeVersion = domain.EnvelopeVersionV1
		if pkt.SchemaVersion == "" {
			pkt.SchemaVersion = "1.0.0"
		}
	}
	return plan
}

// RenderPlan produces the explainable text plan.
func RenderPlan(p *Plan) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== context plan: %s (budget %d, ~%d tokens) ==\n", p.TaskType, p.Budget, p.TotalTokens)
	var prios []string
	for i, pr := range p.Policy.Priorities {
		if i >= 3 {
			break
		}
		prios = append(prios, fmt.Sprintf("%s(%g)", pr.Type, pr.Weight))
	}
	fmt.Fprintf(&b, "policy: %s\n", strings.Join(prios, ", "))
	for _, s := range p.Selections {
		fmt.Fprintf(&b, "%g %s %s ~%dtok — %s — %s\n", s.Weight, s.Type, s.Source, s.Tokens, s.Reason, capRunes(s.Content, 80))
	}
	fmt.Fprintf(&b, "~%d tokens", p.TotalTokens)
	if p.Truncated {
		b.WriteString(" (truncated)")
	}
	return b.String()
}

// selectionTokens sums the token counts of a selection list.
func selectionTokens(sels []Selection) int {
	total := 0
	for _, s := range sels {
		total += s.Tokens
	}
	return total
}

// capRunes truncates s to at most n runes.
func capRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// containsKeyword reports whether any of the keywords appears as a whole word
// in the lowercased intent. Word-boundary matching (not substring) keeps
// "sqlite" from matching the "sqli" security keyword while still catching
// "fix" in "fix the auth panic".
func containsKeyword(intent string, keywords ...string) bool {
	words := wordSet(intent)
	for _, k := range keywords {
		if words[k] {
			return true
		}
	}
	return false
}

// wordSet lowercases the intent and splits it into word tokens.
func wordSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		set[w] = true
	}
	return set
}
