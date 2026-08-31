package loop

import "strings"

// Deterministic pause-trigger helpers. These are pure, offline
// predicates a caller can use inside LoopConfig.PauseTrigger to detect the
// generalized pause conditions (scope expansion, confidence drop, unexpected
// file/tool, policy change, verification regression) WITHOUT an LLM. Each is
// trivial and deterministic.

// ScopeExpanded reports whether newScope is not a prefix (or equal) of
// originalScope, i.e. the scope grew beyond what was originally granted. It is
// a simple containment check: true when newScope is not contained in
// originalScope. Used to PAUSE on scope expansion .
func ScopeExpanded(originalScope, newScope string) bool {
	return !strings.HasPrefix(originalScope, newScope)
}

// ConfidenceDropped reports whether the current confidence cur has dropped
// meaningfully below the previous confidence prev, i.e. cur < prev - threshold.
// Used to PAUSE on a confidence drop .
func ConfidenceDropped(prev, cur float64, threshold float64) bool {
	return cur < prev-threshold
}

// UnexpectedTool reports whether the given used tool is not in the expected set.
// The expected set maps allowed tool names to true. Used to PAUSE on an
// unexpected tool invocation .
func UnexpectedTool(expected map[string]bool, used string) bool {
	if expected == nil {
		return true
	}
	return !expected[used]
}

// UnexpectedFile reports whether the given file path is not in the expected
// set of allowed files. The expected set maps allowed file paths to true. Used
// to PAUSE on an unexpected file being touched/read .
func UnexpectedFile(expected map[string]bool, used string) bool {
	if expected == nil {
		return true
	}
	return !expected[used]
}

// PolicyChanged reports whether the policy changed between two recorded states:
// true when both hashes are non-empty and differ. A nil or empty prior hash means
// no baseline was recorded and is NOT treated as a change (avoid false pauses).
// Used to PAUSE on a policy change .
func PolicyChanged(priorHash, currentHash string) bool {
	return priorHash != "" && currentHash != "" && priorHash != currentHash
}

// VerificationRegressed reports whether the current verification verdict is a
// stricter/failed verdict than the prior one. A verdict is considered a failure
// when it is a case-insensitive "FAIL" or contains "FAIL". A regression occurs
// when the current verdict is a failure while the prior was not (e.g. "PASS"→
// "FAIL"). Used to PAUSE on a verification regression .
func VerificationRegressed(priorVerdict, currentVerdict string) bool {
	curFailed := strings.Contains(strings.ToUpper(currentVerdict), "FAIL")
	priorFailed := strings.Contains(strings.ToUpper(priorVerdict), "FAIL")
	return curFailed && !priorFailed
}
