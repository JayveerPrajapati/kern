// Custom (user-declared) agent adapters.
//
// Forked or private agent builds can be wired into `kern setup` without
// code changes by declaring them as JSON:
//
//	~/.config/kern/agents.json   user scope
//	.kern/agents.json            project scope (wins on name clash)
//
// Schema (JSONC tolerated; unknown fields are rejected):
//
//	[
//	  {
//	    "name":  "myagent",                 // required
//	    "path":  "~/.myagent/config.json",  // required; ~ and $VAR expanded;
//	                                      // relative paths resolve against
//	                                      // the project root
//	    "key":   "mcpServers",              // required; JSON key that holds
//	                                      // the servers map
//	    "entry": "stdio",                   // "stdio" (default) | "cmd"
//	    "scope": "global"                   // "global" (default) | "repo"
//	  }
//	]
//
// A custom entry whose name matches a builtin replaces it (later wins),
// so an incorrect builtin path can be corrected locally without a kern
// release. Malformed custom files never abort the rest of setup: they
// surface as error Status rows while all other wiring proceeds.
package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// customAdapter is the wire format declared in agents.json.
type customAdapter struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Key   string `json:"key"`
	Entry string `json:"entry"`
	Scope string `json:"scope"`
}

// userAgentsPath returns the user-scope declaration file (same XDG/home
// resolution as the builtin global configs — see globalConfig).
func userAgentsPath() string {
	return globalConfig("kern", "agents.json")("")
}

// projectAgentsPath returns the project-scope declaration file.
func projectAgentsPath(root string) string {
	return filepath.Join(root, ".kern", "agents.json")
}

// effectiveAdapters returns the builtin registry plus validated custom
// adapters (user file first, then project file; on a name clash the
// later definition wins). Load errors are returned alongside so callers
// can surface them without aborting.
func effectiveAdapters(root string) ([]adapter, []error) {
	var errs []error
	out := make([]adapter, 0, len(adapters)+4)
	out = append(out, adapters...) // copy: never mutate the builtin registry

	add := func(file string) {
		if file == "" {
			return
		}
		data, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			errs = append(errs, fmt.Errorf("%s: %w", file, err))
			return
		}
		var customs []customAdapter
		dec := json.NewDecoder(bytes.NewReader(stripJSONC(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&customs); err != nil {
			errs = append(errs, fmt.Errorf("%s is not a valid agents file: %w", file, err))
			return
		}
		for i, c := range customs {
			a, err := c.toAdapter(root)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: entry %d: %w", file, i+1, err))
				continue
			}
			// Name clash: replace the earlier definition (builtin or
			// user-scope) instead of appending a duplicate.
			replaced := false
			for j := range out {
				if out[j].name == a.name {
					out[j] = a
					replaced = true
					break
				}
			}
			if !replaced {
				out = append(out, a)
			}
		}
	}

	add(userAgentsPath())
	add(projectAgentsPath(root))
	return out, errs
}

// toAdapter validates the declaration and converts it to an adapter.
func (c customAdapter) toAdapter(root string) (adapter, error) {
	if strings.TrimSpace(c.Name) == "" {
		return adapter{}, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(c.Path) == "" {
		return adapter{}, fmt.Errorf("path is required")
	}
	if strings.TrimSpace(c.Key) == "" {
		return adapter{}, fmt.Errorf("key is required")
	}
	entryFn := stdioEntry
	switch strings.ToLower(strings.TrimSpace(c.Entry)) {
	case "", "stdio":
	case "cmd":
		entryFn = cmdEntry
	default:
		return adapter{}, fmt.Errorf("entry must be \"stdio\" or \"cmd\", got %q", c.Entry)
	}
	scope := "global"
	switch strings.ToLower(strings.TrimSpace(c.Scope)) {
	case "", "global":
	case "repo":
		scope = "repo"
	default:
		return adapter{}, fmt.Errorf("scope must be \"global\" or \"repo\", got %q", c.Scope)
	}
	path := expandCustomPath(c.Path, root)
	return adapter{
		name:  c.Name,
		path:  func(string) string { return path },
		key:   c.Key,
		entry: entryFn,
		scope: scope,
	}, nil
}

// expandCustomPath expands $VAR/${VAR} references and a leading ~ to the
// user home, then resolves relative paths against the project root.
func expandCustomPath(p, root string) string {
	p = os.ExpandEnv(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}
