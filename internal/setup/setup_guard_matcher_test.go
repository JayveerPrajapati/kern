package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// func TestWireCodexHooksFullMatcher pins the D-fix: wireCodexHooks must write the
// full four-family matcher (Read|Grep|Glob|Bash), not the former Bash-only
// matcher that left Read/Grep/Glob unguarded on Codex. The other five hosts
// already wrote the full matcher; codex was the lone outlier (verified
// against ~/.codex/hooks.json).
func TestWireCodexHooksFullMatcher(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	st := wireCodexHooks(dir)
	if !st.Installed {
		t.Fatalf("wireCodexHooks failed: %s", st.Note)
	}
	b, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatalf("read codex hooks.json: %v", err)
	}
	if !strings.Contains(string(b), `"matcher": "Read|Grep|Glob|Bash"`) {
		t.Fatalf("codex PreToolUse matcher must be Read|Grep|Glob|Bash:\n%s", b)
	}
	// The hook must reference the guard script (wiring sanity).
	if !strings.Contains(string(b), "kern-guard.sh") {
		t.Fatalf("codex hook does not reference kern-guard.sh:\n%s", b)
	}
}

// TestCheckHookMatcherCompleteness exercises checkHookMatcherCompleteness
// against every host config shape: the full matcher must be silent, a
// partial matcher (the codex regression) must warn naming the missing
// families, kiro's "read"+"shell" vocabulary must be accepted, and configs
// that do not reference kern-guard.sh must be ignored.
func TestCheckHookMatcherCompleteness(t *testing.T) {
	writeCfg := func(t *testing.T, path string, content any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.MarshalIndent(content, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name    string
		path    func(home string) string // which host slot the config lands in (host-dependent semantics)
		content any
		want    int    // expected number of WARN statuses for this host
		wantIn  string // substring that must appear in the WARN note ("" = no check)
	}{
		{
			name: "codex full matcher is silent",
			path: func(h string) string { return filepath.Join(h, ".codex", "hooks.json") },
			content: map[string]any{
				"hooks": map[string]any{
					"PreToolUse": []any{
						map[string]any{
							"matcher": "Read|Grep|Glob|Bash",
							"hooks":   []any{map[string]any{"type": "command", "command": "/home/x/.kern/hooks/kern-guard.sh"}},
						},
					},
				},
			},
			want: 0,
		},
		{
			name: "codex bash-only matcher warns (the regression)",
			path: func(h string) string { return filepath.Join(h, ".codex", "hooks.json") },
			content: map[string]any{
				"hooks": map[string]any{
					"PreToolUse": []any{
						map[string]any{
							"matcher": "Bash",
							"hooks":   []any{map[string]any{"type": "command", "command": "/home/x/.kern/hooks/kern-guard.sh"}},
						},
					},
				},
			},
			want:   1,
			wantIn: "missing read, grep, glob",
		},
		{
			name: "gemini full matcher is silent",
			path: func(h string) string { return filepath.Join(h, ".gemini", "settings.json") },
			content: map[string]any{
				"hooks": map[string]any{
					"BeforeTool": []any{
						map[string]any{
							"matcher": "read_file|run_shell_command|grep|glob|list_files",
							"hooks":   []any{map[string]any{"type": "command", "command": "/home/x/.kern/hooks/kern-guard.sh"}},
						},
					},
				},
			},
			want: 0,
		},
		{
			name: "antigravity full matcher is silent",
			path: func(h string) string { return filepath.Join(h, ".gemini", "config", "hooks.json") },
			content: map[string]any{
				"kern-guard": map[string]any{
					"enabled": true,
					"PreToolUse": []any{
						map[string]any{
							"matcher": "run_command|view_file|grep_search|find_by_name",
							"hooks":   []any{map[string]any{"type": "command", "command": "/home/x/.kern/hooks/kern-guard.sh"}},
						},
					},
				},
			},
			want: 0,
		},
		{
			name: "kiro read+shell vocabulary is accepted",
			path: func(h string) string { return filepath.Join(h, ".kiro", "hooks", "kern-guard.json") },
			content: map[string]any{
				"version": "v1",
				"hooks": []any{
					map[string]any{"name": "kern-guard-read", "trigger": "PreToolUse", "matcher": "read", "action": map[string]any{"type": "command", "command": "/home/x/.kern/hooks/kern-guard.sh"}},
					map[string]any{"name": "kern-guard-shell", "trigger": "PreToolUse", "matcher": "shell", "action": map[string]any{"type": "command", "command": "/home/x/.kern/hooks/kern-guard.sh"}},
				},
			},
			want: 0,
		},
		{
			name: "kiro read-only matcher warns",
			path: func(h string) string { return filepath.Join(h, ".kiro", "hooks", "kern-guard.json") },
			content: map[string]any{
				"version": "v1",
				"hooks": []any{
					map[string]any{"name": "kern-guard-read", "trigger": "PreToolUse", "matcher": "read", "action": map[string]any{"type": "command", "command": "/home/x/.kern/hooks/kern-guard.sh"}},
				},
			},
			want:   1,
			wantIn: "missing bash",
		},
		{
			name: "foreign hook without kern-guard is ignored",
			path: func(h string) string { return filepath.Join(h, ".codex", "hooks.json") },
			content: map[string]any{
				"hooks": map[string]any{
					"PreToolUse": []any{
						map[string]any{
							"matcher": "Bash",
							"hooks":   []any{map[string]any{"type": "command", "command": "/usr/bin/something-else"}},
						},
					},
				},
			},
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			root := t.TempDir()
			writeCfg(t, tc.path(home), tc.content)

			sts := checkHookMatcherCompleteness(root)
			if len(sts) != tc.want {
				t.Fatalf("expected %d WARN statuses, got %d: %+v", tc.want, len(sts), sts)
			}
			if tc.wantIn != "" {
				if len(sts) == 0 || !strings.Contains(sts[0].Note, tc.wantIn) {
					t.Fatalf("WARN note missing %q, got: %+v", tc.wantIn, sts)
				}
			}
		})
	}
}

