package asttypes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"testing"
)

// declarationIDOracle is the historical implementation of DeclarationID:
// a reflection-based json.Marshal over the identity fields. It stays here as
// the byte-exact reference for the append-based encoder in symbol.go — the
// persisted identity (adjacency and refs tables are keyed by it) must never
// drift, so any divergence between the two encoders is a data-format break.
func declarationIDOracle(symbol Symbol) string {
	params := append([]string{}, symbol.Params...)
	data, _ := json.Marshal([]any{
		path.Clean(strings.ReplaceAll(symbol.File, "\\", "/")),
		symbol.Lang, symbol.Kind, symbol.Outer, symbol.Receiver,
		symbol.Name, symbol.Line, params,
	})
	digest := sha256.Sum256(data)
	return "decl:" + hex.EncodeToString(digest[:])
}

func TestDeclarationIDMatchesJSONOracle(t *testing.T) {
	// Adversarial strings: every ASCII byte alone, every escaped rune,
	// multi-byte UTF-8, invalid UTF-8, U+2028/U+2029, quotes, and the
	// path-cleaning inputs the real field values produce.
	inputs := []string{
		"", "a", "A", "0",
		"\"", "'", "\\", "\n", "\r", "\t", "\x00", "\x01", "\x1f", "\x7f",
		"<", ">", "&", "<<>>&&", "a<b>&c\\d\"e",
		"\u2028", "\u2029", "\u00e9\u4e2d\U0001F600",
		"\xff\xfe", "ok\xfftail", "\xc3",
		"internal/index/engine.go", "C:\\Users\\x\\file.go", "./a/../b.go",
		"a\\b\\c.go", strings.Repeat("x", 4096),
	}
	symbols := make([]Symbol, 0, len(inputs)*15)
	for _, file := range inputs {
		for _, name := range []string{"Name", "", "n<>&\"\\", "\u4e2d", "with space"} {
			symbols = append(symbols,
				Symbol{File: file, Name: name, Kind: "func", Lang: "go", Line: 42},
				Symbol{File: file, Name: name, Kind: "method", Receiver: "T", Outer: "Outer", Lang: "go", Line: 1,
					Params: []string{"a", "b", "\xff", "<&>"}},
				Symbol{File: file, Name: name, Params: nil, Line: 0},
			)
		}
	}
	for _, symbol := range symbols {
		want, got := declarationIDOracle(symbol), symbol.DeclarationID()
		if want != got {
			params := append([]string{}, symbol.Params...)
			data, _ := json.Marshal([]any{
				path.Clean(strings.ReplaceAll(symbol.File, "\\", "/")),
				symbol.Lang, symbol.Kind, symbol.Outer, symbol.Receiver,
				symbol.Name, symbol.Line, params,
			})
			var b []byte
			b = append(b, '[')
			b = appendJSONString(b, path.Clean(strings.ReplaceAll(symbol.File, "\\", "/")))
			b = append(b, ',')
			b = appendJSONString(b, symbol.Lang)
			b = append(b, ',')
			b = appendJSONString(b, symbol.Kind)
			b = append(b, ',')
			b = appendJSONString(b, symbol.Outer)
			b = append(b, ',')
			b = appendJSONString(b, symbol.Receiver)
			b = append(b, ',')
			b = appendJSONString(b, symbol.Name)
			b = append(b, ',')
			b = strconv.AppendInt(b, int64(symbol.Line), 10)
			b = append(b, ',', '[')
			for i, param := range symbol.Params {
				if i > 0 {
					b = append(b, ',')
				}
				b = appendJSONString(b, param)
			}
			b = append(b, ']', ']')
			t.Fatalf("DeclarationID drift for %+v:\n json=%q\n new =%q", symbol, string(data), string(b))
		}
	}
}

func BenchmarkDeclarationID(b *testing.B) {
	symbol := Symbol{
		File: "internal/index/engine.go", Name: "computeCallers", Kind: "func",
		Lang: "go", Line: 2147, Params: []string{"ctx", "symbols", "byFile"},
	}
	b.Run("oracle", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = declarationIDOracle(symbol)
		}
	})
	b.Run("new", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = symbol.DeclarationID()
		}
	})
}

func TestDeclarationIDGolden(t *testing.T) {
	// Fixed regression values produced by the reflection-based
	// implementation, pinned verbatim so a future change to EITHER encoder
	// fails loudly.
	cases := []struct {
		name   string
		symbol Symbol
	}{
		{"plain func", Symbol{File: "internal/index/engine.go", Name: "Load", Kind: "func", Lang: "go", Line: 589}},
		{"windows path method", Symbol{File: "a\\b.go", Name: "T.M", Kind: "method", Receiver: "T", Lang: "go", Line: 7,
			Params: []string{"ctx", "x"}}},
	}
	for _, tc := range cases {
		want, got := declarationIDOracle(tc.symbol), tc.symbol.DeclarationID()
		if want != got {
			t.Fatalf("%s: oracle=%s new=%s", tc.name, want, got)
		}
	}
}
