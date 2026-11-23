package guardhook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/guardtelemetry"
)

func writeMarker(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectManagedExplicitMarker(t *testing.T) {
	root := t.TempDir()
	if projectManaged(root) {
		t.Fatal("empty root must not be managed")
	}
	writeMarker(t, root, `{"schema_version":1,"managed":true,"guard":{"enabled":true}}`)
	if !projectManaged(root) {
		t.Fatal("explicit marker must manage root")
	}
	writeMarker(t, root, `{"schema_version":1,"managed":false,"guard":{"enabled":true}}`)
	if projectManaged(root) {
		t.Fatal("managed=false must disable guard")
	}
	writeMarker(t, root, `{"schema_version":1,"managed":true,"guard":{"enabled":false}}`)
	if projectManaged(root) {
		t.Fatal("guard.enabled=false must disable guard")
	}
}

func TestProjectManagedLegacyAndAncestors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "index.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if !projectManaged(child) {
		t.Fatal("legacy ancestor index must manage child")
	}
	writeMarker(t, root, `{"schema_version":1,"managed":true,"guard":{"enabled":true}}`)
	if !projectManaged(child) {
		t.Fatal("ancestor explicit marker must manage child")
	}
	writeMarker(t, child, `{"schema_version":1,"managed":false,"guard":{"enabled":false}}`)
	if projectManaged(child) {
		t.Fatal("nearest explicit opt-out must win over ancestor index")
	}
}

func TestProjectManagedMalformedPreservesLegacy(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "index.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeMarker(t, root, `{`)
	if !projectManaged(root) {
		t.Fatal("malformed marker must not disable a legacy managed project")
	}
}

func TestAuditFastPathManagedRootContainment(t *testing.T) {
	root := t.TempDir()
	writeMarker(t, root, `{"schema_version":1,"managed":true,"guard":{"enabled":true}}`)
	child := filepath.Join(root, "child", "deep")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	guardtelemetry.AppendFastPath("read", "tiny", child)
	p := filepath.Join(root, ".kern", "adoption.log")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("managed fast path was not recorded: %v", err)
	}
	if strings.Contains(string(data), root) || strings.Contains(string(data), child) {
		t.Fatalf("fast-path event leaked a path: %s", data)
	}
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("fast-path event is not JSON: %v", err)
	}
	if ev["tool"] != "read" || ev["routed"] != false || ev["reason"] != "tiny" {
		t.Fatalf("unexpected fast-path event: %s", data)
	}

	unmanaged := t.TempDir()
	guardtelemetry.AppendFastPath("read", "tiny", unmanaged)
	if _, err := os.Stat(filepath.Join(unmanaged, ".kern")); !os.IsNotExist(err) {
		t.Fatalf("unmanaged project received kern state: %v", err)
	}

	writeMarker(t, root, `{"schema_version":1,"managed":false,"guard":{"enabled":false}}`)
	guardtelemetry.AppendFastPath("read", "tiny", child)
	data, err = os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), "\n"); count != 1 {
		t.Fatalf("disabled project emitted another event, got %d lines", count)
	}
}

func TestAuditFastPathFailureTolerantAndReasonSafe(t *testing.T) {
	root := t.TempDir()
	writeMarker(t, root, `{"schema_version":1,"managed":true,"guard":{"enabled":true}}`)
	if err := os.Mkdir(filepath.Join(root, ".kern", "adoption.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	guardtelemetry.AppendFastPath("read", "tiny", root) // must not panic
	guardtelemetry.AppendFastPath("read", "unexpected-subject", root)
	if _, err := os.Stat(filepath.Join(root, ".kern", "adoption.log")); err != nil {
		t.Fatalf("failure-tolerance fixture changed unexpectedly: %v", err)
	}
}
