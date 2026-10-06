package main

import "testing"

func TestExtractUsage(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	cases := []struct {
		name    string
		raw     string
		in, out *int64
		events  int
		note    bool
	}{
		{
			name: "usage shape",
			raw:  `{"usage":{"input":10,"output":5}}` + "\n",
			in:   i64(10), out: i64(5), events: 1,
		},
		{
			name: "nested tokens shape",
			raw:  `{"tokens":{"input":7,"output":3}}` + "\n",
			in:   i64(7), out: i64(3), events: 1,
		},
		{
			name: "flat shape",
			raw:  `{"input_tokens":4,"output_tokens":2}` + "\n",
			in:   i64(4), out: i64(2), events: 1,
		},
		{
			name: "deeply wrapped",
			raw:  `{"type":"message","data":{"info":{"tokens":{"input":9,"output":1}}}}` + "\n",
			in:   i64(9), out: i64(1), events: 1,
		},
		{
			name: "two events sum and max",
			raw:  `{"usage":{"input":10,"output":5}}` + "\n" + `{"usage":{"input":7,"output":3}}` + "\n",
			in:   i64(17), out: i64(8), events: 2,
		},
		{
			name: "per-event max across duplicate shapes",
			raw:  `{"usage":{"input":10,"output":2},"tokens":{"input":4,"output":3}}` + "\n",
			in:   i64(10), out: i64(3), events: 1,
		},
		{
			name: "empty stream",
			raw:  "",
			note: true,
		},
		{
			name: "garbage",
			raw:  "not json at all\n",
			note: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := extractUsage([]byte(tc.raw))
			if tc.note {
				if u.Note == "" {
					t.Errorf("expected a note for unparseable stream, got %+v", u)
				}
				if u.InputSum != nil || u.OutputSum != nil {
					t.Errorf("expected nil tokens, got %+v", u)
				}
				return
			}
			if u.InputSum == nil || *u.InputSum != *tc.in {
				t.Errorf("input sum = %v, want %d", u.InputSum, *tc.in)
			}
			if u.OutputSum == nil || *u.OutputSum != *tc.out {
				t.Errorf("output sum = %v, want %d", u.OutputSum, *tc.out)
			}
			if u.Events != tc.events {
				t.Errorf("events = %d, want %d", u.Events, tc.events)
			}
		})
	}
}
