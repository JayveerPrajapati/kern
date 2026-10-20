package duplication

// This file carries the duplication-similarity types the verify reuse gate
// needs, extracted from the blueprint scanners family during the pivot.
// The blueprint change-validation service glue (service.Check / KernClient
// cache) stayed with the family; verification only consumes the fingerprint
// model and the structural similarity score.

// Fingerprint is the normalized structural signature of one function,
// produced by kern's fingerprint extraction and consumed by the reuse gate.
type Fingerprint struct {
	FuncName       string // original name (for reporting only)
	SignatureShape string // normalized signature, e.g. "func(1ptr,1int)1err"
	ParamCount     int    // number of parameters
	ReturnCount    int    // number of return values
	ControlFlow    CFFingerprint
	CalledSymbols  []string // normalized call signatures
	LiteralCount   int      // total literals
	StatementCount int      // total statements
}

// CFFingerprint captures control-flow shape counts emitted by kern's
// fingerprint record (control_flow). It feeds controlFlowSimilarity, which
// compares the vectors with cosine similarity as part of the overall
// structural score.
type CFFingerprint struct {
	IfCount     int
	ForCount    int
	RangeCount  int
	SwitchCount int
	ReturnCount int
	DeferCount  int
	GoCount     int
	AssignCount int
	CallCount   int
}
