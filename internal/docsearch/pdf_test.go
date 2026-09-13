//go:build pdf

package docsearch

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// minimalPDF builds a minimal, valid single-page PDF whose text layer
// contains text. It is assembled programmatically with correct byte offsets
// (no binary blob in the repo): a catalog, one pages node, one page with a
// MediaBox and an uncompressed Contents stream holding a single text-showing
// operation, one Helvetica font object, an xref table and a trailer. The
// fixture is only parsed by the pdf decoder under -tags pdf.
func minimalPDF(text string) []byte {
	var b bytes.Buffer
	write := func(s string) { b.WriteString(s) }

	write("%PDF-1.4\n")

	// Object bodies. The Contents stream /Length is the exact byte length of
	// the stream content between "stream\n" and "\nendstream".
	streamContent := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		"<< /Length " + strconv.Itoa(len(streamContent)) + " >>\nstream\n" + streamContent + "\nendstream",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	offsets := make([]int, len(objs))
	for i, body := range objs {
		offsets[i] = b.Len()
		write(strconv.Itoa(i+1) + " 0 obj\n" + body + "\nendobj\n")
	}

	xrefOffset := b.Len()
	write("xref\n0 " + strconv.Itoa(len(objs)+1) + "\n")
	write("0000000000 65535 f \n")
	for _, off := range offsets {
		write(fmt.Sprintf("%010d 00000 n \n", off))
	}
	write("trailer\n<< /Size " + strconv.Itoa(len(objs)+1) + " /Root 1 0 R >>\n")
	write("startxref\n" + strconv.Itoa(xrefOffset) + "\n%%EOF\n")
	return b.Bytes()
}

// TestIndexDirIndexesPDF: with -tags pdf, IndexDir must index a .pdf file —
// readDocFile is overridden by extractPDFText and extRe accepts .pdf. The
// fixture's text layer must surface as a chunk.
func TestIndexDirIndexesPDF(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "doc.pdf", string(minimalPDF("Hello from kern pdf ingestion. This longer sentence ensures the extracted text exceeds the forty character minimum chunk length used by ChunkText.")))
	writeDoc(t, root, "notes.md", "# Notes\n\nRegular markdown content about the deploy pipeline here.\n")

	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Docs) == 0 {
		t.Fatal("expected docs to be indexed")
	}

	foundPDF := false
	foundMarkdown := false
	for _, d := range ix.Docs {
		if strings.Contains(d.Chunk.Text, "Hello from kern pdf ingestion") {
			foundPDF = true
		}
		if strings.HasPrefix(d.Chunk.File, "notes") {
			foundMarkdown = true
		}
	}
	if !foundPDF {
		t.Fatal("no chunk contains the PDF text; extractPDFText may have yielded empty text")
	}
	if !foundMarkdown {
		t.Fatal("expected the .md doc to be indexed alongside the PDF")
	}
}

// TestSearchFindsPDFText: a query over the PDF's text must surface at least
// one scored chunk carrying that text.
func TestSearchFindsPDFText(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "doc.pdf", string(minimalPDF("Hello from kern pdf ingestion. This longer sentence ensures the extracted text exceeds the forty character minimum chunk length used by ChunkText.")))
	writeDoc(t, root, "notes.md", "# Notes\n\nUnrelated markdown about the deploy pipeline here.\n")

	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}

	scores := ix.Search("pdf ingestion", 3)
	if len(scores) == 0 {
		t.Fatal("expected at least one search result")
	}
	for _, s := range scores {
		if strings.Contains(s.Doc.Chunk.Text, "Hello from kern pdf ingestion") {
			return // at least one scored chunk carries the PDF text
		}
	}
	t.Fatalf("no scored chunk contains the PDF text; got %d scores, e.g. %q", len(scores), scores[0].Doc.Chunk.Text)
}
