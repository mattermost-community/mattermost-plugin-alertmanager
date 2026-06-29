---
name: review-code
description: Comprehensive code review via specialized agents + multi-LLM review. Works on local changes or GitHub PRs.
---

# Review Code

Comprehensive code review using **specialized agents** AND **multi-LLM review**. Catches bugs, security issues, and pattern violations.

Works on:
- **Branch changes since last PR** (default) - commits on the current branch vs `master`, plus uncommitted working-tree changes
- **Uncommitted only** (with `--uncommitted` flag) - working-tree changes only
- **GitHub PRs** (with `--pr` flag)

> **Taxonomy**:
> - `/create-plan` → `/create-code` → `/review-code`
> - This is the final quality gate before committing

**Run `/lint` after** - This skill finds semantic issues; lint afterward to clean up formatting.

**Related**:
- `/review-plan` - Review plans (different agents, different LLM models)
- `/create-code` - Implement code from plan
- `/lint` - Linting and formatting (run first)

## Two-Phase Review

This skill combines:
1. **Specialized Code Agents** - Claude agents for pattern-specific checks
2. **Multi-LLM Review** - External models for diverse perspectives

| Phase | Models/Agents | Strength |
|-------|---------------|----------|
| **Phase 1: Agents** | Claude agents (race-condition-finder, etc.) | Pattern detection, domain-specific |
| **Phase 2: Multi-LLM** | Codex, Gemini, seq-server (see `multi-llm-review.md`) | Code quality, diverse perspectives |

## Usage

```
/review-code                              # All changes on current branch since master (default)
/review-code --uncommitted                # Uncommitted working-tree changes only
/review-code <file-or-directory>          # Review specific path
/review-code --pr 123                     # Review GitHub PR #123
/review-code --pr 123 --quick             # Quick PR review (Tier 1 only)
/review-code --quick                      # Tier 1 agents only (no multi-LLM)
/review-code --security                   # Security-focused review
/review-code --full                       # All tiers + multi-LLM (most thorough)
/review-code --agents-only                # Skip multi-LLM review
/review-code --llm-only                   # Skip agents, multi-LLM only
```

## Multi-LLM Models (Code Review)

See `.claude/docs/multi-llm-review.md` for model selection, CLI commands, quota limits, and fallback logic. All three tools (Codex, Gemini, seq-server) MUST be used.

## What It Does

```
/review-code [--pr <number>]
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 1: IDENTIFY CHANGES               │
│  - Default: git diff master...HEAD +    │
│    working tree (branch since last PR)  │
│  - On master: falls back to working     │
│    tree (uncommitted) only              │
│  - --uncommitted: git diff (working     │
│    tree only)                           │
│  - --pr: gh pr diff <number>            │
│  - Detect languages (Go, TS, etc.)      │
│  - Identify domains (API, store, UI)    │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 2: RUN CODE AGENTS (Claude)       │
│                                         │
│  Tier 1 (always):                       │
│  - race-condition-finder                │
│  - simplicity-reviewer                  │
│  - pattern-reviewer                     │
│  - error-handling-reviewer              │
│                                         │
│  Tier 2 (security):                     │
│  - xss-reviewer                         │
│  - validation-reviewer                  │
│  - permission-auditor                   │
│                                         │
│  Tier 3+ (domain-specific):             │
│  - Based on files changed               │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 3: MULTI-LLM REVIEW (in parallel) │
│                                         │
│  All models from multi-llm-review.md    │
│  (Codex + Gemini + seq-server)          │
│                                         │
│  Focus: Code quality, edge cases,       │
│  security, performance                  │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 4: SYNTHESIZE FINDINGS            │
│  - Merge agent + LLM findings           │
│  - Prioritize by severity               │
│  - Apply 80/20 filter                   │
│  - Only 2+ model agreement = MUST FIX   │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  OUTPUT: Review Report                  │
│  - MUST FIX (blockers)                  │
│  - SHOULD FIX (quality)                 │
│  - Passed checks                        │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 5: OFFER TO FIX                   │
│  Ask user via AskUserQuestion:          │
│  - "Fix all" (MUST FIX + SHOULD FIX)   │
│  - "MUST FIX only"                      │
│  - "No, just the review"               │
│  Then apply chosen fixes to the code.   │
└─────────────────────────────────────────┘
```

## Multi-LLM Review Commands

Run **all models from `.claude/docs/multi-llm-review.md`** in parallel (single message, multiple tool calls). This includes Codex, Gemini, AND seq-server — do NOT skip any.

## Code Review Prompt Template

