package loop

// Cron scheduling for the closed loop (Feature Batch I: kern loop --schedule).
//
// This file implements a minimal, deterministic, stdlib-only cron parser and
// next-fire-time computation. It adds NO imports beyond the standard library
// (errors, fmt, sort, strconv, strings, time), so internal/loop's
// allowed-dependency set is unchanged.
//
// Supported syntax — standard 5-field cron:
//
//	field         allowed values
//	------        --------------
//	minute        0-59
//	hour          0-23
//	day-of-month  1-31
//	month         1-12
//	day-of-week   0-6 (0 = Sunday; 7 is also accepted as Sunday)
//
// Per-field forms (all combinable with commas):
//
//	*        every value
//	*/N      every N-th value (N >= 1)
//	N        a single value
//	N-M      an inclusive range (N <= M)
//	N,M      a list of the above
//
// Convenience aliases (whole-expression only):
//
//	@hourly   → 0 * * * *
//	@daily    → 0 0 * * *
//	@midnight → 0 0 * * *   (alias of @daily)
//	@weekly   → 0 0 * * 0
//
// NOT supported (documented):
//
//	- no seconds field (6-field expressions are rejected)
//	- no named months/days (JAN, MON) — numeric only
//	- no @reboot/@yearly/@annually/@monthly or other @-keywords
//	- no Quartz-style extensions (? L W # C, ranges with steps like N-M/S)
//	- no timezone field; Next runs in the zone of the reference time
//
// Day-of-month / day-of-week semantics: OR (the common Vixie-cron behavior).
// When BOTH day-of-month and day-of-week are restricted (neither is `*`), a
// date matches if EITHER field matches. When only one is restricted, that
// field alone determines the date (the unrestricted field matches every
// date). This is the widely-deployed default and the one implemented here.
//
// Next() is deterministic and bounded: it scans candidate days forward from
// the day of the reference time, at most maxCronScanCandidates (1000) of
// them. A schedule that cannot fire within that horizon — never-firing
// combos (e.g. Feb 30) or sparse combos beyond the horizon (e.g. Feb 29
// across a century boundary) — returns the zero time.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxCronScanCandidates caps the Next() day scan so an unsatisfiable or
// pathologically sparse schedule fails loudly (zero time) instead of hanging.
const maxCronScanCandidates = 1000

// Schedule is a parsed 5-field cron expression.
type Schedule struct {
	expr          string // original expression, for diagnostics
	minute        []int  // allowed minutes (0-59), ascending, deduped
	hour          []int  // allowed hours (0-23), ascending, deduped
	dayOfMonth    []int  // allowed days of month (1-31), ascending, deduped
	month         []int  // allowed months (1-12), ascending, deduped
	dayOfWeek     []int  // allowed days of week (0-6, 0=Sunday), ascending, deduped
	domRestricted bool   // day-of-month field was not `*`
	dowRestricted bool   // day-of-week field was not `*`
}

// ParseSchedule parses a 5-field cron expression (or one of the supported
// @-aliases) into a Schedule. Malformed expressions return an error naming
// the offending field.
func ParseSchedule(expr string) (*Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, errors.New("cron: empty expression")
	}
	switch expr {
	case "@hourly":
		expr = "0 * * * *"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@weekly":
		expr = "0 0 * * 0"
	default:
		if strings.HasPrefix(expr, "@") {
			return nil, fmt.Errorf("cron: unsupported @-keyword %q (supported: @hourly, @daily, @midnight, @weekly)", expr)
		}
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields (minute hour day-of-month month day-of-week), got %d in %q", len(fields), expr)
	}
	s := &Schedule{expr: expr}
	var err error
	if s.minute, err = parseField(fields[0], 0, 59, "minute"); err != nil {
		return nil, err
	}
	if s.hour, err = parseField(fields[1], 0, 23, "hour"); err != nil {
		return nil, err
	}
	if s.dayOfMonth, s.domRestricted, err = parseDateField(fields[2], 1, 31, "day-of-month"); err != nil {
		return nil, err
	}
	if s.month, err = parseField(fields[3], 1, 12, "month"); err != nil {
		return nil, err
	}
	if s.dayOfWeek, s.dowRestricted, err = parseDateField(fields[4], 0, 7, "day-of-week"); err != nil {
		return nil, err
	}
	s.dayOfWeek = normalizeDow(s.dayOfWeek)
	return s, nil
}

