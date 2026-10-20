package jobs

import "testing"

func TestDrain(t *testing.T) {
	got := Drain([]string{"x", "y"})
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("Drain = %v", got)
	}
}
