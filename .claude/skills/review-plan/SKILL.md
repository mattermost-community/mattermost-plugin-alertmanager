---
name: review-plan
description: Multi-LLM validation for implementation plans. Supports --spec mode for requirements validation and default mode for technical feasibility. Focuses on 80/20 high-impact issues.
---

# Plan Review

Validate implementation plans using multiple LLMs before writing code. **Focuses on 80/20 analysis** - identifies the critical few issues that matter vs the trivial many that don't.

**Two modes:**
- **Default**: Technical feasibility review (can this be built?)
- **`--spec`**: Requirements validation review (are we building the right thing?)

**Related**:
- Use `/create-plan` to generate plans
- Use `/multi-review` for code/architecture decisions

## Usage

```
/review-plan <plan-file>                           # Technical review (default)
/review-plan <plan-file> --spec                    # Requirements validation
/review-plan <plan-file> --req <requirements>      # Compare against original request
/review-plan <plan-file> --context <relevant-files> # Include codebase context
/review-plan <plan-file> --quick                   # Fast single-model check (Gemini only)
```

## Two Review Modes

### Default Mode: Technical Feasibility

**Question**: Can this plan be implemented?

| Validates | Questions |
|-----------|-----------|
| Feasibility | Do required APIs exist? Dependencies available? |
| Risks | Data integrity? Security? Breaking changes? |
| Ambiguity | Can developer implement without guessing? |
| Scope | Over-engineered? What can be cut? |

### Spec Mode (`--spec`): Requirements Validation

**Question**: Are we building the right thing?

| Validates | Questions |
|-----------|-----------|
| Completeness | All user requirements captured? |
| Clarity | Requirements testable and unambiguous? |
| Scope | Out-of-scope clearly defined? |
| Acceptance | Criteria specific and verifiable? |
| Stakeholder | Could non-technical person approve this? |

**Use `--spec` FIRST**, then default mode:
```bash
/review-plan plan.md --spec    # Step 1: Requirements OK?
/review-plan plan.md           # Step 2: Technical OK?
```

## Core Philosophy: 80/20 Prioritization

**Not all issues are equal.** This skill explicitly separates:

| Category | Criteria | Action |
|----------|----------|--------|
| **MUST FIX** | Blocks implementation, causes data loss, security risk, or will definitely fail | Fix before coding |
| **SHOULD FIX** | Improves quality but plan works without it | Address if time permits |
| **DEFER** | Valid concern but not for this iteration | Track for future |
| **SKIP** | Over-engineering, premature optimization, or gold-plating | Ignore |

### Technical Mode Blockers

**A blocker IS:**
- Missing critical step that makes implementation impossible
- Data integrity risk (orphaned records, corruption)
- Security vulnerability (injection, auth bypass)
- Undefined contract that blocks API integration
- Ambiguity that requires guessing during implementation

**A blocker is NOT:**
- Missing documentation
- Imperfect error messages
- Edge cases that affect <1% of users
- "Best practice" that isn't required

### Spec Mode Blockers

**A blocker IS:**
- Missing user requirement from original request
- Acceptance criteria that can't be tested
- No "out of scope" section (scope creep risk)
- Contradictory requirements
- Requirements that stakeholder hasn't agreed to

