package domain

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/sec"
)

// ---- Project ----

func TestProjectZeroValue(t *testing.T) {
	var p Project
	if p.Root != "" || p.Name != "" || p.VCS != "" || p.SymbolCount != 0 {
		t.Fatalf("unexpected zero value: %+v", p)
	}
	if p.Languages != nil || p.Frameworks != nil {
		t.Fatalf("slices should be nil in zero value: %+v", p)
	}
	if !p.IndexedAt.IsZero() {
		t.Fatalf("IndexedAt should be zero time")
	}
}

func TestProjectPopulated(t *testing.T) {
	now := time.Now()
	p := Project{
		Root:        "/repo",
		Name:        "kern",
		Languages:   []string{"Go", "Python"},
		Frameworks:  []string{"echo"},
		VCS:         "git",
		IndexedAt:   now,
		SymbolCount: 42,
	}
	if p.Name != "kern" || p.VCS != "git" || p.SymbolCount != 42 {
		t.Fatalf("populated Project wrong: %+v", p)
	}
	if len(p.Languages) != 2 || p.IndexedAt != now {
		t.Fatalf("populated Project wrong: %+v", p)
	}
}

// ---- Repository ----

func TestRepository(t *testing.T) {
	var z Repository
	if z.Root != "" || z.VCS != "" || z.Remote != "" || z.Branch != "" || z.HEAD != "" {
		t.Fatalf("unexpected zero value: %+v", z)
	}
	r := Repository{Root: "/repo", VCS: "git", Remote: "origin", Branch: "main", HEAD: "abc123"}
	if r.Branch != "main" || r.HEAD != "abc123" {
		t.Fatalf("populated Repository wrong: %+v", r)
	}
}

// ---- Module ----

func TestModule(t *testing.T) {
	var m Module
	if m.Path != "" || m.Language != "" {
		t.Fatalf("unexpected zero value: %+v", m)
	}
	if m.Files != nil || m.Symbols != nil {
		t.Fatalf("slices should be nil in zero value")
	}
	m = Module{
		Path:     "github.com/acme/pkg",
		Language: "Go",
		Files:    []string{"a.go"},
		Symbols:  []Symbol{{Name: "Foo"}},
	}
	if m.Path != "github.com/acme/pkg" || len(m.Symbols) != 1 {
		t.Fatalf("populated Module wrong: %+v", m)
	}
}

// ---- File ----

func TestFile(t *testing.T) {
	var f File
	if f.Path != "" || f.Language != "" || f.Hash != "" || f.Lines != 0 {
		t.Fatalf("unexpected zero value: %+v", f)
	}
	if f.Symbols != nil {
		t.Fatalf("Symbols should be nil in zero value")
	}
	f = File{Path: "main.go", Language: "Go", Hash: "sha", Lines: 10, Symbols: []Symbol{{Name: "main"}}}
	if f.Lines != 10 || len(f.Symbols) != 1 {
		t.Fatalf("populated File wrong: %+v", f)
	}
}

// ---- Symbol ----

func TestSymbol(t *testing.T) {
	var s Symbol
	if s.Name != "" || s.Qualified != "" || s.Line != 0 || s.Exported {
		t.Fatalf("unexpected zero value: %+v", s)
	}
	s = Symbol{
		Name: "Foo", Qualified: "pkg.Foo", Kind: "func", File: "a.go",
		Line: 3, Language: "Go", Signature: "func()", Receiver: "", Exported: true,
	}
	if s.Qualified != "pkg.Foo" || s.Line != 3 || !s.Exported {
		t.Fatalf("populated Symbol wrong: %+v", s)
	}
}

// ---- Graph / Node / Edge ----

func TestGraph(t *testing.T) {
	var g Graph
	if g.Nodes != nil || g.Edges != nil {
		t.Fatalf("slices should be nil in zero value: %+v", g)
	}
	g = Graph{
		Project: Project{Name: "kern"},
		Nodes:   []Node{{ID: "n1"}},
		Edges:   []Edge{{From: "n1", To: "n2"}},
	}
	if len(g.Nodes) != 1 || len(g.Edges) != 1 {
		t.Fatalf("populated Graph wrong: %+v", g)
	}
}

func TestNodePointerFields(t *testing.T) {
	var n Node
	if n.Symbol != nil || n.File != nil {
		t.Fatalf("pointer fields should be nil in zero value")
	}
	sym := Symbol{Name: "Foo"}
	fl := File{Path: "a.go"}
	n = Node{ID: "n1", Kind: "symbol", Label: "Foo", Symbol: &sym}
	if n.Symbol == nil || n.Symbol.Name != "Foo" {
		t.Fatalf("Symbol pointer not set: %+v", n)
	}
	if n.File != nil {
		t.Fatalf("File should be nil for a symbol node")
	}
	n2 := Node{ID: "f1", Kind: "file", File: &fl}
	if n2.File == nil || n2.File.Path != "a.go" {
		t.Fatalf("File pointer not set: %+v", n2)
	}
}

