package loop

import (
	"strings"
	"testing"
	"time"
)

// utc is the fixed zone every deterministic test uses: no DST, no local-TZ
// dependence. DST transitions are not representable in a time.FixedZone, so
// the DST-adjacent behavior is exercised instead as non-UTC fixed-zone
// correctness (TestScheduleNextNonUTCFixedZone).
var utc = time.FixedZone("UTC", 0)

func mustParse(t *testing.T, expr string) *Schedule {
	t.Helper()
	s, err := ParseSchedule(expr)
	if err != nil {
		t.Fatalf("ParseSchedule(%q): %v", expr, err)
	}
	return s
}

func TestParseScheduleErrors(t *testing.T) {
	cases := []struct {
		expr string
		want string // substring of the error message
	}{
		{"", "empty expression"},
		{"   ", "empty expression"},
		{"0 0 * *", "expected 5 fields"},
		{"0 0 * * * *", "expected 5 fields"},
		{"60 * * * *", "minute value 60 out of range 0-59"},
		{"* 24 * * *", "hour value 24 out of range 0-23"},
		{"* * 0 * *", "day-of-month value 0 out of range 1-31"},
		{"* * 32 * *", "day-of-month value 32 out of range 1-31"},
		{"* * * 13 *", "month value 13 out of range 1-12"},
		{"* * * 0 *", "month value 0 out of range 1-12"},
		{"* * * * 8", "day-of-week value 8 out of range 0-7"},
		{"* * * * -1", "invalid range"},
		{"abc * * * *", "invalid value \"abc\" in minute field"},
		{"*/0 * * * *", "invalid step"},
		{"*/ * * * *", "invalid step"},
		{"5- * * * *", "invalid range"},
		{"5-2 * * * *", "start 5 > end 2"},
		{"1-70 * * * *", "minute value 60 out of range"},
		{"1,,2 * * * *", "empty element"},
		{"@yearly", "unsupported @-keyword"},
		{"@reboot * * * *", "unsupported @-keyword"},
		{"0 0 31 2 * extra", "expected 5 fields"},
	}
	for _, tc := range cases {
		_, err := ParseSchedule(tc.expr)
		if err == nil {
			t.Errorf("ParseSchedule(%q): expected error containing %q, got nil", tc.expr, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseSchedule(%q): error %q does not contain %q", tc.expr, err, tc.want)
		}
	}
}

func TestScheduleFieldExpansion(t *testing.T) {
	// `*` expands to the full range.
	s := mustParse(t, "* * * * *")
	if len(s.minute) != 60 || s.minute[0] != 0 || s.minute[59] != 59 {
		t.Errorf("minute: want 0-59, got %v", s.minute)
	}
	if len(s.hour) != 24 {
		t.Errorf("hour: want 0-23, got %v", s.hour)
	}
	if s.domRestricted || s.dowRestricted {
		t.Errorf("wildcard dom/dow must be unrestricted")
	}
	// Steps.
	s = mustParse(t, "*/5 * * * *")
	want := []int{0, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55}
	if !equalInts(s.minute, want) {
		t.Errorf("*/5 minute: want %v, got %v", want, s.minute)
	}
	s = mustParse(t, "*/15 * * * *")
	want = []int{0, 15, 30, 45}
	if !equalInts(s.minute, want) {
		t.Errorf("*/15 minute: want %v, got %v", want, s.minute)
	}
	// Lists.
	s = mustParse(t, "1,15,30 * * * *")
	want = []int{1, 15, 30}
	if !equalInts(s.minute, want) {
		t.Errorf("list minute: want %v, got %v", want, s.minute)
	}
	// Ranges.
	s = mustParse(t, "0 9-17 * * *")
	want = []int{9, 10, 11, 12, 13, 14, 15, 16, 17}
	if !equalInts(s.hour, want) {
		t.Errorf("range hour: want %v, got %v", want, s.hour)
	}
	// Mixed list of range + step + single.
	s = mustParse(t, "0,30-32,*/10 * * * *")
	want = []int{0, 10, 20, 30, 31, 32, 40, 50}
	if !equalInts(s.minute, want) {
		t.Errorf("mixed minute: want %v, got %v", want, s.minute)
	}
	// Day-of-week 7 normalizes to 0 (Sunday) and dedupes with 0.
	s = mustParse(t, "0 0 * * 0,7")
	if len(s.dayOfWeek) != 1 || s.dayOfWeek[0] != 0 {
		t.Errorf("dow 0,7: want [0], got %v", s.dayOfWeek)
	}
	if !s.dowRestricted {
		t.Errorf("dow 0,7 must be restricted")
	}
}

func TestScheduleNextBasics(t *testing.T) {
	// Every minute: strictly-after, whole-minute results.
	s := mustParse(t, "* * * * *")
	after := time.Date(2024, 1, 1, 12, 0, 30, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 1, 12, 1, 0, 0, utc)) {
		t.Errorf("every-minute next: got %v, want 12:01", got)
	}
	// An `after` sitting exactly on a fire minute must not fire that minute.
	s = mustParse(t, "0 * * * *")
	after = time.Date(2024, 1, 1, 12, 0, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 1, 13, 0, 0, 0, utc)) {
		t.Errorf("hourly next from on-the-hour: got %v, want 13:00", got)
	}
	after = time.Date(2024, 1, 1, 12, 0, 30, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 1, 13, 0, 0, 0, utc)) {
		t.Errorf("hourly next from 12:00:30: got %v, want 13:00", got)
	}
	// Steps.
	s = mustParse(t, "*/5 * * * *")
	after = time.Date(2024, 1, 1, 12, 3, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 1, 12, 5, 0, 0, utc)) {
		t.Errorf("*/5 next: got %v, want 12:05", got)
	}
	// Lists.
	s = mustParse(t, "1,15,30 * * * *")
	after = time.Date(2024, 1, 1, 12, 1, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 1, 12, 15, 0, 0, utc)) {
		t.Errorf("list next: got %v, want 12:15", got)
	}
	// Ranges roll to the next day.
	s = mustParse(t, "0 9-17 * * *")
	after = time.Date(2024, 1, 1, 17, 30, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 2, 9, 0, 0, 0, utc)) {
		t.Errorf("range next-day: got %v, want next day 09:00", got)
	}
}

