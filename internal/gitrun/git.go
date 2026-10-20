// Package gitrun provides the minimal git invocation helper shared by the
// verification and verifycmd surfaces. Extracted from the blueprint family
// (bpcli/mcp.GitOutput) during the pivot: the pre-commit/verify paths need
// raw `git` output without the blueprint service layer.
package gitrun

import "os/exec"

// Output runs `git <args...>` in dir and returns stdout. Any git error
// (including a non-zero exit) is returned as-is; callers decide whether a
// failure is fatal or a signal to fall back.
func Output(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
