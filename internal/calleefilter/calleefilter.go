// Package calleefilter is the single shared Go callee noise filter: builtins
// and bare type conversions never become call edges, and builder chains
// (method calls chained on the result of another call) longer than two
// segments are dropped at record time. Two-segment qualified forms (db.Do)
// and receiver-field chains (a.taskSvc.Deploy) are preserved.
package calleefilter

import (
	"go/ast"
	"strings"
)

// goBuiltinCallees is the shared denylist of Go builtins and bare type
// conversions that must never be recorded as callees (len, append, string,
// uint, ...): language primitives, not project symbols.
var goBuiltinCallees = map[string]bool{
	"len": true, "cap": true, "make": true, "new": true, "append": true,
	"copy": true, "delete": true, "panic": true, "print": true, "println": true,
	"recover": true,
	"string":  true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true,
	"bool": true, "byte": true, "rune": true, "error": true,
	"min": true, "max": true, "clear": true, // Go 1.21 builtins
}

// IsTestSymbol reports whether a symbol lives in a Go test file. The
// Test*/Benchmark* name prefixes alone no longer classify a symbol (L5-5):
// a production TestConnection/BenchmarkPrice in a non-test file is a real
// project symbol — Go test entry points must live in _test.go anyway.
func IsTestSymbol(file string) bool {
	return strings.HasSuffix(file, "_test.go")
}

// IsNoiseCallee reports whether a raw Go callee name is record-time noise:
// a bare builtin/type conversion, or a dotted chain longer than two segments
// whose receiver chain contains a call (chainHasCall — the builder shape).
// Two-segment qualified forms (db.Do) and receiver-field chains are kept.
// chainHasCall is only meaningful for Go; name-only call sites pass true.
func IsNoiseCallee(name string, chainHasCall bool) bool {
	if strings.Count(name, ".") >= 2 {
		return chainHasCall
	}
	if strings.Contains(name, ".") {
		return false // two-segment qualified form: keep
	}
	return goBuiltinCallees[name]
}

// testingTBCallees lists the testing.T/TB/B/F/M methods conventionally invoked
// on the short receivers (t, b, f, m, tb) inside test files: harness plumbing
// (t.Run subtests, t.Fatalf guards), not project calls — deep-verify L3 F3-6
// measured ~13k such edges polluting views in the kern repo alone.
var testingTBCallees = map[string]bool{
	"Run": true, "Error": true, "Errorf": true, "Fatal": true, "Fatalf": true,
	"Fail": true, "FailNow": true, "Failed": true, "Log": true, "Logf": true,
	"Name": true, "Helper": true, "Parallel": true, "Skip": true, "Skipf": true,
	"SkipNow": true, "Skipped": true, "Cleanup": true, "TempDir": true,
	"Setenv": true, "Chdir": true, "Deadline": true, "Short": true, "Verbose": true,
	"Reset": true, "ResetTimer": true, "StartTimer": true, "StopTimer": true,
	"SetBytes": true, "SetParallelism": true, "ReportAllocs": true, "ReportMetric": true,
	"Add": true, "Fuzz": true,
}

// IsTestingReceiverCallee reports whether name is a testing.T/B/F/M method
// call in the conventional short-receiver form (t.Run, b.Reset, tb.Helper) or
// its record-time resolved form (T.Fatalf, B.Reset — the extractor's
// parameter-type inference rewrites `t.Fatalf` to the bare testing.T type,
// "T.Fatalf", with INFERRED confidence) — a name-shape check only; the
// caller's test-file context is decided by IsNoiseCalleeFor.
func IsTestingReceiverCallee(name string) bool {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return false
	}
	switch name[:dot] {
	case "t", "b", "f", "m", "tb", "T", "B", "F", "M", "TB":
		return testingTBCallees[name[dot+1:]]
	}
	return false
}

// IsNoiseCalleeFor is IsNoiseCallee plus the test-file rule (F3-6): inside a
// _test.go caller, testing.T/B method calls on the conventional short
// receiver are harness plumbing, never project callees. Production callers
// keep every two-segment form — a production type whose receiver happens to
// be a short `t` with a Run method is never filtered.
func IsNoiseCalleeFor(name string, chainHasCall bool, callerFile string) bool {
	return IsNoiseCallee(name, chainHasCall) ||
		(IsTestSymbol(callerFile) && IsTestingReceiverCallee(name))
}

// ChainHasCall reports whether a Go callee expression is a dotted chain whose
// receiver chain contains a call expression — the builder-chain shape
// (logger.Info().Int().Msg()) that never resolves to a project symbol. Pure
// receiver-field chains (a.taskSvc.Deploy) contain no call and are preserved
// for the merge-time field rewrite.
func ChainHasCall(fun ast.Expr) bool {
	switch t := fun.(type) {
	case *ast.SelectorExpr:
		return ChainHasCall(t.X)
	case *ast.CallExpr:
		return true
	case *ast.IndexExpr:
		return ChainHasCall(t.X)
	case *ast.IndexListExpr:
		return ChainHasCall(t.X)
	case *ast.ParenExpr:
		return ChainHasCall(t.X)
	case *ast.TypeAssertExpr:
		return ChainHasCall(t.X)
	case *ast.StarExpr:
		return ChainHasCall(t.X)
	case *ast.UnaryExpr:
		return ChainHasCall(t.X)
	case *ast.CompositeLit:
		return ChainHasCall(t.Type)
	}
	return false
}
