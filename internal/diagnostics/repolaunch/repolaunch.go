package repolaunch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result is the doctor-shaped diagnostic result without importing doctor.
type Result struct {
	Check  string
	Level  string
	Detail string
}

// checkRepoLauncher diagnoses the source-checkout convenience launcher at
// root/kern. The launcher must be an executable delegating shim, not a copied
// binary: copied binaries survive `make install` unchanged and make agents test
// stale behavior. Version parity proves the delegation without depending on the
// shell implementation or build stamp format.
func Check(root string) Result {
	const check = "repo-launcher"
	if !isKernCheckout(root) {
		return Result{Check: check, Level: "ok", Detail: "not a kern checkout — repo launcher not checked"}
	}
	launcher := filepath.Join(root, "kern")
	target := filepath.Join(root, "bin", "kern")
	info, err := os.Stat(launcher)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{Check: check, Level: "warn", Detail: "repo launcher ./kern is missing — restore it with: git checkout -- kern"}
		}
		return Result{Check: check, Level: "fail", Detail: "repo launcher ./kern: " + err.Error()}
	}
	if info.IsDir() {
		return Result{Check: check, Level: "fail", Detail: "repo launcher ./kern is a directory"}
	}
	if info.Mode().Perm()&0o111 == 0 {
		return Result{Check: check, Level: "fail", Detail: "repo launcher ./kern is not executable — run: chmod +x ./kern"}
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{Check: check, Level: "warn", Detail: "repo launcher ./kern present, but bin/kern is not built — run: make build"}
		}
		return Result{Check: check, Level: "fail", Detail: "repo target bin/kern: " + err.Error()}
	}
	if targetInfo.IsDir() || targetInfo.Mode().Perm()&0o111 == 0 {
		return Result{Check: check, Level: "fail", Detail: "repo target bin/kern is not an executable file"}
	}
	launcherVersion, lerr := commandVersion(launcher)
	targetVersion, terr := commandVersion(target)
	if lerr != nil {
		return Result{Check: check, Level: "fail", Detail: "repo launcher ./kern version failed: " + lerr.Error()}
	}
	if terr != nil {
		return Result{Check: check, Level: "fail", Detail: "repo target bin/kern version failed: " + terr.Error()}
	}
	if launcherVersion != targetVersion {
		return Result{Check: check, Level: "fail", Detail: fmt.Sprintf("repo launcher ./kern is stale (%s; bin/kern is %s) — restore the shim with: git checkout -- kern", launcherVersion, targetVersion)}
	}
	return Result{Check: check, Level: "ok", Detail: "repo launcher ./kern delegates to bin/kern " + targetVersion}
}

// commandVersion runs a kern binary's version command and returns its first
// line. It is deliberately tiny: doctor must not depend on the full command
// output format, only on exact equality between two invocations.
func commandVersion(bin string) (string, error) {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return line, nil
}

const kernModulePath = "github.com/JayveerPrajapati/kern"

// isKernCheckout reports whether root is a kern source checkout.
func isKernCheckout(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest) == kernModulePath
		}
	}
	return false
}
