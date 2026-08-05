package mcp

import (
	"fmt"
	"strings"
)

// Prompt is an MCP prompt definition (workflow template). Each prompt
// describes a multi-step recipe the client should run with the kern tools.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument is a single argument to a workflow prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// prompts is the curated workflow catalog. Modeled on the review-oriented
// recipes competitors ship, but every step runs locally through kern tools.
var prompts = []Prompt{
	{
		Name:        "review_changes",
		Description: "Pre-commit review of the current (or ranged) diff: map changed files to affected symbols, blast radius, risk and test gaps, then enforce architectural boundaries.",
		Arguments: []PromptArgument{
			{Name: "range", Description: "Git range like 'HEAD~2..HEAD'. Empty = working-tree changes", Required: false},
			{Name: "root", Description: "Project root (defaults to current directory)", Required: false},
		},
	},
	{
		Name:        "architecture_map",
		Description: "High-level architecture overview: subsystems/communities, hub symbols, cross-community coupling warnings and coupling hotspots.",
		Arguments: []PromptArgument{
			{Name: "root", Description: "Project root (defaults to current directory)", Required: false},
		},
	},
	{
		Name:        "debug_issue",
		Description: "Investigate a failing test, panic, or bug report: resolve the task to the relevant symbols, overlay a stack trace, then read the minimal source slices.",
		Arguments: []PromptArgument{
			{Name: "task", Description: "The bug report, panic message, or failing test description", Required: true},
			{Name: "trace", Description: "Optional stack trace / pprof -top text to overlay", Required: false},
			{Name: "root", Description: "Project root (defaults to current directory)", Required: false},
		},
	},
	{
		Name:        "onboard_developer",
		Description: "Understand a codebase fast: project map, hub symbols, largest declarations, and the architecture overview in one pass.",
		Arguments: []PromptArgument{
			{Name: "root", Description: "Project root (defaults to current directory)", Required: false},
		},
	},
	{
		Name:        "pre_merge_check",
		Description: "Pre-merge readiness: token-optimised review context for a PR diff, untested hotspots in the blast radius, and boundary enforcement.",
		Arguments: []PromptArgument{
			{Name: "range", Description: "Git range like 'main..HEAD' or 'origin/main..HEAD'", Required: true},
			{Name: "root", Description: "Project root (defaults to current directory)", Required: false},
		},
	},
}

// promptStep formats one numbered tool-call instruction.
func promptStep(i int, tool, root, rng, instructions string) string {
	s := fmt.Sprintf("%d. Call `%s` (root %q", i, tool, root)
	if rng != "" {
		s += fmt.Sprintf(", range %q", rng)
	}
	s += "). " + instructions
	return s
}

// promptText renders the step-by-step instructions for a prompt, filling in
// argument values. The text tells the agent which kern tools to call in which
// order and how to interpret the results.
func promptText(name string, args map[string]any) string {
	root := argString(args, "root")
	if root == "" {
		root = "."
	}
	rng := argString(args, "range")
	n := func(i int, tool, instructions string) string {
		return promptStep(i, tool, root, rng, instructions)
	}
	switch name {
	case "review_changes":
		return strings.Join([]string{
			"Review the current changes against the call graph.",
			n(1, "kern_changes", "Map changed files to affected symbols, blast radius, risk scores and test gaps."),
			n(2, "kern_test_gaps", "Confirm which untested hotspots fall inside the blast radius."),
			n(3, "kern_guard_check", "Enforce architectural boundaries; REJECT exit 2 means a boundary violation."),
			"Synthesize a prioritized review: blockers first, then risk, then test gaps. Cite symbol file:line.",
		}, "\n")
	case "architecture_map":
		return strings.Join([]string{
			"Build an architecture digest of the project.",
			n(1, "kern_arch", "List subsystems/communities with their hubs and packages, plus coupling warnings."),
			n(2, "kern_hubs", "Rank the most depended-on symbols and cross-package bridges."),
			"Synthesize: what the subsystems are, what everything flows through, and where changes ripple.",
		}, "\n")
	case "debug_issue":
		task := argString(args, "task")
		step := 1
		lines := []string{
			fmt.Sprintf("Investigate this issue: %q", task),
			n(step, "kern_probe", fmt.Sprintf("Resolve the issue description %q to the relevant symbols with budget-capped micro-context.", task)),
		}
		step = 2
		if t := argString(args, "trace"); t != "" {
			lines = append(lines, fmt.Sprintf("%d. Call `kern_trace` (trace %q) to overlay the stack frames on the call graph and find hot symbols.", step, t))
			step++
		}
		lines = append(lines,
			n(step, "kern_context", "Read the minimal source slice for each hot symbol."),
			n(step+1, "kern_near", "Expand the neighborhood of the root-cause candidates."),
			"Hypothesize the root cause, verify against source, and state the fix with file:line references.",
		)
		return strings.Join(lines, "\n")
	case "onboard_developer":
		return strings.Join([]string{
			"Onboard to this codebase in one pass.",
			n(1, "kern_project_map", "Get the compressed project map (files + symbols)."),
			n(2, "kern_hubs", "Identify what everything depends on."),
			n(3, "kern_larges", "Spot god functions that dominate the code."),
			n(4, "kern_arch", "See the subsystem structure and coupling."),
			"Synthesize an onboarding brief: structure, entry points, hotspots, and where to be careful.",
		}, "\n")
	case "pre_merge_check":
		if rng == "" {
			return "pre_merge_check requires a `range` argument (e.g. 'main..HEAD')."
		}
		return strings.Join([]string{
			"Assess PR readiness before merge.",
			n(1, "kern_review", "Get token-optimised review context for the diff: changed symbols, blast radius, risk, test gaps."),
			n(2, "kern_test_gaps", "Find untested hotspots touched by the change."),
			n(3, "kern_guard_check", "Enforce boundaries across the changed files."),
			"Give a go/no-go verdict with reasons and any required follow-ups.",
		}, "\n")
	}
	return "unknown prompt"
}
