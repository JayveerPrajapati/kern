package intel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// AnchorLine returns the one-line evidence anchor for a report subject:
// "evidence: <file>:<line> <evidence-sha256:…>". Resolution (exact name,
// FullName, ranked-search fallback) and the certificate formula are
// identical to the evidence_anchor tool, so the appended line reproduces
// that tool's certificate byte-for-byte — a downstream agent can re-run
// kern evidence_anchor on the cited file:line and match it
// (zero-hallucination claims on impact/what_if/explore output, P2).
// Unresolvable symbols yield "" (no anchor appended).
//
// It lives in intel rather than evidence because evidence→domain→intel
// would be an import cycle; it needs only the index.
func AnchorLine(ix *index.Index, symbol string) string {
	if ix == nil || symbol == "" {
		return ""
	}
	var file, full string
	var line int
	found := false
	for _, sym := range ix.Symbols {
		if sym.Name == symbol || sym.FullName() == symbol {
			file, full, line = sym.File, sym.FullName(), sym.Line
			found = true
			break
		}
	}
	if !found {
		if matches := ix.Search(symbol, 1); len(matches) > 0 {
			file, full, line = matches[0].File, matches[0].FullName(), matches[0].Line
			found = true
		}
	}
	if !found {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d|%v|%s", file, full, line, true, ix.Root)
	return fmt.Sprintf("evidence: %s:%d %s", file, line, "evidence-sha256:"+hex.EncodeToString(h.Sum(nil))[:16])
}
