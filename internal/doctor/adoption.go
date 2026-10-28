package doctor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// checkAdoption surfaces the kern-first adoption metric (<root>/.kern/adoption.log; on by default, KERN_ADOPTION_LOG=0 disables; 20 calls noise floor, 60% target).
func checkAdoption(root string) Finding {
	path := filepath.Join(root, ".kern", "adoption.log")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Finding{Check: "adoption", Level: "ok", Detail: "no adoption log yet — the plugin records one per shadow call (KERN_ADOPTION_LOG=0 disables)"}
	}
	counts, err := metrics.AggregateAdoptionLog(path)
	if err != nil {
		return Finding{Check: "adoption", Level: "warn", Detail: err.Error()}
	}
	d := fmt.Sprintf("%d/%d shadow-tool calls kern-routed (%.1f%%)", counts.Routed, counts.Total, counts.Pct())
	if counts.Total >= 20 && counts.Pct() < 60 {
		return Finding{Check: "adoption", Level: "warn", Detail: d + " — below the 60% kern-first target"}
	}
	return Finding{Check: "adoption", Level: "ok", Detail: d}
}
