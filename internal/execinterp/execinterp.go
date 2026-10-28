// Package execinterp holds the best-effort interpreter/eval and encoded-pipe
// heuristics for the exec firewall. They raise the bar against the common
// wrapper bypasses (python -c subprocess, perl -e unlink, ruby -e File.delete,
// node -e fs.rmSync, eval, base64 -d | sh); obfuscated code can still evade
// them. The package is separate from execclassify to keep that package under
// its ARCHITECTURE.md LOC cap.
package execinterp

import "strings"

// ClassifyInterpreter reports whether an interpreter/eval invocation runs
// destructive code.
func ClassifyInterpreter(name, seg string, tokens []string) (bool, string) {
	lname := strings.ToLower(name)
	code := ""
	for i := 1; i < len(tokens); i++ {
		switch strings.ToLower(tokens[i]) {
		case "-c", "--command", "-e", "-ec", "-eval":
			code = strings.Join(tokens[i+1:], " ")
		}
	}
	if code == "" {
		return false, ""
	}
	has := func(patterns ...string) string {
		for _, p := range patterns {
			if strings.Contains(code, p) {
				return p
			}
		}
		return ""
	}
	switch lname {
	case "python", "python2", "python3", "py":
		if p := has("subprocess.", "os.system(", "os.remove(", "os.unlink(", "os.rmdir(", "shutil.rmtree(", ".unlink(", ".rmdir("); p != "" {
			return true, name + " -c executes destructive code (" + p + ")"
		}
		if strings.Contains(code, "open(") && has("'w'", `"w"`, "'a'", `"a"`, "'wb'", `"wb"`) != "" {
			return true, name + " -c opens a file for writing"
		}
	case "perl":
		if p := has("unlink", "system(", "exec(", "`"); p != "" {
			return true, name + " -e executes destructive code (" + p + ")"
		}
	case "ruby":
		if p := has("File.delete", "File.unlink", "system(", "`"); p != "" {
			return true, name + " -e executes destructive code (" + p + ")"
		}
	case "node", "nodejs":
		if p := has("rmSync(", "unlinkSync(", "rmdirSync(", "child_process", "execSync", "spawn("); p != "" {
			return true, name + " -e executes destructive code (" + p + ")"
		}
	}
	return false, ""
}

// ClassifyEncodedPipe reports a pipeline that decodes an encoded payload and
// pipes it into an interpreter (base64 -d | sh, xxd -r | bash, ...).
func ClassifyEncodedPipe(stages []string) (bool, string) {
	head := func(s string) string {
		f := strings.Fields(strings.TrimSpace(s))
		if len(f) == 0 {
			return ""
		}
		return strings.ToLower(f[0])
	}
	for i, s := range stages {
		h := head(s)
		if h != "base64" && h != "base32" && h != "xxd" {
			continue
		}
		decodes := false
		for _, f := range strings.Fields(s)[1:] {
			if f == "-d" || f == "--decode" || f == "-r" || f == "-D" {
				decodes = true
			}
		}
		if !decodes {
			continue
		}
		for j := i + 1; j < len(stages); j++ {
			switch head(stages[j]) {
			case "sh", "bash", "zsh", "dash", "ksh", "python", "python3", "perl", "ruby", "node":
				return true, h + " decodes a payload | " + head(stages[j]) + " runs hidden code"
			}
		}
	}
	return false, ""
}
