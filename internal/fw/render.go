package fw

import (
	"fmt"
	"strings"
)

// Render renders the detected frameworks as a compact human-readable report.
func Render(d []Detected) string {
	var b strings.Builder
	if len(d) == 0 {
		b.WriteString("No known frameworks detected.\n")
		return b.String()
	}
	b.WriteString("Detected frameworks:\n")
	curLang := ""
	for _, det := range d {
		if det.Lang != curLang {
			curLang = det.Lang
			fmt.Fprintf(&b, "\n%s\n", curLang)
		}
		fmt.Fprintf(&b, "  %-18s %s\n", det.Name, strings.Join(det.Signals, ", "))
		if det.Summary != "" {
			fmt.Fprintf(&b, "      %s\n", det.Summary)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
