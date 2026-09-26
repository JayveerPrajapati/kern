package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// policyUsage is the help text for kern policy.
const policyUsage = `usage: kern policy <set|get|apply> [flags]

Org-level governance policy distribution (P13 stage 1). Manages the org
policy document at <org-root>/.kern/org-policy.json — the single source of
truth enterprise project firewalls are built from. The org root resolves from
--root, else $KERN_ORG_ROOT; no org root means no org governance (each
project's own .kern store stays authoritative).

Subcommands:
  set        write/merge the org policy (--file JSON array of policies, or
             {"policies": [...]}; --merge merges by policy ID with the
             existing document)
  get        print the org policy (+ recorded/content hashes + drift report)
  apply      reload the org policy document into canonical state (re-records
             the content hash when the file was edited out-of-band) and
             report drift; a running enterprise server picks the document up
             via POST /org/policies/apply (or on restart)

Flags:
  --file F   policy JSON file for set (array or {"policies": [...]})
  --merge    merge by policy ID instead of replacing the whole set (set)
  --json     emit JSON output (get)
  --root R   org root (default: $KERN_ORG_ROOT)
`

// runPolicy implements `kern policy set|get|apply`.
func runPolicy(rest []string) {
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, policyUsage)
		return
	}
	sub := rest[0]
	f, args, err := parseFlags(rest[1:])
	if err != nil {
		fatalUsage("policy: %v", err)
	}
	if len(args) > 0 {
		fatalUsage("policy: unexpected argument: %s", args[0])
	}
	orgRoot := f.root
	if orgRoot == "" {
		orgRoot = governance.OrgRoot()
	}
	switch sub {
	case "set":
		if orgRoot == "" {
			fatal("policy set: no org root configured — pass --root or set %s", governance.OrgRootEnv)
		}
		runPolicySet(orgRoot, f)
	case "get":
		runPolicyGet(orgRoot, f)
	case "apply":
		if orgRoot == "" {
			fatal("policy apply: no org root configured — pass --root or set %s", governance.OrgRootEnv)
		}
		runPolicyApply(orgRoot, f)
	default:
		fatalUsage("policy: unknown subcommand %q\n%s", sub, policyUsage)
	}
}

// runPolicySet implements `kern policy set --file F [--merge]`: it loads the
// policy JSON (a bare array of domain.Policy or the {"policies": [...]}
// envelope), optionally merges by policy ID with the existing org document,
// and persists the result atomically to the org policy store.
func runPolicySet(orgRoot string, f flags) {
	if f.file == "" {
		fatalUsage("policy set: --file is required (JSON array of policies or {\"policies\": [...]})")
	}
	data, err := os.ReadFile(f.file)
	if err != nil {
		fatal("policy set: %v", err)
	}
	var policies []domain.Policy
	if err := json.Unmarshal(data, &policies); err != nil {
		// Fall back to the envelope form {"policies": [...]}.
		var env struct {
			Policies []domain.Policy `json:"policies"`
		}
		if err2 := json.Unmarshal(data, &env); err2 != nil {
			fatal("policy set: invalid policy JSON: %v", err)
		}
		policies = env.Policies
	}
	if len(policies) == 0 {
		fatalUsage("policy set: no policies in %s", f.file)
	}
	if f.merge {
		merged, err := mergeOrgPolicies(orgRoot, policies)
		if err != nil {
			fatal("policy set: %v", err)
		}
		policies = merged
	}
	hash, err := governance.SaveOrgPolicy(orgRoot, policies)
	if err != nil {
		fatal("policy set: %v", err)
	}
	fmt.Printf("org policy written to %s (%d policies, hash %s)\n", governance.OrgPolicyPath(orgRoot), len(policies), hash)
}

