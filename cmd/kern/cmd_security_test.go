package main

import "testing"

func TestParseTaintRange(t *testing.T) {
	cases := []struct {
		in       string
		from, to string
		wantErr  bool
	}{
		{in: "HEAD~1..HEAD", from: "HEAD~1", to: "HEAD"},
		{in: "HEAD..", from: "HEAD", to: ""},
		{in: "..HEAD", from: "", to: "HEAD"},
		{in: "..", from: "", to: ""}, // working tree
		{in: "abc", wantErr: true},
		{in: "a..b..c", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, c := range cases {
		from, to, err := parseTaintRange(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseTaintRange(%q): expected error, got from=%q to=%q", c.in, from, to)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTaintRange(%q): unexpected error %v", c.in, err)
			continue
		}
		if from != c.from || to != c.to {
			t.Errorf("parseTaintRange(%q) = (%q, %q), want (%q, %q)", c.in, from, to, c.from, c.to)
		}
	}
}