// parseField parses one cron field into the sorted, deduped set of allowed
// values within [min, max]. `*` expands to the full range.
func parseField(spec string, min, max int, name string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	add := func(v int) error {
		if v < min || v > max {
			return fmt.Errorf("cron: %s value %d out of range %d-%d", name, v, min, max)
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
		return nil
	}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		switch {
		case item == "":
			return nil, fmt.Errorf("cron: empty element in %s field %q", name, spec)
		case item == "*":
			for v := min; v <= max; v++ {
				if err := add(v); err != nil {
					return nil, err
				}
			}
		case strings.HasPrefix(item, "*/"):
			step, err := strconv.Atoi(strings.TrimPrefix(item, "*/"))
			if err != nil || step < 1 {
				return nil, fmt.Errorf("cron: invalid step %q in %s field (want */N with N >= 1)", item, name)
			}
			for v := min; v <= max; v += step {
				if err := add(v); err != nil {
					return nil, err
				}
			}
		case strings.Contains(item, "-"):
			lo, hi, err := parseRange(item, name)
			if err != nil {
				return nil, err
			}
			for v := lo; v <= hi; v++ {
				if err := add(v); err != nil {
					return nil, err
				}
			}
		default:
			v, err := strconv.Atoi(item)
			if err != nil {
				return nil, fmt.Errorf("cron: invalid value %q in %s field (want *, */N, N, N-M or a comma list of these)", item, name)
			}
			if err := add(v); err != nil {
				return nil, err
			}
		}
	}
	sort.Ints(out)
	return out, nil
}

// parseDateField is parseField for day-of-month / day-of-week: it also reports
// whether the field was restricted (not `*`), which drives the OR semantics in
// dayMatches.
func parseDateField(spec string, min, max int, name string) ([]int, bool, error) {
	vals, err := parseField(spec, min, max, name)
	if err != nil {
		return nil, false, err
	}
	return vals, strings.TrimSpace(spec) != "*", nil
}

// parseRange parses the inclusive N-M range form.
func parseRange(item, name string) (lo, hi int, err error) {
	dash := strings.IndexByte(item, '-')
	lo, err1 := strconv.Atoi(item[:dash])
	hi, err2 := strconv.Atoi(item[dash+1:])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("cron: invalid range %q in %s field", item, name)
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("cron: invalid range %q in %s field (start %d > end %d)", item, name, lo, hi)
	}
	return lo, hi, nil
}

// normalizeDow maps Sunday-as-7 to 0, dedupes and sorts (7 and 0 are the same
// weekday, so `0,7` collapses to `0`).
func normalizeDow(vals []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range vals {
		if v == 7 {
			v = 0
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// Next returns the deterministic next fire time strictly after `after` (always
// a whole-minute boundary), or the zero time when the schedule cannot fire
// within maxCronScanCandidates days.
func (s *Schedule) Next(after time.Time) time.Time {
	// The smallest whole minute strictly after `after`: Next only ever returns
	// whole-minute times, so scanning from here enforces the "strictly after"
	// contract exactly once — including for sub-minute reference times and for
	// an `after` that already sits on a fire minute.
	after = after.Truncate(time.Minute).Add(time.Minute)
	loc := after.Location()
	day := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < maxCronScanCandidates; i++ {
		if s.dayMatches(day) {
			if t, ok := s.firstMatchAfter(day, after); ok {
				return t
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}
}

// dayMatches reports whether any time on the given calendar day matches the
// schedule's month / day-of-month / day-of-week fields. When BOTH day-of-month
// and day-of-week are restricted, either field matching suffices (OR); when
// only one is restricted, that field alone decides.
func (s *Schedule) dayMatches(day time.Time) bool {
	if !contains(s.month, int(day.Month())) {
		return false
	}
	switch {
	case s.domRestricted && s.dowRestricted:
		// Both restricted: standard cron OR — either field matching suffices.
		return contains(s.dayOfMonth, day.Day()) || contains(s.dayOfWeek, int(day.Weekday()))
	case s.domRestricted:
		return contains(s.dayOfMonth, day.Day())
	case s.dowRestricted:
		return contains(s.dayOfWeek, int(day.Weekday()))
	default:
		return true
	}
}

// firstMatchAfter returns the earliest whole-minute time on `day` that matches
// the schedule's minute/hour fields and is >= after. It assumes dayMatches(day)
// already holds. hour and minute are non-empty ascending lists, so the earliest
// match is the first allowed hour with any allowed minute at/after the
// reference time-of-day.
func (s *Schedule) firstMatchAfter(day, after time.Time) (time.Time, bool) {
	if day.After(after) {
		// The whole day lies ahead of the reference time: the earliest match
		// is the smallest (hour, minute) combination.
		return time.Date(day.Year(), day.Month(), day.Day(), s.hour[0], s.minute[0], 0, 0, day.Location()), true
	}
	ah, am := after.Hour(), after.Minute()
	for _, h := range s.hour {
		if h < ah {
			continue
		}
		for _, m := range s.minute {
			if h == ah && m < am {
				continue
			}
			return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, day.Location()), true
		}
	}
	return time.Time{}, false
}

// contains reports whether vals contains v.
func contains(vals []int, v int) bool {
	for _, x := range vals {
		if x == v {
			return true
		}
	}
	return false
}
