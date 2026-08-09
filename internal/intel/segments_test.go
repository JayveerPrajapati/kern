package intel

import (
	"reflect"
	"testing"
)

func TestSplitIdentifierSegments(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"", nil},
		{"Send", []string{"send"}},
		{"OrderStateMachine", []string{"order", "state", "machine"}},
		{"HTMLParser", []string{"html", "parser"}},
		{"order_state", []string{"order", "state"}},
		{"order-state", []string{"order", "state"}},
		{"base64Encode", []string{"base64", "encode"}},
		{"readindex", []string{"readindex"}},
		{"g2", []string{"g2"}},
		{"_", nil},
		{"HTTPRequestBuilder", []string{"http", "request", "builder"}},
		{"a", nil},   // below min segment length
		{"123", nil}, // digit-only dropped
		{"api_v2_client", []string{"api", "v2", "client"}},
		{"myVeryLongFunctionNameThatExceedsNormalSegmentBounds", []string{"my", "very", "long", "function", "name", "that", "exceeds", "normal", "segment", "bounds"}},
	}
	for _, c := range cases {
		got := splitIdentifierSegments(c.name)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitIdentifierSegments(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSplitIdentifierSegmentsAcronymRun(t *testing.T) {
	// Acronym runs stay glued until the last Upper before a lowercase (matches
	// codegraph: "HTMLParser" -> html/parser), then each hump splits.
	got := splitIdentifierSegments("XMLHTTPParserClient")
	want := []string{"xmlhttp", "parser", "client"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeProseWord(t *testing.T) {
	cases := map[string]string{
		"résolution": "resolution",
		"Résolution": "resolution",
		"RÉSOLUTION": "resolution",
		"references": "references",
		"café":       "cafe",
		"naïve":      "naive",
		"simple":     "simple",
	}
	for in, want := range cases {
		if got := normalizeProseWord(in); got != want {
			t.Errorf("normalizeProseWord(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSegmentLookupVariants(t *testing.T) {
	cases := []struct {
		word string
		want []string
	}{
		{"services", []string{"services", "service"}},
		{"machines", []string{"machines", "machine"}},
		{"classes", []string{"classes", "class"}},
		{"boxes", []string{"boxes"}}, // 5 chars, can't strip the full -es
		{"hashes", []string{"hashes", "hash"}},
		{"process", []string{"process"}}, // -ss is singular, no strip
		{"state", []string{"state"}},
		{"users", []string{"users", "user"}},
		{"caches", []string{"caches", "cach", "cache"}}, // ambiguous -ches emits both
	}
	for _, c := range cases {
		got := segmentLookupVariants(c.word)
		if len(got) != len(c.want) {
			t.Errorf("segmentLookupVariants(%q) = %v, want %v", c.word, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("segmentLookupVariants(%q) = %v, want %v", c.word, got, c.want)
				break
			}
		}
	}
}

func TestQueryWords(t *testing.T) {
	got := queryWords(" state-machine  LOAD index ")
	want := []string{"state", "machine", "load", "index"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("queryWords = %v, want %v", got, want)
	}
}
