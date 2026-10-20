package worker

import "testing"

func TestWarm(t *testing.T) {
	if v, ok := Warm().Get("warm"); !ok || v != "1" {
		t.Fatalf("Warm().Get(warm) = %q, %v", v, ok)
	}
}
