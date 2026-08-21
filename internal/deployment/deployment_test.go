package deployment

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestNoopDeployerReturnsSimulated verifies the default deployer reports a
// simulated success and never touches production.
func TestNoopDeployerReturnsSimulated(t *testing.T) {
	d := NoopDeployer{}
	res, err := d.Deploy(context.Background(), DeployRequest{Service: "svc", Version: "v1"})
	if err != nil {
		t.Fatalf("NoopDeployer.Deploy: %v", err)
	}
	if !res.Success {
		t.Fatal("NoopDeployer must report success")
	}
	if !strings.Contains(res.Output, "simulated") {
		t.Fatalf("expected simulated output, got %q", res.Output)
	}
}

// TestShellDeployerRefusedWithoutAllowDeploy verifies the fail-closed gate: a
// ShellDeployer must refuse to run unless KERN_ALLOW_DEPLOY=1.
func TestShellDeployerRefusedWithoutAllowDeploy(t *testing.T) {
	t.Setenv("KERN_ALLOW_DEPLOY", "")
	t.Setenv("KERN_DEPLOY_COMMAND", "echo should-not-run")

	d := &ShellDeployer{Command: "echo should-not-run"}
	_, err := d.Deploy(context.Background(), DeployRequest{Service: "svc", Version: "v1"})
	if err == nil {
		t.Fatal("expected refusal without KERN_ALLOW_DEPLOY=1")
	}
	if !strings.Contains(err.Error(), "KERN_ALLOW_DEPLOY") {
		t.Fatalf("expected gate error mentioning KERN_ALLOW_DEPLOY, got %v", err)
	}
}

// TestShellDeployerRunsCommand verifies a real command runs and receives the
// deployment context as env vars when KERN_ALLOW_DEPLOY=1.
func TestShellDeployerRunsCommand(t *testing.T) {
	t.Setenv("KERN_ALLOW_DEPLOY", "1")
	t.Setenv("KERN_DEPLOY_COMMAND", "echo deploying $KERN_SERVICE")

	d := &ShellDeployer{Command: "echo deploying $KERN_SERVICE", Timeout: 10 * time.Second}
	res, err := d.Deploy(context.Background(), DeployRequest{Service: "checkout", Version: "v1"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got failure: %q", res.Output)
	}
	if !strings.Contains(res.Output, "deploying checkout") {
		t.Fatalf("expected output to contain env-expanded service, got %q", res.Output)
	}
}

// TestShellDeployerFailure verifies a non-zero exit yields Success=false.
func TestShellDeployerFailure(t *testing.T) {
	t.Setenv("KERN_ALLOW_DEPLOY", "1")
	t.Setenv("KERN_DEPLOY_COMMAND", "exit 1")

	d := &ShellDeployer{Command: "exit 1", Timeout: 10 * time.Second}
	res, err := d.Deploy(context.Background(), DeployRequest{Service: "svc", Version: "v1"})
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if res.Success {
		t.Fatal("expected Success=false on non-zero exit")
	}
}

// TestNewDeployerFromEnv verifies env-driven resolution: unset command yields a
// NoopDeployer, set command yields a ShellDeployer.
func TestNewDeployerFromEnv(t *testing.T) {
	t.Setenv("KERN_ALLOW_DEPLOY", "1")

	t.Setenv("KERN_DEPLOY_COMMAND", "")
	if d := NewDeployerFromEnv(); !isNoop(d) {
		t.Fatalf("expected NoopDeployer when KERN_DEPLOY_COMMAND unset, got %T", d)
	}

	t.Setenv("KERN_DEPLOY_COMMAND", "kubectl rollout status")
	if d := NewDeployerFromEnv(); !isShell(d) {
		t.Fatalf("expected ShellDeployer when KERN_DEPLOY_COMMAND set, got %T", d)
	}
}

func isNoop(d Deployer) bool {
	_, ok := d.(NoopDeployer)
	return ok
}

func isShell(d Deployer) bool {
	_, ok := d.(*ShellDeployer)
	return ok
}