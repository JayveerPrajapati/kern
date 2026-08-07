package schema

import (
	"encoding/json"
	"testing"
)

func TestIsNumber(t *testing.T) {
	cases := []struct {
		in     any
		want   float64
		wantOK bool
	}{
		{float64(3.5), 3.5, true},
		{int(4), 4, true},
		{json.Number("2.25"), 2.25, true},
		{json.Number("bogus"), 0, false},
		{"12", 0, false},
		{nil, 0, false},
		{true, 0, false},
	}
	for _, c := range cases {
		got, ok := isNumber(c.in)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("isNumber(%v) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestEqJSON(t *testing.T) {
	if !eqJSON(map[string]int{"a": 1}, map[string]int{"a": 1}) {
		t.Error("identical maps should be equal")
	}
	if eqJSON(map[string]int{"a": 1}, map[string]int{"b": 2}) {
		t.Error("different maps should not be equal")
	}
	if !eqJSON([]int{1, 2, 3}, []int{1, 2, 3}) {
		t.Error("identical slices should be equal")
	}
	if eqJSON([]int{1, 2}, []int{2, 1}) {
		t.Error("slice order matters")
	}
	if !eqJSON(nil, nil) {
		t.Error("nil should equal nil")
	}
	if !eqJSON(struct{ A int }{1}, map[string]int{"A": 1}) {
		t.Error("struct and matching map marshal identically")
	}
}