**A blocker is NOT:**
- Missing nice-to-have features
- Imperfect wording
- Missing implementation details (that's for technical review)

## Models

See `.claude/docs/multi-llm-review.md` for model selection, quota limits, and fallback logic.

## Plan Agents (Optional)

After multi-LLM review, run domain-specific agents for deeper analysis.

### MUST RUN (Every Plan)

| Agent | Catches |
|-------|---------|
| `design-flaw-finder` | Logical flaws, missing steps, impossible sequences |
| `simplicity-reviewer` | Over-engineering, YAGNI violations |

### Architecture (Complex Plans)

| Agent | When to Use |
|-------|-------------|
| `system-design-reviewer` | Multi-component features |
| `separation-of-concerns-reviewer` | Layer violations |
| `client-server-alignment` | Frontend+backend changes |
| `permission-design-auditor` | Permission model changes |
| `type-design-analyzer` | New type hierarchies |

### Domain-Specific

| Agent | When to Use |
|-------|-------------|
| `api-contract-reviewer` | API endpoint changes |
| `rest-api-expert` | REST API design |
| `database-architecture-reviewer` | Schema changes |
| `ux-design-reviewer` | UI/UX changes |
| `edge-case-ux-analyst` | Error/empty/loading states |
| `confluence-alignment-reviewer` | Document/pages features |
| `threat-modeler` | Security-sensitive features |
| `arch-system-design` | Large-scale architecture |
| `repo-architect` | Repository structure |

**Note**: These are PLAN agents. Do NOT use code agents (go-backend, xss-reviewer, etc.) on plans.

## Full Agent Reference

For complete agent listing (~140 agents), see `.claude/agents/AGENT_REGISTRY.md`.

## Workflow

### Step 1: Gather Context

1. Read the plan file
2. If `--req` provided, read original requirements
3. If `--context` provided, read relevant codebase files
4. Detect mode (`--spec` or default)

### Step 2: Run Reviews in Parallel

Launch **all models from `.claude/docs/multi-llm-review.md`** simultaneously (single message, multiple tool calls). This includes Codex, Gemini, AND seq-server — do NOT skip any.

**Quick mode (`--quick`)**: Use only Gemini.

### Step 3: Synthesize with 80/20 Filter

1. **MUST FIX** - Only issues where 2+ models agree AND meets blocker criteria
2. **SHOULD FIX** - Single-model findings that are valid but not blocking
3. **DEFER** - Valid concerns for future iterations
4. **SKIP** - Reject over-engineering suggestions

**Be ruthless.** Most "warnings" from LLMs are nice-to-haves.

### Step 4: Offer to Update the Plan

After presenting the review results, **always ask the user if they would like to update the plan file** with the suggested fixes. Use `AskUserQuestion` to prompt:

> "Would you like me to update `<plan-file>` with the MUST FIX and SHOULD FIX changes?"

Where `<plan-file>` is the path passed as the first argument (e.g., `implementation-plans/2026-02-22-1200-add-new-feature.md`).

Options:
- **Yes, apply all** — Apply MUST FIX and SHOULD FIX changes to the plan file
- **MUST FIX only** — Apply only blocker fixes to the plan file
- **No, just the review** — Leave the plan file unchanged

If the user chooses to update, edit the plan file in-place, preserving its structure and only modifying the sections affected by the findings.

## Prompt Templates

### Technical Review Prompt (Default)

```
Review this implementation plan for readiness to code.

## The Plan
<plan>
[paste plan content]
</plan>

## Original Requirements (if provided)
<requirements>
[paste requirements]
</requirements>

## CRITICAL: Apply 80/20 Thinking

Focus on the 20% of issues that cause 80% of problems.

**A MUST FIX blocker is ONLY:**
- Missing step that makes implementation impossible
- Data integrity risk (orphaned records, corruption, loss)
- Security vulnerability (injection, auth bypass, SSRF)
- Undefined contract that blocks integration
- Ambiguity requiring guesswork during implementation

**NOT a blocker (put in DEFER or SKIP):**
- Missing docs, imperfect error messages
- Edge cases affecting <1% of users
- "Best practices" not strictly required
- Future-proofing for hypothetical scenarios

## Evaluate (priority order)

1. **Feasibility**: Do required APIs/functions exist?
2. **Risks**: Data integrity? Security? Breaking changes?
3. **Ambiguity**: Can developer implement without guessing?
4. **Scope**: What can be cut for MVP?
5. **Diagnostics**: If the plan adds user-initiated actions or error paths in Go handlers, does it include `PostDiagnostic` calls? (See `server/CLAUDE.md` → Diagnostics Channel)
6. **Slash command**: If the plan adds new admin-facing functionality, does it consider a `/template` subcommand? (See `server/CLAUDE.md` → Slash Commands)

## Output

1. **MUST FIX** (0-3 max): What breaks? How to fix?
2. **SHOULD FIX** (0-5): Why it matters, why not blocking
3. **DEFER**: Valid for later
4. **SKIP**: Over-engineering to reject
5. **VERDICT**: READY / NEEDS WORK / MAJOR REVISION
```

### Spec Review Prompt (`--spec` flag)

```
Review this plan's REQUIREMENTS for completeness and clarity.
DO NOT review technical implementation - only requirements.

## The Plan
<plan>
[paste plan content]
</plan>

## Original User Request (if provided)
<request>
[paste original request]
</request>

## CRITICAL: Requirements-Only Review

You are validating "are we building the right thing?" NOT "can we build it?"

**A MUST FIX blocker is ONLY:**
- User requirement from original request NOT captured in plan
- Acceptance criteria that cannot be tested/verified
- Missing "Out of Scope" section (scope creep risk)
- Contradictory or ambiguous requirements
- Unstated assumptions that could surprise stakeholders

**NOT a blocker (put in DEFER or SKIP):**
- Missing implementation details
- Technical approach concerns
- Nice-to-have features not in original request
- Imperfect wording that's still clear

## Evaluate

1. **Completeness**: Every requirement from original request captured?
2. **Testability**: Each requirement has verifiable acceptance criteria?
3. **Scope Boundaries**: "Out of Scope" section exists and is clear?
4. **Clarity**: Could a stakeholder approve without asking questions?
5. **Assumptions**: Are implicit assumptions made explicit?

## Output

1. **MUST FIX** (0-3 max): Missing/unclear requirements
2. **SHOULD FIX** (0-5): Improvements to clarity
3. **DEFER**: Nice-to-haves for future
4. **SKIP**: Scope creep suggestions to reject
5. **VERDICT**: READY / NEEDS WORK / MAJOR REVISION
```

## Output Format

```markdown
## Plan Review: [plan-name]
### Mode: Technical / Spec

### MUST FIX (Blockers)
| Issue | Found By | What Breaks | Fix |
|-------|----------|-------------|-----|
| [description] | o3-pro, gpt-5.2 | [why blocked] | [fix] |

*If empty: "None - plan is ready"*

### SHOULD FIX (Quality Improvements)
- [Issue] - [why it matters but isn't blocking]

### DEFER (Future Iterations)
- [Issue] - [why it can wait]

### SKIP (Rejected Suggestions)
- [Suggestion] - [why this is over-engineering/scope-creep]

### What's Good
- [Validated aspects]

---

### Verdict: READY / NEEDS WORK / MAJOR REVISION
### Confidence: HIGH/MEDIUM/LOW
```

## Verdict Criteria

| Verdict | Criteria |
|---------|----------|
| **READY** | 0 MUST FIX items. Proceed. |
| **NEEDS WORK** | 1-2 MUST FIX items. Quick fixes needed. |
| **MAJOR REVISION** | 3+ MUST FIX or fundamental flaw. Rethink. |

## CLI Reference

See `.claude/docs/multi-llm-review.md` for CLI commands and quota fallback logic.

## Examples

```bash
# Requirements validation first
/review-plan implementation-plans/feature.md --spec

# Then technical review
/review-plan implementation-plans/feature.md

# With original user request for comparison
/review-plan implementation-plans/feature.md --spec --req "User asked for X with Y"

# Quick check
/review-plan implementation-plans/small-fix.md --quick
```

## Integration with Workflow

```
User request
    ↓
/create-plan                    # Generate structured plan
    ↓
/review-plan plan.md --spec     # Validate requirements ← NEW
    ↓
/review-plan plan.md            # Validate technical approach
    ↓
Fix MUST FIX items
    ↓
User approval
    ↓
Implementation
    ↓
/review-code                    # Code review
```

## Tips

- **Run `--spec` before default** - No point validating technical if requirements are wrong
- **Be skeptical of LLM "blockers"** - Most are actually SHOULD FIX or DEFER
- **Parallel execution**: Run all model calls in single message
- **Quick check**: Use `--quick` for initial pass
- **Trust judgment**: If it feels like over-engineering, it probably is