func TestEdge(t *testing.T) {
	var e Edge
	if e.From != "" || e.To != "" || e.Kind != "" || e.Line != 0 {
		t.Fatalf("unexpected zero value: %+v", e)
	}
	e = Edge{From: "a", To: "b", Kind: "calls", File: "a.go", Line: 5}
	if e.Kind != "calls" || e.Line != 5 {
		t.Fatalf("populated Edge wrong: %+v", e)
	}
}

// ---- Claim ----

func TestClaimTypeConstants(t *testing.T) {
	cases := []struct {
		got  ClaimType
		want string
	}{
		{ClaimFact, "FACT"},
		{ClaimInference, "INFERENCE"},
		{ClaimHypothesis, "HYPOTHESIS"},
		{ClaimRecommendation, "RECOMMENDATION"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("ClaimType constant = %q, want %q", c.got, c.want)
		}
	}
}

func TestClaimZeroValue(t *testing.T) {
	var c Claim
	if c.Type != "" || c.Statement != "" || c.Source != "" || c.Confidence != 0 {
		t.Fatalf("unexpected zero value: %+v", c)
	}
	if c.Evidence != nil {
		t.Fatalf("Evidence should be nil in zero value")
	}
	if c.HasEvidence() {
		t.Fatalf("zero-value Claim should have no evidence")
	}
}

func TestClaimPopulated(t *testing.T) {
	now := time.Now()
	c := Claim{
		Type: ClaimFact, Statement: "x calls y", Evidence: []Evidence{{Type: EvidenceGraph}},
		Source: "graph", Timestamp: now, Scope: "y", Confidence: 0.9,
	}
	if !c.HasEvidence() || len(c.Evidence) != 1 {
		t.Fatalf("populated Claim evidence wrong: %+v", c)
	}
	if c.Confidence != 0.9 || c.Timestamp != now {
		t.Fatalf("populated Claim wrong: %+v", c)
	}
}

// ---- Evidence ----

func TestEvidenceTypeConstants(t *testing.T) {
	cases := []struct {
		got  EvidenceType
		want string
	}{
		{EvidenceGraph, "graph"},
		{EvidenceTest, "test"},
		{EvidenceBuild, "build"},
		{EvidenceGit, "git"},
		{EvidenceRuntime, "runtime"},
		{EvidenceMemory, "memory"},
		{EvidencePolicy, "policy"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("EvidenceType constant = %q, want %q", c.got, c.want)
		}
	}
}

func TestEvidencePopulated(t *testing.T) {
	e := Evidence{Type: EvidenceGit, Source: "git", Content: "diff", Digest: "d", Timestamp: time.Now()}
	if e.Type != EvidenceGit || e.Digest == "" {
		t.Fatalf("populated Evidence wrong: %+v", e)
	}
}

// ---- Memory ----

func TestMemoryTypeConstants(t *testing.T) {
	cases := []struct {
		got  MemoryType
		want string
	}{
		{MemoryLesson, "lesson"},
		{MemoryDecision, "decision"},
		{MemoryIncident, "incident"},
		{MemoryConstraint, "constraint"},
		{MemorySemantic, "semantic"},
		{MemoryAgent, "agent"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("MemoryType constant = %q, want %q", c.got, c.want)
		}
	}
}