```
Review this code for bugs, security issues, and quality.

## Code Changes
<code>
[paste git diff or file contents]
</code>

## CRITICAL: Apply 80/20 Thinking

**A MUST FIX blocker is ONLY:**
- Bug that will cause runtime failure
- Security vulnerability (injection, auth bypass, XSS)
- Data integrity risk (corruption, loss)
- Race condition / concurrency bug
- Missing error handling that crashes

**NOT a blocker (SHOULD FIX or SKIP):**
- Style issues, naming preferences
- Minor optimizations
- Missing comments/docs
- "Best practices" that don't affect correctness

## Evaluate

1. **Correctness**: Will this code work as intended?
2. **Security**: Any vulnerabilities?
3. **Edge Cases**: Null checks, error handling, boundary conditions?
4. **Performance**: Any obvious inefficiencies?
5. **Patterns**: Does it follow established codebase patterns?
6. **Diagnostics**: For Go handler changes — do user-initiated actions and error paths call `PostDiagnostic`? (See `server/CLAUDE.md` → Diagnostics Channel)

## Output

1. **MUST FIX** (0-3 max): What breaks? File:line? Fix?
2. **SHOULD FIX** (0-5): Quality improvements
3. **VERDICT**: APPROVED / NEEDS WORK
```

## Agent Tiers

### Tier 1: Core (Always Run)

| Agent | Catches |
|-------|---------|
| `race-condition-finder` | Concurrency bugs, TOCTOU, data races |
| `simplicity-reviewer` | Over-engineering, YAGNI violations |
| `error-handling-reviewer` | Missing error checks, swallowed errors |

### Tier 2: Security

| Agent | Catches |
|-------|---------|
| `xss-reviewer` | XSS vulnerabilities in frontend |
| `validation-reviewer` | Missing input validation |
| `hardcoded-values-reviewer` | Secrets, magic numbers, config in code |

### Tier 3: Backend (Go files)

| Agent | Catches |
|-------|---------|
| `go-pro` | Go patterns, error handling, concurrency |
| `concurrent-go-reviewer` | Go concurrency safety |
| `postgres-expert` | Database query optimization |

### Tier 4: Frontend (TS/TSX files)

| Agent | Catches |
|-------|---------|
| `react-pro` | React patterns, hooks, performance |
| `typescript-pro` | TypeScript patterns, type safety |

### Tier 5: Testing

| Agent | Catches |
|-------|---------|
| `test-coverage-reviewer` | Missing test coverage for new code |
| `test-unit-expert` | Test quality, assertions, mocking |
| `playwright-patterns-reviewer` | E2E test patterns, flaky tests |
| `production-validator` | Mock/stub code that should be real implementations |

### Tier 6: Advanced/Expert (Optional)

| Agent | When |
|-------|------|
| `websocket-expert` | WebSocket patterns |
| `owasp-security` | OWASP vulnerability checks |
| `accessibility-guardian` | Accessibility compliance |

### Tier 7: Quality/Maintenance (Optional)

| Agent | When |
|-------|------|
| `duplication-reviewer` | Code duplication detection |
| `comment-analyzer` | Comment quality analysis |

### DO NOT Use for Code Review

These are **PLAN agents** - use them in `/create-plan` and `/review-plan`:
- `design-flaw-finder` - Reviews design, not implementation
- `api-contract-reviewer` - Reviews API design, not handler code
- `database-architecture-reviewer` - Reviews schema design, not queries
- `ux-design-reviewer` - Reviews UX design, not components
- `system-design-reviewer` - Reviews architecture, not code

## Full Agent Reference

For complete agent listing (~140 agents), see `.claude/agents/AGENT_REGISTRY.md`.

## Agent Selection Logic

```python
# Pseudo-logic for agent selection
agents = []

# Tier 1: Always run (MUST RUN)
agents.extend([
    "race-condition-finder",
    "simplicity-reviewer",
    "error-handling-reviewer"
])

# Tier 2: Security (always for production code)
if not test_files_only:
    agents.extend([
        "hardcoded-values-reviewer",
        "owasp-security"
    ])

# Tier 3: Backend (Go files)
if has_go_files:
    agents.append("go-pro")
    agents.append("concurrent-go-reviewer")
    if has_db_changes:
        agents.append("postgres-expert")

# Tier 4: Frontend (TS/TSX files)
if has_ts_files:
    agents.append("react-pro")
    agents.append("typescript-pro")

# Tier 5: Testing
if has_test_files:
    if has_go_tests:
        agents.append("test-unit-expert")
    if has_e2e_tests:
        agents.append("playwright-patterns-reviewer")
```

