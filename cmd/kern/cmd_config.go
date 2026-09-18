package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// runConfig prints the effective value and source of every file-configurable
// key (env var > .kern/config.json > built-in default). Env-only knobs —
// secrets and fail-closed/safety toggles — are intentionally not listed.
// `kern config` exits 0 even when no config file exists (all defaults shown).
func runConfig(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) > 0 {
		fatalUsage("config: unexpected argument %q", args[0])
	}
	root := f.root
	if f.json {
		out := map[string]any{}
		for _, k := range config.Registry {
			v, src := config.Effective(root, k)
			if k.Type == "duration" {
				if d, ok := v.(time.Duration); ok {
					v = d.String()
				}
			}
			out[k.Key] = map[string]any{"value": v, "source": src}
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fatal("config: %v", err)
		}
		fmt.Println(string(b))
		return
	}
	for _, k := range config.Registry {
		v, src := config.Effective(root, k)
		fmt.Printf("%s=%s (%s)\n", k.Key, formatConfigValue(v), src)
	}
}

// formatConfigValue renders a config value for the human-readable `kern
// config` output, with deterministic map ordering.
func formatConfigValue(v any) string {
	switch t := v.(type) {
	case time.Duration:
		return t.String()
	case map[string]string:
		keys := slices.Sorted(maps.Keys(t))
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+t[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []string:
		return "[" + strings.Join(t, ", ") + "]"
	default:
		return fmt.Sprintf("%v", t)
	}
}
