package verdict

import (
	"reflect"
	"testing"
)

func TestFailedTestNames(t *testing.T) {
	log := `=== RUN TestB
--- PASS: TestB (0.00s)
--- FAIL: TestA (0.00s)
--- FAIL: TestA (0.00s)
--- FAIL: TestC (0.00s)
`
	got := FailedTestNames(log)
	want := []string{"TestA", "TestC"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FailedTestNames() = %q, want %q", got, want)
	}
}