func TestMemory(t *testing.T) {
	var m Memory
	if m.ID != "" || m.Type != "" || m.Content != "" || m.Tags != nil {
		t.Fatalf("unexpected zero value: %+v", m)
	}
	m = Memory{ID: "m1", Type: MemoryLesson, Content: "learned", Source: "agent", Scope: "svc", Tags: []string{"go"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if m.ID != "m1" || m.Type != MemoryLesson || len(m.Tags) != 1 {
		t.Fatalf("populated Memory wrong: %+v", m)
	}
}

func TestMemoryProvenanceFields(t *testing.T) {
	var m Memory
	if m.Subject != "" || m.Confidence != 0 || m.Provenance != "" || m.RelatedEntities != nil {
		t.Fatalf("unexpected non-zero provenance/confidence fields: %+v", m)
	}
	m = Memory{
		ID:              "m1",
		Subject:         "PaymentService",
		Confidence:      0.9,
		Provenance:      "loop:learn",
		RelatedEntities: []string{"symbol:Charge", "task:T-42"},
		Tags:            []string{"go"},
	}
	if m.Subject != "PaymentService" || m.Confidence != 0.9 || m.Provenance != "loop:learn" {
		t.Fatalf("provenance/confidence fields not carried: %+v", m)
	}
	if len(m.RelatedEntities) != 2 || m.RelatedEntities[0] != "symbol:Charge" {
		t.Fatalf("RelatedEntities not carried: %+v", m)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "go" {
		t.Fatalf("existing Tags field regressed: %+v", m)
	}
}

// ---- Decision ----

func TestDecision(t *testing.T) {
	var d Decision
	if d.ID != "" || d.Title != "" || d.Status != "" || d.Author != "" {
		t.Fatalf("unexpected zero value: %+v", d)
	}
	d = Decision{ID: "d1", Title: "T", Context: "C", Decision: "D", Consequences: "X", Status: "accepted", Author: "alice", CreatedAt: time.Now()}
	if d.Title != "T" || d.Status != "accepted" {
		t.Fatalf("populated Decision wrong: %+v", d)
	}
}

// ---- Policy ----

func TestPolicy(t *testing.T) {
	var p Policy
	if p.ID != "" || p.Name != "" || p.Enabled {
		t.Fatalf("unexpected zero value: %+v", p)
	}
	p = Policy{ID: "p1", Name: "no-eval", Rule: "forbid eval", Scope: "all", Enabled: true}
	if p.Name != "no-eval" || !p.Enabled {
		t.Fatalf("populated Policy wrong: %+v", p)
	}
}

// ---- Risk ----

func TestRiskLevelConstants(t *testing.T) {
	cases := []struct {
		got  RiskLevel
		want string
	}{
		{RiskLow, "LOW"},
		{RiskMedium, "MEDIUM"},
		{RiskHigh, "HIGH"},
		{RiskCritical, "CRITICAL"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("RiskLevel constant = %q, want %q", c.got, c.want)
		}
	}
}

func TestRisk(t *testing.T) {
	var r Risk
	if r.Level != "" || r.Score != 0 || r.Factors != nil {
		t.Fatalf("unexpected zero value: %+v", r)
	}
	r = Risk{Level: RiskHigh, Factors: []string{"no tests"}, Score: 0.8, Mitigation: "add tests"}
	if r.Level != RiskHigh || r.Score != 0.8 {
		t.Fatalf("populated Risk wrong: %+v", r)
	}
}

// ---- Approval ----

func TestApproval(t *testing.T) {
	var a Approval
	if a.ID != "" || a.TaskID != "" || a.Status != "" {
		t.Fatalf("unexpected zero value: %+v", a)
	}
	if a.DecidedAt != nil {
		t.Fatalf("DecidedAt pointer should be nil in zero value")
	}
	now := time.Now()
	a = Approval{ID: "a1", TaskID: "t1", Requester: "agent", Approver: "human", Status: "approved", Reason: "ok", RequestedAt: now, DecidedAt: &now}
	if a.Status != "approved" || a.DecidedAt == nil {
		t.Fatalf("populated Approval wrong: %+v", a)
	}
}

// ---- Agent ----

func TestAgent(t *testing.T) {
	var ag Agent
	if ag.ID != "" || ag.Name != "" || ag.Type != "" {
		t.Fatalf("unexpected zero value: %+v", ag)
	}
	ag = Agent{ID: "a1", Name: "planner", Type: "planner", CreatedAt: time.Now()}
	if ag.ID != "a1" || ag.Type != "planner" {
		t.Fatalf("populated Agent wrong: %+v", ag)
	}
}

// ---- Task ----

func TestTaskStateConstants(t *testing.T) {
	cases := []struct {
		got  TaskState
		want string
	}{
		{TaskCreated, "CREATED"},
		{TaskAnalyzing, "ANALYZING"},
		{TaskPlanning, "PLANNING"},
		{TaskWaitingApproval, "WAITING_FOR_APPROVAL"},
		{TaskApproved, "APPROVED"},
		{TaskExecuting, "EXECUTING"},
		{TaskVerifying, "VERIFYING"},
		{TaskReadyForPR, "READY_FOR_PR"},
		{TaskPRCreated, "PR_CREATED"},
		{TaskDeploying, "DEPLOYING"},
		{TaskObserving, "OBSERVING"},
		{TaskCompleted, "COMPLETED"},
		{TaskFailed, "FAILED"},
		{TaskBlocked, "BLOCKED"},
		{TaskRejected, "REJECTED"},
		{TaskCancelled, "CANCELLED"},
		{TaskRolledBack, "ROLLED_BACK"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("TaskState constant = %q, want %q", c.got, c.want)
		}
	}
}

func TestTaskZeroValue(t *testing.T) {
	var tk Task
	if tk.ID != "" || tk.Type != "" || tk.State != "" || tk.Agent != nil {
		t.Fatalf("unexpected zero value: %+v", tk)
	}
	if tk.IsTerminal() {
		t.Fatalf("zero-value task should not be terminal")
	}
}

func TestTaskPopulatedAndTerminal(t *testing.T) {
	ag := Agent{ID: "a1"}
	tk := Task{ID: "t1", Type: "code", State: TaskExecuting, Agent: &ag, Input: "do it", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if tk.Agent == nil || tk.Agent.ID != "a1" || tk.Type != "code" {
		t.Fatalf("populated Task wrong: %+v", tk)
	}
	if tk.IsTerminal() {
		t.Fatalf("executing task should not be terminal")
	}
	for _, s := range []TaskState{TaskCompleted, TaskFailed, TaskCancelled, TaskRejected, TaskRolledBack} {
		tk := Task{State: s}
		if !tk.IsTerminal() {
			t.Errorf("state %q should be terminal", s)
		}
	}
	for _, s := range []TaskState{TaskCreated, TaskApproved, TaskBlocked} {
		tk := Task{State: s}
		if tk.IsTerminal() {
			t.Errorf("state %q should not be terminal", s)
		}
	}
}

// ---- Adapters ----

func TestFromIndexSymbol(t *testing.T) {
	src := index.Symbol{
		Kind: "func", Name: "Fetch", Receiver: "Client", File: "client.go",
		Line: 12, Params: []string{"id string"}, Lang: "go",
	}
	d := FromIndexSymbol(src)
	if d.Name != "Fetch" || d.Kind != "func" || d.File != "client.go" || d.Line != 12 {
		t.Fatalf("mapped fields wrong: %+v", d)
	}
	if d.Language != "go" {
		t.Fatalf("Language should map from Lang: %q", d.Language)
	}
	// Qualified should be Receiver.Name for methods.
	if d.Qualified != "Client.Fetch" {
		t.Fatalf("Qualified = %q, want Client.Fetch", d.Qualified)
	}
	if !d.Exported {
		t.Fatalf("Fetch should be exported")
	}
	// Method receiver preserved.
	if d.Receiver != "Client" {
		t.Fatalf("Receiver = %q, want Client", d.Receiver)
	}
	// Signature joins params.
	if d.Signature != "id string" {
		t.Fatalf("Signature = %q, want param join", d.Signature)
	}
}

func TestFromIndexSymbolExportedRule(t *testing.T) {
	lower := FromIndexSymbol(index.Symbol{Name: "internalHelper"})
	if lower.Exported {
		t.Fatalf("lowercase name should not be exported")
	}
}

func TestFromMemoryLesson(t *testing.T) {
	m := FromMemoryLesson("always use context with timeouts")
	if m.Type != MemoryLesson {
		t.Fatalf("Type = %q, want lesson", m.Type)
	}
	if m.Content != "always use context with timeouts" {
		t.Fatalf("Content not preserved: %q", m.Content)
	}
	if m.Source == "" || m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Fatalf("FromMemoryLesson should set source/timestamps: %+v", m)
	}
}

func TestFromSecFinding(t *testing.T) {
	f := sec.Finding{
		File: "main.go", Line: 3, Rule: "sql-injection",
		Severity: string(sec.SeverityError), Message: "dynamic SQL", Snippet: "q := ...",
	}
	c := FromSecFinding(f)
	if c.Type != ClaimFact {
		t.Fatalf("Type = %q, want FACT", c.Type)
	}
	if c.Statement != "dynamic SQL" || c.Scope != "main.go" {
		t.Fatalf("claim fields wrong: %+v", c)
	}
	if c.Confidence != 1.0 {
		t.Fatalf("deterministic sec finding should have confidence 1.0, got %v", c.Confidence)
	}
	if !c.HasEvidence() {
		t.Fatalf("expected evidence")
	}
	ev := c.Evidence[0]
	if ev.Type != EvidencePolicy {
		t.Fatalf("evidence type = %q, want policy", ev.Type)
	}
	if ev.Digest == "" {
		t.Fatalf("evidence digest should be set")
	}
}

func TestFromGuardRule(t *testing.T) {
	r := intel.BoundaryRule{From: "web", To: "db", Action: "forbid"}
	p := FromGuardRule(r)
	if p.Name != "boundary:web->db" || p.Enabled != true {
		t.Fatalf("policy fields wrong: %+v", p)
	}
	if p.Scope != "all" || p.Rule != "forbid web -> db" {
		t.Fatalf("policy Rule/Scope wrong: %+v", p)
	}
}
