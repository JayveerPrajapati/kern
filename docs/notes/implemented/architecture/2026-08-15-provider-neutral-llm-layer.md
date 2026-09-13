# Agent Note: Provider-Neutral LLM Layer
Status: implemented

## Problem
LLM access was Ollama-specific; other vendors required code changes.

## Decision
internal/llm Provider interface (Generate/Embed/Capabilities/Stream) + NewProvider() selecting
vendor via KERN_LLM_PROVIDER (ollama|openai|anthropic|google, default ollama); NewEmbedder adapts to
docsearch/intel; agent OllamaProvider routes through the factory honoring model/tokens/temperature.

## Consequence
New vendors are provider implementations, never core changes.
