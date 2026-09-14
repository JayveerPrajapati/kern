package doctor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// SubprojectFreshness holds aggregate index and freshness status for multi-repo roots.
type SubprojectFreshness struct {
	TotalSymbols int
	TotalFiles   int
	FreshRepos   []string
	StaleRepos   []string
	Unindexed    []string
}

// scanSubprojects inspects child repositories under root to aggregate freshness.
func scanSubprojects(root string) (SubprojectFreshness, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	subrepos := intel.DiscoverSubrepos(root)
	var filtered []intel.Repo
	for _, sub := range subrepos {
		absSub, _ := filepath.Abs(sub.Root)
		if absSub != absRoot {
			filtered = append(filtered, sub)
		}
	}
	if len(filtered) == 0 {
		return SubprojectFreshness{}, false
	}

	var sf SubprojectFreshness
	for _, sub := range filtered {
		six, err := index.Load(sub.Root)
		if err == nil && six != nil {
			if six.Stale() {
				sf.StaleRepos = append(sf.StaleRepos, sub.Name)
			} else {
				sf.FreshRepos = append(sf.FreshRepos, sub.Name)
			}
			sf.TotalSymbols += len(six.Symbols)
			sf.TotalFiles += len(six.FileHashes)
		} else {
			sf.Unindexed = append(sf.Unindexed, sub.Name)
		}
	}
	return sf, true
}

// CheckMultiRepoFreshness evaluates aggregate multi-project freshness.
func CheckMultiRepoFreshness(root string) (Finding, bool) {
	sf, ok := scanSubprojects(root)
	if !ok || (len(sf.FreshRepos) == 0 && len(sf.StaleRepos) == 0) {
		return Finding{}, false
	}

	if len(sf.StaleRepos) == 0 {
		detail := fmt.Sprintf("multi-repo index is fresh (%d symbols across %d subprojects: %s)",
			sf.TotalSymbols, len(sf.FreshRepos), strings.Join(sf.FreshRepos, ", "))
		return Finding{Check: "freshness", Level: "ok", Detail: detail}, true
	}

	detail := fmt.Sprintf("multi-repo: %d subprojects fresh, %d STALE (%s) — run `kern index <subproject>`",
		len(sf.FreshRepos), len(sf.StaleRepos), strings.Join(sf.StaleRepos, ", "))
	return Finding{Check: "freshness", Level: "warn", Detail: detail}, true
}

// CheckMultiRepoIndex evaluates aggregate multi-project index existence.
func CheckMultiRepoIndex(root string) (Finding, bool) {
	sf, ok := scanSubprojects(root)
	if !ok || (len(sf.FreshRepos) == 0 && len(sf.StaleRepos) == 0) {
		return Finding{}, false
	}
	indexedCount := len(sf.FreshRepos) + len(sf.StaleRepos)
	allIndexed := append(append([]string(nil), sf.FreshRepos...), sf.StaleRepos...)
	detail := fmt.Sprintf("multi-repo index: %d symbols across %d indexed subprojects (%s)",
		sf.TotalSymbols, indexedCount, strings.Join(allIndexed, ", "))
	return Finding{Check: "index", Level: "ok", Detail: detail}, true
}