func TestScheduleNextEndOfMonth(t *testing.T) {
	// The 31st skips months without one (Feb, Apr, Jun, Sep, Nov).
	s := mustParse(t, "0 0 31 * *")
	after := time.Date(2024, 1, 31, 23, 59, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 3, 31, 0, 0, 0, 0, utc)) {
		t.Errorf("end-of-month next: got %v, want 2024-03-31 00:00", got)
	}
	// Late Dec 31 rolls to the next year.
	after = time.Date(2024, 12, 31, 23, 59, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2025, 1, 31, 0, 0, 0, 0, utc)) {
		t.Errorf("year-roll next: got %v, want 2025-01-31 00:00", got)
	}
}

func TestScheduleNextLeapYearFeb29(t *testing.T) {
	s := mustParse(t, "0 0 29 2 *")
	// The classic boundary: the last minute of Feb 28 in a leap year.
	after := time.Date(2028, 2, 28, 23, 59, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2028, 2, 29, 0, 0, 0, 0, utc)) {
		t.Errorf("Feb 29 next: got %v, want 2028-02-29 00:00", got)
	}
	// From mid-year, the next Feb 29 is ~11 months out (well within the cap).
	after = time.Date(2023, 3, 1, 0, 0, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 2, 29, 0, 0, 0, 0, utc)) {
		t.Errorf("Feb 29 from 2023: got %v, want 2024-02-29 00:00", got)
	}
}

func TestScheduleNextDayOfMonthDayOfWeekOR(t *testing.T) {
	// OR semantics: `0 0 13 * 5` fires on the 13th OR any Friday.
	// Jan 2024: the 5th is a Friday; the 13th is a Saturday.
	s := mustParse(t, "0 0 13 * 5")
	after := time.Date(2024, 1, 1, 0, 0, 0, 0, utc)
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 5, 0, 0, 0, 0, utc)) {
		t.Errorf("OR next (Friday first): got %v, want 2024-01-05 00:00", got)
	}
	// dom-only when dow is `*`.
	s = mustParse(t, "0 0 13 * *")
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 13, 0, 0, 0, 0, utc)) {
		t.Errorf("dom-only next: got %v, want 2024-01-13 00:00", got)
	}
	// dow-only when dom is `*`.
	s = mustParse(t, "0 0 * * 5")
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 5, 0, 0, 0, 0, utc)) {
		t.Errorf("dow-only next: got %v, want 2024-01-05 00:00", got)
	}
	// Sunday via 7.
	s = mustParse(t, "0 0 * * 7")
	after = time.Date(2024, 1, 6, 12, 0, 0, 0, utc) // Saturday
	if got := s.Next(after); !got.Equal(time.Date(2024, 1, 7, 0, 0, 0, 0, utc)) {
		t.Errorf("dow=7 next: got %v, want 2024-01-07 00:00", got)
	}
}

