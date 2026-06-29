---
name: multi-review
description: Multi-LLM review for code AND architecture decisions. Auto-detects mode or use --arch flag.
---

# Multi-LLM Review

Get consensus from multiple LLMs on code quality OR architectural decisions.

## Usage

```
/multi-review <file-path>                    # Code review (auto-detected)
/multi-review <question or decision>         # Architecture review (auto-detected)
/multi-review --arch <decision to evaluate>  # Force architecture mode
/multi-review --code <file-path>             # Force code review mode
```

## Auto-Detection

| Input | Mode |
|-------|------|
| File path (`.go`, `.ts`, `.tsx`, etc.) | Code review |
| Question ("Should we...", "What's the best...") | Architecture |
| Plan or design doc | Architecture |
| `--arch` flag | Architecture (forced) |
| `--code` flag | Code review (forced) |

## Models

See `.claude/docs/multi-llm-review.md` for model selection, quota limits, and fallback logic.

## Workflow

### Step 1: Detect Mode & Gather Context

**Code Review Mode:**
- Read the target file(s)
- Identify the language and framework
- Note surrounding context if needed

**Architecture Mode:**
- Frame the decision clearly
- Identify options and trade-offs
- Read relevant code for context

### Step 2: Run Reviews in Parallel

Launch **all models from `.claude/docs/multi-llm-review.md`** simultaneously (single message, multiple tool calls). This includes Codex, Gemini, AND seq-server — do NOT skip any.

### Step 3: Synthesize Consensus

Analyze all responses and identify:
1. **Consensus** - What 2+ models agree on (high confidence)
2. **Unique findings** - Single-model insights (verify manually)
3. **Disagreements** - Where models differ (investigate)

## Output Format

### Code Review Output

```markdown
## Multi-LLM Code Review: [filename]

### Consensus Issues (High Confidence)
| Issue | Found By | Severity |
|-------|----------|----------|
| [description] | o3-pro, gpt-5.2 | HIGH/MED/LOW |

### Additional Findings
- [Issue] (found by [model]) - [verify/consider]

### Recommendations
1. [Priority fix]
2. [Secondary fix]

### Confidence: HIGH/MEDIUM/LOW
```

### Architecture Review Output

```markdown
## Multi-LLM Architecture Review: [decision]

### Model Recommendations

| Model | Recommendation | Key Reasoning |
|-------|----------------|---------------|
| o3-pro | [option] | [rationale] |
| gpt-5.2-codex | [option] | [rationale] |
| Gemini | [option] | [rationale] |

### Consensus Points
- [What all models agree on]

### Disagreements
- [Where models differ and why]

### Final Recommendation
[Synthesized decision with implementation guidance]

### Risks & Mitigations
- Risk: [issue] → Mitigation: [solution]

### Confidence: HIGH/MEDIUM/LOW
```

## Prompt Templates

### Code Review Prompt
```
Review this [language] code for:
1. Bugs and logic errors
2. Security vulnerabilities
3. Performance issues
4. Code clarity and maintainability
5. Adherence to best practices

<code>
[paste code]
</code>

Provide specific, actionable findings with line numbers.
```

### Architecture Prompt
```
Evaluate this architectural decision:

Context: [situation summary]
Options: [list options being considered]
Constraints: [requirements, limitations]

Questions:
1. Which option do you recommend and why?
2. What are the trade-offs?
3. What risks should we mitigate?

Provide a concrete recommendation with rationale.
```

## CLI Reference

See `.claude/docs/multi-llm-review.md` for CLI commands and quota fallback logic.

## Tips

- **Parallel execution**: Run all model calls in single message for speed
- **Large files**: Chunk files >500 lines or summarize first
- **Model details**: See `.claude/docs/multi-llm-review.md` for models, quotas, and fallback logic

## Examples

```bash
# Review a Go file
/multi-review server/app/item_core.go

# Review architecture decision
/multi-review Should we use Redis or PostgreSQL for caching?

# Force architecture mode on a plan doc
/multi-review --arch implementation-plans/new-feature.md

# Review with specific focus
/multi-review server/api4/page_api.go - Focus on security and input validation
```
