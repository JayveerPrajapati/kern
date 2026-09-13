package cli

import (
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/sandbox"
)

// sandboxOptsFromConfig maps the sandbox section of .blueprint/config.yaml to
// sandbox.ConfigOptions (execution timeout + polyglot matrix). `kern check`,
// `kern ci`, and `kern diff-gate` all build their tests:build-test check from
// this one helper so the three commands cannot drift apart again: the
// timeout-wiring bug (27e4559) was exactly this drift — `kern check` wired
// only the matrix while `kern ci` wired timeout + matrix, so check silently
// ran DefaultConfig's 120s timeout and reported a spurious ERROR on every
// healthy run of a repo whose test matrix needs longer.
func sandboxOptsFromConfig(file policy.ConfigFile) []sandbox.ConfigOption {
	var opts []sandbox.ConfigOption
	if file.Sandbox.TimeoutSeconds > 0 {
		opts = append(opts, sandbox.WithTimeout(time.Duration(file.Sandbox.TimeoutSeconds)*time.Second))
	}
	if len(file.Sandbox.Matrix) > 0 {
		matrix := make([]sandbox.MatrixTarget, 0, len(file.Sandbox.Matrix))
		for _, m := range file.Sandbox.Matrix {
			target := sandbox.MatrixTarget{
				Name: m.Name,
				Dir:  m.Dir,
			}
			if m.Build != "" {
				target.Build = sandbox.SplitCommand(m.Build)
			}
			if m.Test != "" {
				target.Test = sandbox.SplitCommand(m.Test)
			}
			if m.Command != "" {
				target.Command = sandbox.SplitCommand(m.Command)
			}
			matrix = append(matrix, target)
		}
		opts = append(opts, sandbox.WithMatrix(matrix))
	}
	return opts
}
