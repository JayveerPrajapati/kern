package whatif

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestExtractSymbols(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "bare symbol no spaces",
			in:   "GetMySQLDB",
			want: []string{"GetMySQLDB"},
		},
		{
			name: "prose with bare camelcase and file path",
			in:   "Remove GetMySQLDB from connections/db_connections.go",
			want: []string{"GetMySQLDB", "db_connections"},
		},
		{
			name: "prose with qualified name",
			in:   "Refactor the 558-line SymphonyCMAASAdaptor.ConfigureNF method",
			want: []string{"SymphonyCMAASAdaptor.ConfigureNF"},
		},
		{
			name: "prose with backtick quoted symbol",
			in:   "Refactor the `translate` function",
			want: []string{"translate"},
		},
		{
			name: "prose with no symbols",
			in:   "just some prose with no symbols here",
			want: nil,
		},
		{
			name: "camelCase lowercase start",
			in:   "refactor loadQuestion",
			want: []string{"loadQuestion"},
		},
		{
			name: "camelCase lowercase start replica",
			in:   "remove replicaCount",
			want: []string{"replicaCount"},
		},
		{
			name: "snake_case identifier",
			in:   "fix the process_service_request bug",
			want: []string{"process_service_request"},
		},
		{
			name: "snake_case file stem",
			in:   "refactor the get_slice_profile function",
			want: []string{"get_slice_profile"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractSymbols(tc.in)
			if len(tc.want) == 0 && len(got) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExtractSymbols(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractSymbolsSkipsInflectedLeadVerbs(t *testing.T) {
	cands := ExtractSymbols("what breaks if I remove the translate function from cmaas_controller?")
	if len(cands) == 0 || cands[0] != "translate" {
		t.Fatalf("ExtractSymbols = %v, want leading candidate %q", cands, "translate")
	}
	for _, bad := range []string{"breaks", "breaks", "remove", "removes"} {
		for _, c := range cands {
			if c == bad {
				t.Errorf("stoplisted verb %q leaked into candidates %v", bad, cands)
			}
		}
	}
}

func TestIsNetNewFeature(t *testing.T) {
	cases := []struct {
		intent string
		want   bool
	}{
		{"Add REST endpoint for consumer lag", true},
		{"Create new notification service", true},
		{"Introduce kafka retry topic", true},
		{"implement dead letter queue", true},
		{"new telemetry pipeline", true},
		{"build healthcheck endpoint", true},
		{"Refactor ResponseWrapperFactory.build", false},
		{"Remove GetMySQLDB", false},
		{"Fix null pointer in processSingle", false},
	}
	for _, tc := range cases {
		got := IsNetNewFeature(tc.intent)
		if got != tc.want {
			t.Errorf("IsNetNewFeature(%q) = %v, want %v", tc.intent, got, tc.want)
		}
	}
}

func TestExtractSymbolsNetNewFeatureStopwords(t *testing.T) {
	cands := ExtractSymbols("Add REST endpoint for consumer lag")
	for _, bad := range []string{"rest", "endpoint", "api", "lag"} {
		for _, c := range cands {
			if strings.EqualFold(c, bad) {
				t.Errorf("stoplisted word %q leaked into candidates %v", bad, cands)
			}
		}
	}
}

// Stopword-colliding symbols: "fix the Add function" mentions only words that
// collide with the change-verb stoplist, yet "Add" is a real symbol. The
// index-aware extractor must surface it; the pure extractor (no index) keeps
// its old stopword behavior.
func TestExtractSymbolsIndexKeepsStopwordCollidingSymbol(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "Add", File: "math.go", Line: 10},
		},
		Calls:   map[string][]index.CallEdge{},
		Callers: map[string][]string{},
	}
	cands := ExtractSymbolsIndex("fix the Add function", ix)
	if len(cands) == 0 || cands[0] != "Add" {
		t.Fatalf("ExtractSymbolsIndex(%q) = %v, want leading candidate %q", "fix the Add function", cands, "Add")
	}
	if pure := ExtractSymbols("fix the Add function"); len(pure) != 0 {
		t.Errorf("pure ExtractSymbols must keep its stopword behavior, got %v", pure)
	}
}
