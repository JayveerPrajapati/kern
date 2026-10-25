// Package guardstats turns the guard's audit sidecar
// (~/.kern/audit/bypass.jsonl) into the doctor telemetry: the deny/serve
// ratio, the top denied command shapes, and the Ask-Protocol usage. The
// burden the guard places on agents must be measurable — P3 of the
// guard-consent campaign (2026-10-24): a rising deny/serve ratio or a
// persistent top denied shape is the next false-positive class to fix.
package guardstats

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ShapeCount is one aggregated command shape (first two tokens of the
// denied subject) with its count.
type ShapeCount struct {
	Shape string
	Count int
}

// Report is the aggregated guard telemetry for a time window.
type Report struct {
	WindowDays   int
	Denies       int
	Serves       int
	GrantUses    int
	Bypasses     int
	PathAllows   int
	TopDenied    []ShapeCount
	ScanErr      string
	RecordsTotal int
}

type auditRecord struct {
	TS      string `json:"ts"`
	Event   string `json:"event"`
	Tool    string `json:"tool"`
	Subject string `json:"subject"`
}

// AuditPath returns the guard audit sidecar path for home.
func AuditPath(home string) string {
	return filepath.Join(home, ".kern", "audit", "bypass.jsonl")
}

// Collect scans the audit sidecar and aggregates the guard events inside
// the window (records with unparseable timestamps are counted but not
// aggregated — never a hard error; telemetry is advisory).
func Collect(home string, windowDays int) Report {
	rep := Report{WindowDays: windowDays}
	f, err := os.Open(AuditPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return rep
		}
		rep.ScanErr = err.Error()
		return rep
	}
	defer f.Close()
	cutoff := time.Now().UTC().AddDate(0, 0, -windowDays)
	shapes := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		rep.RecordsTotal++
		var r auditRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil || r.Event == "" {
			continue
		}
		ts, terr := time.Parse(time.RFC3339, strings.TrimSpace(r.TS))
		if terr != nil || ts.Before(cutoff) {
			continue
		}
		switch r.Event {
		case "kern-guard-deny":
			rep.Denies++
			if s := shape(r.Subject); s != "" {
				shapes[s]++
			}
		case "kern-guard-redirect":
			rep.Serves++
		case "kern-guard-gate-grant-use":
			rep.GrantUses++
		case "kern-guard-bypass":
			rep.Bypasses++
		case "kern-guard-path-write-allow":
			rep.PathAllows++
		}
	}
	if serr := sc.Err(); serr != nil {
		rep.ScanErr = serr.Error()
	}
	for s, c := range shapes {
		rep.TopDenied = append(rep.TopDenied, ShapeCount{Shape: s, Count: c})
	}
	sort.Slice(rep.TopDenied, func(i, j int) bool {
		if rep.TopDenied[i].Count != rep.TopDenied[j].Count {
			return rep.TopDenied[i].Count > rep.TopDenied[j].Count
		}
		return rep.TopDenied[i].Shape < rep.TopDenied[j].Shape
	})
	if len(rep.TopDenied) > 10 {
		rep.TopDenied = rep.TopDenied[:10]
	}
	return rep
}

// Diagnostic is the doctor-shaped result (mirrors guardpause.Diagnostic —
// deliberately independent of internal/doctor so the owning package
// renders its own telemetry and doctor just displays it).
type Diagnostic struct {
	Check  string `json:"check"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
}

// Check renders the 30-day telemetry as one doctor finding: the deny/serve
// ratio (above 1:1 is a warn — the guard should serve more than it
// denies), the top denied shapes (the next false-positive class to fix),
// and the ask-protocol grant uses. It resolves the home directory itself
// and fails soft (an ok finding) when there is none.
func Check() Diagnostic {
	home, err := os.UserHomeDir()
	if err != nil {
		return Diagnostic{Check: "guard-telemetry", Level: "ok", Detail: "no home dir — guard telemetry not checked"}
	}
	rep := Collect(home, 30)
	if rep.Denies+rep.Serves == 0 {
		return Diagnostic{Check: "guard-telemetry", Level: "ok", Detail: "no guard events in the last 30d"}
	}
	level := "ok"
	if rep.Denies > rep.Serves {
		level = "warn"
	}
	detail := "30d: " + itoa(rep.Denies) + " denies / " + itoa(rep.Serves) + " serves"
	if len(rep.TopDenied) > 0 {
		top := make([]string, 0, 3)
		for i, s := range rep.TopDenied {
			if i >= 3 {
				break
			}
			top = append(top, s.Shape+" x"+itoa(s.Count))
		}
		detail += " | top denied: " + strings.Join(top, ", ")
	}
	detail += " | grant uses: " + itoa(rep.GrantUses)
	return Diagnostic{Check: "guard-telemetry", Level: level, Detail: detail}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// shape reduces a denied command to its first two tokens (env-assignment
// prefixes and sudo are skipped): the shape of a denial is what a fix
// targets, not the exact subject text.
func shape(subject string) string {
	fields := strings.Fields(strings.TrimSpace(subject))
	out := make([]string, 0, 2)
	for _, f := range fields {
		if f == "sudo" || strings.Contains(f, "=") {
			continue
		}
		out = append(out, strings.TrimSuffix(f, ";"))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " ")
}
