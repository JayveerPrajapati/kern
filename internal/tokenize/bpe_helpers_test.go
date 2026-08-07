package tokenize

import (
	"reflect"
	"testing"
)

func TestBytesToIDs(t *testing.T) {
	cases := []struct {
		in   []byte
		want []int
	}{
		{nil, []int{}},
		{[]byte(""), []int{}},
		{[]byte("ab"), []int{97, 98}},
		{[]byte{0}, []int{0}},
		{[]byte{255}, []int{255}},
	}
	for _, c := range cases {
		if got := bytesToIDs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("bytesToIDs(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestApplyMerge(t *testing.T) {
	cases := []struct {
		name  string
		ids   []int
		pair  pairKey
		newID int
		want  []int
	}{
		{"no match", []int{1, 2, 3}, pairKey{9, 9}, 300, []int{1, 2, 3}},
		{"single", []int{1, 2, 3}, pairKey{1, 2}, 300, []int{300, 3}},
		{"overlapping", []int{1, 1, 1}, pairKey{1, 1}, 300, []int{300, 1}},
		{"all", []int{5, 5, 5, 5}, pairKey{5, 5}, 300, []int{300, 300}},
		{"adjacent at end", []int{3, 4}, pairKey{3, 4}, 300, []int{300}},
	}
	for _, c := range cases {
		if got := applyMerge(c.ids, c.pair, c.newID); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: applyMerge(%v, %v, %d) = %v; want %v", c.name, c.ids, c.pair, c.newID, got, c.want)
		}
	}
}

func TestTrainMerges(t *testing.T) {
	// words: "ab" x2, "abc" x1 -> the (a,b) pair is most frequent.
	words := [][]int{
		{97, 98},
		{97, 98},
		{97, 98, 99},
	}
	merges := trainMerges(words, 2)
	if len(merges) != 2 {
		t.Fatalf("trainMerges returned %d merges; want 2: %v", len(merges), merges)
	}
	if merges[0] != [2]int{97, 98} {
		t.Errorf("first merge = %v; want (97,98)", merges[0])
	}
	// After merging (a,b), the remaining pairs are the merged id with (c).
	if merges[1][0] != 256 && merges[1][1] != 256 {
		t.Errorf("second merge should involve the merged id 256, got %v", merges[1])
	}

	if got := trainMerges(words, 0); len(got) != 0 {
		t.Errorf("trainMerges(_, 0) = %v; want none", got)
	}
	// Ties break lexicographically: identical counts for (1,2) and (2,1).
	ties := [][]int{{1, 2, 2, 1}, {1, 2, 2, 1}}
	m := trainMerges(ties, 1)
	if len(m) == 0 {
		t.Fatal("expected a merge from ties")
	}
	if m[0] != [2]int{1, 2} {
		t.Errorf("tie broken wrong: %v; want (1,2)", m[0])
	}
}
