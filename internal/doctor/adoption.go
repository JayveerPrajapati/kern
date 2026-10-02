package doctor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// checkAdoption surfaces the kern-first adoption metric: how many
// shadow-tool calls the opencode plugin routed through kern versus raw
// fallbacks, aggregated from <root>/.kern/adoption.log (written by the
// plugin when the operator sets KERN_ADOPTION_LOG=1). The log is opt-in,
// so its absence is ok — the detail explains how to enable it.
func checkAdoption(root string) Finding {
	path := filepath.Join(root, ".kern", "adoption.log")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Finding{Check: "adoption", Level: "ok", Detail: "no adoption log — set KERN_ADOPTION_LOG=1 in the opencode host env to track kern-first routing"}
	}
	counts, err := metrics.AggregateAdoptionLog(path)
	if err != nil {
		return Finding{Check: "adoption", Level: "warn", Detail: err.Error()}
	}
	return Finding{Check: "adoption", Level: "ok", Detail: fmt.Sprintf("%d/%d shadow-tool calls kern-routed (%.1f%%)", counts.Routed, counts.Total, counts.Pct())}
}
