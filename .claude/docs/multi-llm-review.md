# Multi-LLM Code Reviews

For deep code review, security audit, or architecture review, use multiple LLMs:

## Quick Method
Use `/multi-review` - Unified multi-LLM review (includes all below)

## Manual Method (parallel)
- `codex exec -m gpt-5.3-codex` (CLI) - Code quality and reasoning
- `gemini -m gemini-3-flash-preview` (CLI) - Best free tier, outperforms 2.5 Pro
- `mcp__seq-server__sequentialthinking` (MCP) - Systematic reasoning

## CLI vs MCP Preference

**ALWAYS use CLI commands via Bash, NOT MCP tools:**
- `codex exec` via Bash (NOT `mcp__codex-native__codex`)
- `gemini` via Bash (NOT `mcp__gemini-cli__ask-gemini`)

CLIs are faster, have consistent output formatting, and match skill documentation.
MCP tools have different parameter names and behavior.

## Model Selection Guidelines

### Gemini
- **gemini-3-flash-preview**: Best free tier model (20 req/day) - try this first
- **gemini-2.5-flash**: Fallback when gemini-3-flash quota exhausted (1000 req/day)
- **gemini-2.5-pro**: Deep architectural analysis (25 req/day) - use sparingly
- **Flags**: `changeMode: true` for structured edits, `sandbox: true` for risky operations

**Quota fallback**: If gemini-3-flash-preview returns "quota exceeded", retry with gemini-2.5-flash

### Codex (Enterprise Key)
- **gpt-5.3-codex**: Preferred model - try this first
- **gpt-5.2-codex**: Fallback when gpt-5.3-codex is not available on API key

### Claude Code (Handle Directly)
- File operations (Read, Write, Edit, Glob, Grep)
- Git operations, Bash commands
- TodoWrite planning, tool orchestration
- Quick analysis of 1-3 files

## CLI Commands

### Codex (with fallback)
```bash
# Try first (preferred)
codex exec --skip-git-repo-check -m gpt-5.3-codex "prompt"

# Fallback if model not available
codex exec --skip-git-repo-check -m gpt-5.2-codex "prompt"
```

### Gemini (with fallback)
```bash
# Try first (best quality, limited quota)
gemini -o text -m gemini-3-flash-preview "prompt"

# Fallback if quota exceeded
gemini -o text -m gemini-2.5-flash "prompt"
```

## Notes
- Codex fallback: gpt-5.3-codex → gpt-5.2-codex (gpt-5.3 not yet available on all API keys as of 2026-02-05)
- Gemini quota tiers: gemini-3-flash-preview (20/day) → gemini-2.5-flash (1000/day) → gemini-2.5-pro (25/day)
- **On quota error**: If command fails with "quota exceeded", retry with next model in fallback chain
