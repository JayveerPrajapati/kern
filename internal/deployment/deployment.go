// Package deployment provides a vendor-agnostic deployer abstraction. It lets
// the loop and TaskService perform a real deployment by shelling out to an
// external, operator-configured command (kubectl rollout, helm upgrade, docker
// compose up, make deploy, ...). It never touches the working tree or VCS:
// deployment is an external system operation gated by KERN_ALLOW_DEPLOY=1.
package deployment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// maxOutputBytes caps captured command output (8 KiB) so a chatty deploy tool
// cannot blow up a task/loop result.
const maxOutputBytes = 8 << 10

// defaultTimeout is the maximum a deploy command may run when none is set.
const defaultTimeout = 5 * time.Minute

// Deployer performs a deployment for a service. Implementations must be
// fail-closed: a deployment must never proceed without explicit opt-in.
type Deployer interface {
	Deploy(ctx context.Context, req DeployRequest) (DeployResult, error)
}

// DeployRequest describes what to deploy.
type DeployRequest struct {
	Service     string
	Version     string
	Image       string
	Commit      string
	ProjectRoot string
	Environment map[string]string
}

// DeployResult is the outcome of a deployment attempt.
type DeployResult struct {
	Service     string
	Version     string
	DeploymentID string
	Output      string
	StartedAt   time.Time
	CompletedAt time.Time
	Success     bool
	Rollback    bool
}

// NoopDeployer is the default deployer used when no KERN_DEPLOY_COMMAND is
// configured. It reports success without doing anything, preserving the v1
// simulated-deployment behavior when nothing is wired.
type NoopDeployer struct{}

// Deploy returns a simulated success. It never touches production.
func (NoopDeployer) Deploy(ctx context.Context, req DeployRequest) (DeployResult, error) {
	return DeployResult{
		Service:     req.Service,
		Version:     req.Version,
		Output:      "deploy skipped: no deployer configured (simulated)",
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
		Success:     true,
	}, nil
}

// ShellDeployer runs a configurable shell command (kubectl rollout, helm
// upgrade, docker compose up, make deploy, ...) to perform the deployment. The
// command runs via exec.CommandContext with a timeout and receives the
// deployment context as environment variables:
//
//	KERN_SERVICE, KERN_VERSION, KERN_IMAGE, KERN_COMMIT, KERN_PROJECT_ROOT
//
// It refuses to run unless KERN_ALLOW_DEPLOY=1 (fail-closed, the same gate the
// loop enforces). A non-zero exit yields Success=false; the combined
// stdout+stderr (capped) is returned as Output. The command is an external
// operation: ShellDeployer never modifies the working tree or VCS.
type ShellDeployer struct {
	Command string
	Timeout time.Duration
}

// Deploy runs the configured command. It fails closed unless KERN_ALLOW_DEPLOY
// is "1".
func (d *ShellDeployer) Deploy(ctx context.Context, req DeployRequest) (DeployResult, error) {
	res := DeployResult{
		Service:   req.Service,
		Version:   req.Version,
		StartedAt: time.Now(),
	}
	defer func() { res.CompletedAt = time.Now() }()

	if os.Getenv("KERN_ALLOW_DEPLOY") != "1" {
		return res, errors.New("deployment refused: KERN_ALLOW_DEPLOY not set")
	}
	if strings.TrimSpace(d.Command) == "" {
		return res, errors.New("deployment refused: empty deploy command")
	}

	timeout := d.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", d.Command)
	cmd.Env = append(os.Environ(),
		"KERN_SERVICE="+req.Service,
		"KERN_VERSION="+req.Version,
		"KERN_IMAGE="+req.Image,
		"KERN_COMMIT="+req.Commit,
		"KERN_PROJECT_ROOT="+req.ProjectRoot,
	)
	for k, v := range req.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		res.Success = false
		res.Output = capOutput(stdout.String() + stderr.String())
		if ctx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("deployment timed out after %s: %w", timeout, err)
		}
		return res, fmt.Errorf("deployment failed: %w", err)
	}

	res.Success = true
	res.Output = capOutput(stdout.String())
	res.DeploymentID = req.Version
	return res, nil
}

// NewDeployerFromEnv resolves a deployer from the environment. If
// KERN_DEPLOY_COMMAND is set it returns a ShellDeployer (with an optional
// KERN_DEPLOY_TIMEOUT in seconds); otherwise it returns the NoopDeployer so v1
// behavior is preserved when nothing is wired.
func NewDeployerFromEnv() Deployer {
	cmd := os.Getenv("KERN_DEPLOY_COMMAND")
	if strings.TrimSpace(cmd) == "" {
		return NoopDeployer{}
	}
	sd := &ShellDeployer{Command: cmd, Timeout: defaultTimeout}
	if v := os.Getenv("KERN_DEPLOY_TIMEOUT"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			sd.Timeout = time.Duration(secs) * time.Second
		}
	}
	return sd
}

// capOutput truncates output to maxOutputBytes.
func capOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes]
}