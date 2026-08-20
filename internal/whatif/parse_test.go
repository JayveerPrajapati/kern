package whatif

import (
	"reflect"
	"testing"
)

func TestExtractSymbols(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "bare symbol no spaces",
			in:   "GetMySQLDB",
			want: []string{"GetMySQLDB"},
		},
		{
			name: "prose with bare camelcase and file path",
			in:   "Remove GetMySQLDB from connections/db_connections.go",
			want: []string{"GetMySQLDB", "db_connections"},
		},
		{
			name: "prose with qualified name",
			in:   "Refactor the 558-line SymphonyCMAASAdaptor.ConfigureNF method",
			want: []string{"SymphonyCMAASAdaptor.ConfigureNF"},
		},
		{
			name: "prose with backtick quoted symbol",
			in:   "Refactor the `translate` function",
			want: []string{"translate"},
		},
		{
			name: "prose with no symbols",
			in:   "just some prose with no symbols here",
			want: nil,
		},
		{
			name: "camelCase lowercase start",
			in:   "refactor loadQuestion",
			want: []string{"loadQuestion"},
		},
		{
			name: "camelCase lowercase start replica",
			in:   "remove replicaCount",
			want: []string{"replicaCount"},
		},
		{
			name: "snake_case identifier",
			in:   "fix the process_service_request bug",
			want: []string{"process_service_request"},
		},
		{
			name: "snake_case file stem",
			in:   "refactor the get_slice_profile function",
			want: []string{"get_slice_profile"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractSymbols(tc.in)
			if len(tc.want) == 0 && len(got) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExtractSymbols(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}