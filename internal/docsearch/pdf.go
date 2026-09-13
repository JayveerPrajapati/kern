//go:build pdf

package docsearch

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

// maxPDFText caps the accumulated text extracted from a PDF, mirroring the
// maxFetchedSize bound: a pathological document (hundreds of pages) must not
// exhaust heap before chunking. Once exceeded, remaining pages are skipped.
const maxPDFText = 4 << 20 // 4 MiB

func init() {
	extRe = regexp.MustCompile(`\.(md|markdown|txt|rst|adoc|asciidoc|org|pdf)$`)
	readDocFile = extractPDFText
}

// extractPDFText extracts the text layer of a PDF file. It is text-layer
// only — no OCR — and pure Go, compiled only with -tags pdf (see the build
// constraint above and readDocFile in docsearch.go). Pages are extracted
// 1-based via ledongthuc/pdf and joined with a page-separator line; the
// accumulated text is capped at maxPDFText bytes, after which further pages
// are skipped. Documented limitation: PDFs with embedded fonts and no text
// layer (e.g. pure scans) yield empty text.
func extractPDFText(path string) ([]byte, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		// Not a parseable PDF. The -tags pdf init routes every indexed file
		// through this decoder, so text documents (.md, .txt, ...) must fall
		// back to the default raw read to keep indexing them. A file that
		// fails PDF parsing but carries a .pdf extension is skipped (returning
		// the parse error) rather than indexed as raw bytes.
		if strings.HasSuffix(strings.ToLower(path), ".pdf") {
			return nil, err
		}
		return os.ReadFile(path)
	}
	defer f.Close()

	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		txt, err := r.Page(i).GetPlainText(nil)
		if err != nil {
			continue // page without a readable text layer; skip it
		}
		if b.Len()+len(txt) > maxPDFText {
			break // cap reached; stop appending further pages
		}
		if i > 1 {
			b.WriteString(fmt.Sprintf("\n\n--- page %d ---\n", i))
		}
		b.WriteString(txt)
	}
	return []byte(b.String()), nil
}
