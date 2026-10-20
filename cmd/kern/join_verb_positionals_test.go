package main

import "testing"

// TestJoinVerbPositionals (verb-trap fix, F-IM1/F-WI1/F-PL1 family): a
// leading change-verb across multiple positionals is an unquoted sentence —
// `kern impact remove WriteFileAtomic` previously parsed change="remove" and
// fuzzy-resolved the WRONG symbol. Quoted/explicit-kind forms pass through.
// (Moved here from the approval-family test file when the family extracted
// to internal/approvalcli; joinVerbPositionals lives in the impact command.)
func TestJoinVerbPositionals(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"remove", "WriteFileAtomic"}, []string{"remove WriteFileAtomic"}},
		{[]string{"add", "a", "new", "caching", "layer", "to", "Fit"}, []string{"add a new caching layer to Fit"}},
		{[]string{"remove"}, []string{"remove"}},
		{[]string{"WriteFileAtomic"}, []string{"WriteFileAtomic"}},
		{[]string{"Client.Remove", "change_dependency", "NewTarget"}, nil},
		{[]string{"change", "the", "signature", "of", "Fit"}, []string{"change the signature of Fit"}},
	}
	for _, c := range cases {
		got := joinVerbPositionals(c.in)
		if c.want == nil {
			if len(got) != len(c.in) {
				t.Fatalf("joinVerbPositionals(%v) = %v, want untouched", c.in, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != c.want[0] {
			t.Fatalf("joinVerbPositionals(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