// mergeOrgPolicies merges newPolicies into the existing org policy document
// by policy ID: a new entry replaces a same-ID existing entry, existing
// entries not mentioned are kept, and entirely new IDs are appended. A
// missing existing document (first write) keeps only the new entries.
func mergeOrgPolicies(orgRoot string, newPolicies []domain.Policy) ([]domain.Policy, error) {
	doc, ok, err := governance.LoadOrgPolicy(orgRoot)
	if err != nil {
		return nil, err
	}
	existing := []domain.Policy{}
	if ok {
		existing = doc.Policies
	}
	byID := map[string]domain.Policy{}
	order := []string{}
	for _, p := range existing {
		if _, seen := byID[p.ID]; !seen {
			order = append(order, p.ID)
		}
		byID[p.ID] = p
	}
	for _, p := range newPolicies {
		if _, seen := byID[p.ID]; !seen {
			order = append(order, p.ID)
		}
		byID[p.ID] = p
	}
	merged := make([]domain.Policy, 0, len(order))
	for _, id := range order {
		merged = append(merged, byID[id])
	}
	return merged, nil
}

// runPolicyGet implements `kern policy get`: it prints the org policy
// document with the recorded hash (what project firewalls were last built
// with) and the current content hash (what a build NOW would enforce), plus
// the drift verdict: DETECTED when the file was modified since it was last
// set/applied, none otherwise. A missing document prints a notice (exit 0) —
// "no org policy" is a valid answer to a query.
func runPolicyGet(orgRoot string, f flags) {
	if orgRoot == "" {
		fmt.Printf("no org root configured (set %s or pass --root): no org policy\n", governance.OrgRootEnv)
		return
	}
	doc, ok, err := governance.LoadOrgPolicy(orgRoot)
	if err != nil {
		fatal("policy get: %v", err)
	}
	if !ok {
		fmt.Printf("no org policy configured at %s\n", governance.OrgPolicyPath(orgRoot))
		return
	}
	contentHash := governance.PolicyHash(doc.Policies)
	drifted := contentHash != doc.Hash
	if f.json {
		printJSON(map[string]any{
			"root":          orgRoot,
			"path":          governance.OrgPolicyPath(orgRoot),
			"version":       doc.Version,
			"recorded_hash": doc.Hash,
			"content_hash":  contentHash,
			"drifted":       drifted,
			"count":         len(doc.Policies),
			"policies":      doc.Policies,
		})
		return
	}
	fmt.Printf("org root:  %s\n", orgRoot)
	fmt.Printf("policy:    %s\n", governance.OrgPolicyPath(orgRoot))
	fmt.Printf("hash:      %s\n", doc.Hash)
	if drifted {
		fmt.Printf("drift:     DETECTED — org policy file was modified since it was last applied (content hash %s); run 'kern policy apply' to re-record\n", contentHash)
	} else {
		fmt.Printf("drift:     none\n")
	}
	for _, p := range doc.Policies {
		fmt.Printf("  %s\t%s\t%s\tenabled=%v\n", p.ID, p.Name, p.Rule, p.Enabled)
	}
}

// runPolicyApply implements `kern policy apply`: it reloads the org policy
// document into canonical state. When the file was edited out-of-band since
// it was last set/applied (recorded hash != content hash), it atomically
// re-saves the document so the recorded hash matches the content — the
// "re-propagate" step that makes the file the versioned source of truth
// again. A running enterprise server picks the change up via POST
// /org/policies/apply (or on restart); without a server the file itself is
// the propagation target.
func runPolicyApply(orgRoot string, f flags) {
	doc, ok, err := governance.LoadOrgPolicy(orgRoot)
	if err != nil {
		fatal("policy apply: %v", err)
	}
	if !ok {
		fatal("policy apply: no org policy at %s — run 'kern policy set' first", governance.OrgPolicyPath(orgRoot))
	}
	contentHash := governance.PolicyHash(doc.Policies)
	if contentHash != doc.Hash {
		if _, err := governance.SaveOrgPolicy(orgRoot, doc.Policies); err != nil {
			fatal("policy apply: %v", err)
		}
		fmt.Printf("org policy re-applied: %d policies at %s\n", len(doc.Policies), governance.OrgPolicyPath(orgRoot))
		fmt.Printf("hash re-recorded: %s -> %s\n", doc.Hash, contentHash)
		return
	}
	fmt.Printf("org policy already applied: %d policies at %s\n", len(doc.Policies), governance.OrgPolicyPath(orgRoot))
	fmt.Printf("hash: %s\n", doc.Hash)
}
