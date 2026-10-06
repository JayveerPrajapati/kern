package verifycmd

import (
	"strings"
	"testing"
)

func TestClassifyDeniesProgramRunningFlags(t *testing.T) {
	goTC := map[string]bool{"go": true}
	gradleTC := map[string]bool{"gradle": true}

	denied := []struct {
		cmd string
		tc  map[string]bool
	}{
		{"go test -exec /tmp/evil ./...", goTC},
		{"go test --exec=/tmp/evil ./...", goTC},
		{"go test -exec=/tmp/evil ./...", goTC},
		{"go build -toolexec /tmp/evil ./...", goTC},
		{"go vet -vettool=/tmp/evil ./...", goTC},
		{"gradle --init-script /tmp/x.gradle build", gradleTC},
		{"./gradlew -I /tmp/x.gradle build", gradleTC},
	}
	for _, c := range denied {
		err := classifyCommand(c.cmd, c.tc)
		if err == nil || !strings.Contains(err.Error(), "caller-chosen program") {
			t.Errorf("%q must be refused as program-running, got %v", c.cmd, err)
		}
	}

	allowed := []struct {
		cmd string
		tc  map[string]bool
	}{
		{"go test ./...", goTC},
		{"go test -run 'TestExec|TestToolexec' ./internal/x", goTC},
		{"go test -count=1 -race ./...", goTC},
		{"go vet ./...", goTC},
		{"go build -o bin/x ./cmd/x", goTC},
		{"gradle build", gradleTC},
		{"./gradlew test --info", gradleTC},
	}
	for _, c := range allowed {
		if err := classifyCommand(c.cmd, c.tc); err != nil {
			t.Errorf("%q must stay allowed, got %v", c.cmd, err)
		}
	}
}

func TestFlagName(t *testing.T) {
	for in, want := range map[string]string{
		"-exec":         "exec",
		"--exec":        "exec",
		"-exec=/bin/sh": "exec",
		"--init-script": "init-script",
		"-I":            "I",
		"./...":         "",
		"TestExec":      "",
		"":              "",
	} {
		if got := flagName(in); got != want {
			t.Errorf("flagName(%q) = %q, want %q", in, got, want)
		}
	}
}
