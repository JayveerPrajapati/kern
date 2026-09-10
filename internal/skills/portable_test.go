package skills

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// sampleSkill is a fully-populated skill used by the signature tests.
func sampleSkill() Skill {
	return Skill{
		Name:        "sample",
		Description: "A sample skill",
		Policies:    []string{"safe-change"},
		Evals:       []string{"tests pass"},
		Examples:    []string{"sample <target>"},
		Manifest: SkillManifest{
			Version:      "1.0.0",
			Author:       "alice",
			Permissions:  []governance.Permission{{Resource: "source", Action: "read"}},
			Dependencies: []string{"kern-investigate"},
		},
		Body: "# Sample\nplaybook body",
	}
}

func TestLoadSkillsFromDir(t *testing.T) {
	dir := t.TempDir()
	writeSkill := func(name, frontmatter, body string) {
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte("---\n"+frontmatter+"---\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill("zeta-skill", "version: 1.0.0\npermissions:\n  - source:read\n", "# Zeta\nbody")
	writeSkill("alpha-skill", "author: alice\ndependencies:\n  - kern-investigate\n", "# Alpha\nbody2")
	// Unreadable SKILL.md: a directory named SKILL.md makes ReadFile fail.
	if err := os.MkdirAll(filepath.Join(dir, "broken-skill", "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadSkillsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadSkillsFromDir: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("loaded %d skills, want 2 (broken skipped)", len(skills))
	}
	if skills[0].Name != "alpha-skill" || skills[1].Name != "zeta-skill" {
		t.Errorf("skills not sorted by name: %q, %q", skills[0].Name, skills[1].Name)
	}
	// alpha: manifest author + dependency, description/body extracted.
	if skills[0].Manifest.Author != "alice" || len(skills[0].Manifest.Dependencies) != 1 || skills[0].Manifest.Dependencies[0] != "kern-investigate" {
		t.Errorf("alpha manifest = %+v", skills[0].Manifest)
	}
	if skills[0].Description == "" || !strings.Contains(skills[0].Body, "Alpha") {
		t.Errorf("alpha desc/body wrong: %q / %q", skills[0].Description, skills[0].Body)
	}
	// zeta: permission parsed.
	if len(skills[1].Manifest.Permissions) != 1 || skills[1].Manifest.Permissions[0].Resource != "source" || skills[1].Manifest.Permissions[0].Action != "read" {
		t.Errorf("zeta manifest = %+v", skills[1].Manifest)
	}
	if skills[0].Source != dir || skills[1].Source != dir {
		t.Errorf("Source should be the dir, got %q / %q", skills[0].Source, skills[1].Source)
	}

	// Missing dir -> error.
	if _, err := LoadSkillsFromDir(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing dir should error")
	}
}

func TestParseManifest(t *testing.T) {
	doc := `---
name: my-skill
version: 1.2.0
author: ops-team
permissions:
  - source:read
  - tests:write
dependencies:
  - kern-investigate
  - kern-safe-change
---
# My Skill
body here`
	m := ParseManifest([]byte(doc))
	if m.Version != "1.2.0" || m.Author != "ops-team" {
		t.Errorf("version/author = %q / %q, want 1.2.0 / ops-team", m.Version, m.Author)
	}
	if len(m.Permissions) != 2 ||
		m.Permissions[0].Resource != "source" || m.Permissions[0].Action != "read" ||
		m.Permissions[1].Resource != "tests" || m.Permissions[1].Action != "write" {
		t.Errorf("permissions = %+v", m.Permissions)
	}
	if len(m.Dependencies) != 2 || m.Dependencies[0] != "kern-investigate" || m.Dependencies[1] != "kern-safe-change" {
		t.Errorf("dependencies = %v", m.Dependencies)
	}

	// Absent frontmatter -> zero manifest.
	if got := ParseManifest([]byte("no frontmatter here")); !reflect.DeepEqual(got, SkillManifest{}) {
		t.Errorf("absent frontmatter should yield zero manifest, got %+v", got)
	}

	// Unknown keys ignored; malformed permission entry kept as-is.
	doc2 := `---
bogus_key: whatever
permissions:
  - no-colon-entry
---
body`
	m2 := ParseManifest([]byte(doc2))
	if len(m2.Permissions) != 1 || m2.Permissions[0].Resource != "no-colon-entry" || m2.Permissions[0].Action != "" {
		t.Errorf("malformed permission should be kept as-is, got %+v", m2.Permissions)
	}

	// Inline list style also parses.
	doc3 := "---\npermissions: source:read, tests:write\ndependencies: a, b\n---\nbody"
	m3 := ParseManifest([]byte(doc3))
	if len(m3.Permissions) != 2 || m3.Permissions[1].Resource != "tests" {
		t.Errorf("inline permissions = %+v", m3.Permissions)
	}
	if len(m3.Dependencies) != 2 || m3.Dependencies[1] != "b" {
		t.Errorf("inline dependencies = %v", m3.Dependencies)
	}
}

func TestValidateSkill(t *testing.T) {
	valid := Skill{Name: "ok", Manifest: SkillManifest{
		Version:      "1.0.0",
		Author:       "a",
		Permissions:  []governance.Permission{{Resource: "source", Action: "read"}},
		Dependencies: []string{"dep"},
	}}
	if err := ValidateSkill(valid); err != nil {
		t.Errorf("valid skill should pass: %v", err)
	}
	if err := ValidateSkill(Skill{Name: "", Manifest: valid.Manifest}); err == nil {
		t.Error("empty name should error")
	}
	if err := ValidateSkill(Skill{Name: "x", Manifest: SkillManifest{Permissions: []governance.Permission{{Resource: "", Action: "read"}}}}); err == nil {
		t.Error("permission with empty resource should error")
	}
	if err := ValidateSkill(Skill{Name: "x", Manifest: SkillManifest{Permissions: []governance.Permission{{Resource: "source", Action: ""}}}}); err == nil {
		t.Error("permission with empty action should error")
	}
	if err := ValidateSkill(Skill{Name: "x", Manifest: SkillManifest{Dependencies: []string{""}}}); err == nil {
		t.Error("empty dependency name should error")
	}
	// Empty body OK (pure manifest).
	if err := ValidateSkill(Skill{Name: "x", Manifest: SkillManifest{Version: "1.0.0"}}); err != nil {
		t.Errorf("empty body should be valid: %v", err)
	}
}

func TestValidateSkillZeroManifest(t *testing.T) {
	if err := ValidateSkill(Skill{Name: "name-only"}); err != nil {
		t.Errorf("skill with no manifest should be valid (name only): %v", err)
	}
}

func TestPreviewPermissions(t *testing.T) {
	s := Skill{Manifest: SkillManifest{Permissions: []governance.Permission{
		{Resource: "source", Action: "read"},
		{Resource: "tests", Action: "write"},
		{Resource: "source", Action: "read"}, // duplicate: first wins
		{Resource: "source", Action: "write"},
	}}}
	got := PreviewPermissions(s)
	if len(got) != 3 {
		t.Fatalf("PreviewPermissions = %d, want 3 deduped", len(got))
	}
	want := []governance.Permission{
		{Resource: "source", Action: "read"},
		{Resource: "tests", Action: "write"},
		{Resource: "source", Action: "write"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSignVerifySkill(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	s := sampleSkill()
	sig, err := SignSkill(s, key)
	if err != nil {
		t.Fatalf("SignSkill: %v", err)
	}
	if sig.PublicKey == "" || sig.Value == "" || sig.Timestamp == "" {
		t.Fatalf("signature fields empty: %+v", sig)
	}
	if err := VerifySkill(s, sig); err != nil {
		t.Errorf("verify should pass: %v", err)
	}

	// Tampered Body -> verify error (Body is part of the canonical payload).
	tampered := s
	tampered.Body = s.Body + "\ntampered"
	if err := VerifySkill(tampered, sig); err == nil {
		t.Error("tampered body should fail verification")
	}

	// Wrong key: signature bytes from key1 but embedded public key from key2
	// (NewKeyFromSeed proves seed-derived keys sign too).
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	key2 := ed25519.NewKeyFromSeed(seed)
	pub2, ok := key2.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("key2 public type assertion failed")
	}
	bad := Signature{
		PublicKey: base64.StdEncoding.EncodeToString(pub2),
		Value:     sig.Value,
		Timestamp: sig.Timestamp,
	}
	if err := VerifySkill(s, bad); err == nil {
		t.Error("signature with mismatched key should fail")
	}

	// Nil key -> SignSkill error.
	if _, err := SignSkill(s, nil); err == nil {
		t.Error("nil key should error")
	}
}

func TestSignatureJSONRoundTrip(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	s := sampleSkill()
	sig, err := SignSkill(s, key)
	if err != nil {
		t.Fatalf("SignSkill: %v", err)
	}
	data, err := json.Marshal(sig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Signature
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := VerifySkill(s, decoded); err != nil {
		t.Errorf("verify after JSON round-trip: %v", err)
	}
}