// TestCheckHookMatcherCompletenessPreservesForeignHooks pins the merge
// contract: the matcher check only counts kern-guard hook groups, so a host
// config that carries BOTH a kern-guard group and a foreign group (e.g.
// code-review-graph's PostToolUse "Write|Edit|Bash" matcher, seen live in
// ~/.codex/hooks.json) still passes when the kern-guard group is complete.
func TestCheckHookMatcherCompletenessPreservesForeignHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	path := filepath.Join(home, ".codex", "hooks.json")
	cfg := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Read|Grep|Glob|Bash",
					"hooks":   []any{map[string]any{"type": "command", "command": "/home/x/.kern/hooks/kern-guard.sh"}},
				},
			},
			"PostToolUse": []any{
				map[string]any{
					"matcher": "Write|Edit|Bash",
					"hooks":   []any{map[string]any{"type": "command", "command": "code-review-graph update --skip-flows"}},
				},
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	sts := checkHookMatcherCompleteness(root)
	if len(sts) != 0 {
		t.Fatalf("foreign PostToolUse hook must not trip the matcher check: %+v", sts)
	}
}

// TestCheckHookMatcherCompletenessAbsentConfigIsSilent pins that a host with
// no hook config at all produces no matcher finding (presence is the
// fileStatus checks' job, not this check's).
func TestCheckHookMatcherCompletenessAbsentConfigIsSilent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	if sts := checkHookMatcherCompleteness(root); len(sts) != 0 {
		t.Fatalf("absent configs must produce no matcher findings: %+v", sts)
	}
}

// TestCheckHookMatcherCompletenessInvalidJSONIsSilent pins that a corrupt
// hook config does not panic the check (invalid JSON is not this check's
// job — the hook wiring itself will surface it).
func TestCheckHookMatcherCompletenessInvalidJSONIsSilent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sts := checkHookMatcherCompleteness(root); len(sts) != 0 {
		t.Fatalf("invalid JSON must produce no matcher findings: %+v", sts)
	}
}
