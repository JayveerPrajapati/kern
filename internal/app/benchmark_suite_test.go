package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// legacyBenchmarkRepo builds a "legacy" multi-file Go repository
// with cross-referencing symbols and a test file, so analysis is non-trivial.
func legacyBenchmarkRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module legacyrepo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "order.go"), `package main

// OrderService processes orders.
type OrderService struct{ repo *OrderRepository }

// CreateOrder creates an order.
func (s *OrderService) CreateOrder(id string) string { return s.repo.Save(id) }
`)
	writeFile(t, filepath.Join(root, "order_repo.go"), `package main

// OrderRepository stores orders.
type OrderRepository struct{ store map[string]string }

// Save persists an order.
func (s *OrderRepository) Save(id string) string { return id }
`)
	writeFile(t, filepath.Join(root, "billing.go"), `package main

// BillingService bills an order.
type BillingService struct{ orders *OrderService }

// Charge bills an order id.
func (b *BillingService) Charge(id string) string { return b.orders.CreateOrder(id) }
`)
	writeFile(t, filepath.Join(root, "catalog.go"), `package main

// CatalogService lists products.
type CatalogService struct{}

// List returns product ids.
func (c *CatalogService) List() []string { return []string{"p1"} }
`)
	writeFile(t, filepath.Join(root, "order_test.go"), `package main

import "testing"

func TestCreateOrder(t *testing.T) {
	s := &OrderService{repo: &OrderRepository{store: map[string]string{}}}
	if s.CreateOrder("o1") != "o1" { t.Fatal("mismatch") }
}
`)
	return root
}

// benchmarkRow is one (repo × task class) measurement ( /17.2/17.3).
type benchmarkRow struct {
	Repo  string
	Class string
	BaselineComparison
}

// runBenchmarkClass executes one task class against a repo and returns the
// baseline-vs-Kern comparison.
func runBenchmarkClass(t *testing.T, ts *TaskService, repo, class, symbol string) BaselineComparison {
	t.Helper()
	switch class {
	case "lookup":
		task, _, err := ts.Analyze(symbol)
		if err != nil {
			t.Fatalf("%s/%s Analyze: %v", repo, class, err)
		}
		return CompareToBaseline(task)
	case "small-change":
		task, _, _, err := ts.Plan(symbol)
		if err != nil {
			t.Fatalf("%s/%s Plan: %v", repo, class, err)
		}
		return CompareToBaseline(task)
	case "architecture":
		task, _, err := ts.WhatIf(whatif.RemoveSymbol, symbol, "")
		if err != nil {
			t.Fatalf("%s/%s WhatIf: %v", repo, class, err)
		}
		return CompareToBaseline(task)
	case "incident":
		store := runtime.NewStore()
		store.IngestAll([]runtime.Event{
			{ID: "b-1", Type: runtime.EventError, Service: "svc", Severity: "error",
				Message: "benchmark error", Timestamp: time.Now().Add(-time.Minute)},
		})
		ts.platform.WithRuntimeSource(store)
		task, _, _, err := ts.Correlate(domain.Alert{ID: "a", Service: "svc", OccurredAt: time.Now()})
		if err != nil {
			t.Fatalf("%s/%s Correlate: %v", repo, class, err)
		}
		return CompareToBaseline(task)
	default:
		t.Fatalf("unknown benchmark class %q", class)
		return BaselineComparison{}
	}
}

// benchmarkMatrix runs the full (repo × class) matrix, returning the rows.
// It is used twice by the exit-gate test to prove reproducibility.
func benchmarkMatrix(t *testing.T) []benchmarkRow {
	t.Helper()
	repos := []struct {
		name   string
		root   string
		symbol string
	}{
		{"microservice", safeChangeRoot(t), "UserService"},
		{"legacy", legacyBenchmarkRepo(t), "OrderService"},
	}
	classes := []string{"lookup", "small-change", "architecture", "incident"}

	var rows []benchmarkRow
	for _, r := range repos {
		p, err := New(r.root)
		if err != nil {
			t.Fatalf("%s New: %v", r.name, err)
		}
		ts := NewTaskService(p, nil).WithAgentID("bench")
		for _, class := range classes {
			b := runBenchmarkClass(t, ts, r.name, class, r.symbol)
			rows = append(rows, benchmarkRow{Repo: r.name, Class: class, BaselineComparison: b})
		}
	}
	return rows
}

// renderBenchmarkSummary renders the ACTUAL measured results (
// "Only publish actual measured results") as a compact table.
func renderBenchmarkSummary(rows []benchmarkRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-13s %-13s %10s %10s %10s %10s %10s\n",
		"REPO", "CLASS", "KERN_TOK", "IN_RED%", "TOOL_RED%", "RETRY_RED%", "COST_RED%")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-13s %-13s %10d %9.1f%% %9.1f%% %9.1f%% %9.1f%%\n",
			r.Repo, r.Class, r.KernTokens,
			r.InputReductionPct, r.ToolCallReductionPct, r.RetryReductionPct, r.CostReductionPct)
	}
	return b.String()
}

// TestReproducibleBenchmarkSuite is the exit gate: a reproducible
// benchmark suite exists. It runs the (benchmark repo × task class) matrix
// TWICE against fresh fixtures and asserts the measurements are identical
// (deterministic), then surfaces the ACTUAL measured results.
func TestReproducibleBenchmarkSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e; skipped with -short")
	}
	first := benchmarkMatrix(t)
	second := benchmarkMatrix(t)
	if len(first) == 0 {
		t.Fatal("benchmark suite produced no rows")
	}
	if len(first) != len(second) {
		t.Fatalf("matrix sizes differ across runs: %d vs %d (not reproducible)", len(first), len(second))
	}
	// Reproducibility: every measured reduction must be identical across runs.
	for i := range first {
		if first[i].InputReductionPct != second[i].InputReductionPct ||
			first[i].ToolCallReductionPct != second[i].ToolCallReductionPct ||
			first[i].RetryReductionPct != second[i].RetryReductionPct ||
			first[i].CostReductionPct != second[i].CostReductionPct {
			t.Fatalf("run %d (%s/%s) not reproducible: %+v vs %+v",
				i, first[i].Repo, first[i].Class, first[i], second[i])
		}
		if first[i].KernTokens <= 0 {
			// The token dimension is meaningful only for classes whose tasks
			// carry a context packet (lookup/small-change); report-producing
			// classes (architecture/incident) are measured on the tool-call,
			// retry, and cost dimensions instead.
			if first[i].Class == "lookup" || first[i].Class == "small-change" {
				t.Errorf("run %d (%s/%s): kern tokens = %d, want > 0", i, first[i].Repo, first[i].Class, first[i].KernTokens)
			}
		}
		// The tool-call reduction (4x baseline) must hold for every class.
		if first[i].ToolCallReductionPct != 75 {
			t.Errorf("run %d (%s/%s): tool-call reduction = %.1f%%, want 75%%", i, first[i].Repo, first[i].Class, first[i].ToolCallReductionPct)
		}
	}
	t.Logf("Phase 17 benchmark suite (reproducible across two runs):\n%s", renderBenchmarkSummary(first))
}