func TestScheduleNextDeterministic(t *testing.T) {
	s := mustParse(t, "*/7 2,14 * * 1,4")
	after := time.Date(2024, 3, 5, 3, 0, 0, 0, utc)
	first := s.Next(after)
	for i := 0; i < 3; i++ {
		if got := s.Next(after); !got.Equal(first) {
			t.Fatalf("Next not deterministic: got %v then %v", first, got)
		}
	}
	if first.IsZero() {
		t.Fatalf("expected a concrete fire time, got zero")
	}
	// The same reference time always produces the same fire time.
	second := s.Next(after.Add(time.Second))
	if !second.Equal(s.Next(after.Add(time.Second))) {
		t.Fatalf("Next not deterministic for sub-minute reference")
	}
}

func TestScheduleNextNonUTCFixedZone(t *testing.T) {
	// DST transitions are not representable in time.FixedZone, so the
	// zone-correctness requirement is pinned with a non-whole-hour fixed zone
	// (UTC+5:45): the returned time must carry the schedule's wall-clock
	// fields in the reference zone.
	zone := time.FixedZone("UTC+0545", 5*3600+45*60)
	s := mustParse(t, "30 14 * * *")
	after := time.Date(2024, 1, 1, 12, 0, 0, 0, zone)
	got := s.Next(after)
	want := time.Date(2024, 1, 1, 14, 30, 0, 0, zone)
	if !got.Equal(want) {
		t.Errorf("non-UTC next: got %v, want %v", got, want)
	}
	if got.Location() != zone {
		t.Errorf("non-UTC next: location %v, want %v", got.Location(), zone)
	}
}

func TestScheduleNextCapGuard(t *testing.T) {
	// Feb 30 never exists: the scan exhausts the 1000-day cap and returns zero.
	s := mustParse(t, "0 0 30 2 *")
	after := time.Date(2024, 1, 1, 0, 0, 0, 0, utc)
	if got := s.Next(after); !got.IsZero() {
		t.Errorf("Feb 30: want zero time (cap exhausted), got %v", got)
	}
	// Feb 29 across a century-style 8-year gap exceeds the 1000-day cap too
	// (documented horizon: 2024-02-29 → next is 2028-02-29, 1461 days).
	s = mustParse(t, "0 0 29 2 *")
	after = time.Date(2024, 2, 29, 0, 0, 0, 0, utc)
	if got := s.Next(after); !got.IsZero() {
		t.Errorf("Feb 29 beyond horizon: want zero time (cap exhausted), got %v", got)
	}
}

func TestScheduleAtAliases(t *testing.T) {
	cases := []struct {
		expr  string
		after time.Time
		want  time.Time
	}{
		{"@hourly", time.Date(2024, 1, 1, 12, 30, 0, 0, utc), time.Date(2024, 1, 1, 13, 0, 0, 0, utc)},
		{"@daily", time.Date(2024, 1, 1, 12, 0, 0, 0, utc), time.Date(2024, 1, 2, 0, 0, 0, 0, utc)},
		{"@midnight", time.Date(2024, 1, 1, 12, 0, 0, 0, utc), time.Date(2024, 1, 2, 0, 0, 0, 0, utc)},
		{"@weekly", time.Date(2024, 1, 1, 12, 0, 0, 0, utc), time.Date(2024, 1, 7, 0, 0, 0, 0, utc)},
	}
	for _, tc := range cases {
		s := mustParse(t, tc.expr)
		if got := s.Next(tc.after); !got.Equal(tc.want) {
			t.Errorf("%s next: got %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
