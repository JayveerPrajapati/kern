package llm

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// LLMStatus is the offline LLM wiring report `kern doctor` renders: a level
// (ok|warn|fail) and a detail naming the exact state, what still works, and
// what degrades. Status never probes remote providers — construction is
// offline; only the local Ollama endpoint gets the bounded 2s probe.
type LLMStatus struct {
	Level  string `json:"level"`
	Detail string `json:"detail"`
}

// ProviderDisabled reports whether the LLM stage is explicitly pinned off
// (KERN_LLM_PROVIDER=none/off/disabled) — deterministic mode.
func ProviderDisabled() bool {
	switch providerName() {
	case "none", "off", "disabled":
		return true
	}
	return false
}

// LLMRequired reports whether the operator pinned the LLM stage as required
// (KERN_LLM_REQUIRED, or llm.required in .kern/config.json). Optional is the
// default: a missing provider degrades gracefully. Required escalates it to
// a hard failure — CI/enterprise gating.
func LLMRequired() bool {
	return config.Bool("", "KERN_LLM_REQUIRED", "llm.required", false)
}

// CredentialEnv maps a pinned provider name to the environment variable
// holding its API key ("" when the provider needs none — the local agent
// CLIs read their own login state). Doctor reports a missing credential env
// without probing the network.
func CredentialEnv(provider string) string {
	switch provider {
	case "openai", "openrouter", "groq", "litellm", "vllm", "azure":
		return "OPENAI_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "google", "gemini":
		return "GEMINI_API_KEY"
	}
	return ""
}

// KnownProvider reports whether name is a recognized KERN_LLM_PROVIDER value
// (the same set NewProvider dispatches on), so env validation does not flag
// valid pins like "claude" as unknown.
func KnownProvider(name string) bool {
	switch name {
	case "auto", "ollama",
		"none", "off", "disabled",
		"mcp", "host", "sampling",
		"openai", "openrouter", "groq", "litellm", "vllm", "azure",
		"anthropic", "google", "gemini",
		"claude", "codex", "gemini-cli", "qwen", "agy", "antigravity":
		return true
	}
	return false
}

// Status classifies the effective LLM wiring:
//
//   - disabled (KERN_LLM_PROVIDER=none) → ok, deterministic mode, nothing probed
//   - pinned remote/CLI provider → wiring described offline; a missing key
//     env is reported without contacting the endpoint
//   - auto (the default chain: host sampler → agent CLIs → Ollama) → a
//     stopped Ollama next to installed agent CLIs is the NORMAL healthy
//     state (ok): LLM calls are served by the CLIs, and only
//     embedding-backed features (semantic doc search) degrade to keyword
//   - no provider at all → warn; fail when LLMRequired()
//
// Ollama is optional by default (A5): it is one provider in the chain, not
// a prerequisite, and doctor must not present its absence as a broken
// install.
func Status() LLMStatus {
	name := providerName()
	if ProviderDisabled() {
		return LLMStatus{Level: "ok", Detail: "LLM stage disabled (KERN_LLM_PROVIDER=" + name + ") — deterministic mode; no LLM features probed"}
	}
	required := LLMRequired()
	if name != "auto" && name != "ollama" {
		// Pinned provider: describe the wiring offline. Construction is
		// deterministic (network only at call time), so a constructor
		// error is a config problem reportable without a probe.
		if _, err := NewProvider(); err != nil {
			return escalate(required, err.Error())
		}
		if env := CredentialEnv(name); env != "" && os.Getenv(env) == "" {
			return escalate(required, fmt.Sprintf("provider %s pinned (KERN_LLM_PROVIDER=%s) but %s is not set — set it to enable this provider", name, name, env))
		}
		return LLMStatus{Level: "ok", Detail: "LLM provider pinned via KERN_LLM_PROVIDER=" + name + " (offline wiring check; the endpoint is probed at call time)"}
	}
	c := New("")
	if c.Available() {
		return LLMStatus{Level: "ok", Detail: c.Base + " reachable, model " + c.Model}
	}
	agents := AvailableLocalAgents()
	if name == "ollama" || len(agents) == 0 {
		// Pinned to ollama (the chain — and its CLI fallback — is not used),
		// or nothing at all can serve LLM calls.
		detail := c.Base + " not reachable"
		if name == "ollama" {
			detail += " (KERN_LLM_PROVIDER=ollama pinned — no agent-CLI fallback)"
		} else {
			detail += " and no agent CLI installed (claude/opencode/codex/gemini/qwen)"
		}
		detail += " — deterministic tools unaffected; start ollama or install an agent CLI to enable LLM-assisted features"
		return escalate(required, detail)
	}
	// Auto chain with agent CLIs: healthy. Ollama only adds embeddings.
	return LLMStatus{
		Level:  "ok",
		Detail: c.Base + " not running; LLM calls are served by local agent CLI(s): " + strings.Join(agents, ", ") + " (auto chain tries CLIs first) — only embedding-backed features (semantic doc search) degrade to keyword mode; start ollama to enable them",
	}
}

// escalate maps a missing/down provider to warn, or fail when required.
func escalate(required bool, detail string) LLMStatus {
	if required {
		return LLMStatus{Level: "fail", Detail: detail + " (KERN_LLM_REQUIRED is set)"}
	}
	return LLMStatus{Level: "warn", Detail: detail}
}