## Output Format

```markdown
## Code Review: [files reviewed]

### MUST FIX (Blockers)
| Issue | File:Line | Agent | Fix |
|-------|-----------|-------|-----|
| Race condition in cache access | `cache.go:45` | race-condition-finder | Add mutex |
| Missing permission check | `api.go:123` | permission-auditor | Add HasPermission call |

### SHOULD FIX (Quality)
| Issue | File:Line | Agent | Recommendation |
|-------|-----------|-------|----------------|
| Overly complex function | `utils.go:89` | simplicity-reviewer | Extract helper |

### Passed Checks
- ✅ No XSS vulnerabilities
- ✅ Input validation present
- ✅ Error handling complete
- ✅ Tests cover new code

### Agent Summary
| Agent | Verdict | Findings |
|-------|---------|----------|
| race-condition-finder | ⚠️ ISSUES | 1 race condition |
| simplicity-reviewer | ✅ PASS | - |
| xss-reviewer | ✅ PASS | - |
| permission-auditor | ⚠️ ISSUES | 1 missing check |

---

### Verdict: NEEDS WORK / APPROVED

Fix MUST FIX items before committing.
```

## Offer to Fix

After presenting the review report, **always ask the user if they would like the findings fixed**. Use `AskUserQuestion` to prompt:

> "Would you like me to fix the issues found in the review?"

Options:
- **Fix all** — Apply MUST FIX and SHOULD FIX changes to the code
- **MUST FIX only** — Apply only blocker fixes
- **No, just the review** — Leave the code unchanged

If the user chooses to fix, apply the changes directly to the affected files using `Edit`, preserving existing code structure. After applying fixes, run `make check-style` to ensure formatting is correct.

**Note**: This step is skipped in `--pr` mode since you cannot edit PR code directly. Instead, suggest fixes as PR comments.

## Flags

| Flag | Effect |
|------|--------|
| `--pr <number>` | Review GitHub PR instead of local branch changes |
| `--uncommitted` | Review only uncommitted working-tree changes (old default) |
| `--quick` | Tier 1 agents only, no multi-LLM (fastest) |
| `--security` | Focus on Tier 2 security agents + LLM security review |
| `--full` | All tiers + multi-LLM (most thorough) |
| `--agents-only` | Skip multi-LLM review (Claude agents only) |
| `--llm-only` | Skip agents, run multi-LLM review only |

## Examples

```bash
# Full review of branch changes since last PR (agents + multi-LLM) - RECOMMENDED
/review-code

# Review only uncommitted working-tree changes
/review-code --uncommitted

# Review specific file
/review-code server/app/item_core.go

# Review a GitHub PR
/review-code --pr 123

# Quick PR review (Tier 1 agents only)
/review-code --pr 123 --quick

# Security-focused PR review
/review-code --pr 123 --security

# Quick review (Tier 1 agents only, no LLM)
/review-code --quick

# Security-focused review
/review-code --security

# Full review (all tiers + multi-LLM)
/review-code --full

# Agents only (skip external LLMs)
/review-code --agents-only

# Multi-LLM only (skip agents)
/review-code --llm-only
```

## CLI Reference

See `.claude/docs/multi-llm-review.md` for CLI commands and quota fallback logic.

## When to Use

| Scenario | Command | Skip review |
|----------|---------|-------------|
| After `/create-code` | `/review-code` | |
| Before opening a PR | `/review-code` | |
| Before committing WIP | `/review-code --uncommitted` | |
| Reviewing a PR | `/review-code --pr 123` | |
| Security-sensitive code | `/review-code --security` | |
| Quick PR check | `/review-code --pr 123 --quick` | |
| Tiny typo fix | | ✅ |
| Documentation only | | ✅ |

## Integration with Workflow

```
/create-plan "feature"     # Create plan
    │
    ▼
/create-code plan.md       # Implement
    │
    ▼
/review-code               # Agent review  ← THIS SKILL
    │
    ▼
Fix MUST FIX items
    │
    ▼
/lint                      # Final formatting
    │
    ▼
Commit changes
```

## Tips

- **Run before every commit** - Catch issues early
- **Use `--quick` for WIP** - Full review before PR
- **Fix MUST FIX immediately** - They're blockers for a reason
- **SHOULD FIX can wait** - Address in follow-up if time-constrained
- **Trust multi-model consensus** - 2+ models agreeing = real issue
- **Parallel execution** - Run all LLM calls in single message
- **Use `--agents-only` for speed** - When external LLMs are slow/unavailable
- **Be skeptical of single-model findings** - Could be false positive
