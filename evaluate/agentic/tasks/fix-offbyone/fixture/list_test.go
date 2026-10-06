package list

import (
	"reflect"
	"testing"
)

func TestItemsReturnsExactlyCount(t *testing.T) {
	xs := []int{1, 2, 3, 4, 5}
	for _, tc := range []struct {
		start, count int
		want         []int
	}{
		{0, 2, []int{1, 2}},
		{1, 3, []int{2, 3, 4}},
		{3, 10, []int{4, 5}}, // clamped to the tail
		{10, 2, []int{}},     // clamped to empty
	} {
		if got := Items(tc.start, tc.count, xs); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Items(%d, %d) = %v, want %v", tc.start, tc.count, got, tc.want)
		}
	}
}
