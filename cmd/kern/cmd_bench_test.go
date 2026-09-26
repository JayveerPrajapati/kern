package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// collectJSONTags gathers every json tag name declared by t, recursing into
// nested struct and []struct fields so the whole benchResult schema is
// compared, not just the top level.
func collectJSONTags(t reflect.Type, tags map[string]bool) {
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			continue
		}
		tags[name] = true
		switch f.Type.Kind() {
		case reflect.Struct:
			collectJSONTags(f.Type, tags)
		case reflect.Slice:
			if f.Type.Elem().Kind() == reflect.Struct {
				collectJSONTags(f.Type.Elem(), tags)
			}
		}
	}
}

// collectFixtureKeys gathers every JSON object key at every nesting level of
// the decoded fixture document (objects and arrays of objects).
func collectFixtureKeys(v any, keys map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			keys[k] = true
			collectFixtureKeys(sub, keys)
		}
	case []any:
		for _, e := range t {
			collectFixtureKeys(e, keys)
		}
	}
}

// TestBenchResultRoundTripGoldenFixture pins the WRITE side of
// .kern/bench.json — cmd/kern's benchResult — against the golden fixture the
// web console's decoder already pins (internal/web/testdata/bench.json, see
// internal/web/bench_schema_test.go's TestBenchReportGoldenFixture). The two
// structs are hand-duplicated; the web test only pins the DECODE side, so a
// field renamed here would pass the web test and silently zero the
// /benchmarks page. This test asserts the fixture's JSON keys EXACTLY match
// benchResult's json tags, in both directions, at every nesting level (audit
// iteration-3 finding 3).
func TestBenchResultRoundTripGoldenFixture(t *testing.T) {
	fixturePath := "../../internal/web/testdata/bench.json" // test cwd is the package dir (cmd/kern); fixture lives two levels up
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", fixturePath, err)
	}

	// The fixture must decode into the write-side schema; a rename here
	// already fails loudly at this unmarshal.
	var res benchResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode %s into benchResult: %v", fixturePath, err)
	}

	// Schema contract: fixture keys (have) must exactly match benchResult's
	// json tags (want).
	want := map[string]bool{}
	collectJSONTags(reflect.TypeOf(benchResult{}), want)

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s document: %v", fixturePath, err)
	}
	have := map[string]bool{}
	collectFixtureKeys(doc, have)

	for k := range want {
		if !have[k] {
			t.Errorf("fixture %s is missing json key %q declared by benchResult (cmd/kern/cmd_bench.go) — update the fixture in lockstep", fixturePath, k)
		}
	}
	for k := range have {
		if !want[k] {
			t.Errorf("benchResult (cmd/kern/cmd_bench.go) has no json tag %q present in fixture %s — rename the field in lockstep with internal/web/builders.go and the fixture", k, fixturePath)
		}
	}
}
