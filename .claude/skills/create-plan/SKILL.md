---
name: create-plan
description: Complete plan workflow - creates structured plan, runs multi-LLM review + domain agents, iterates on blockers. Outputs validated plan ready for approval.
user-invocable: true
---

# Create Plan

**Complete plan creation workflow** - generates a structured plan, validates it with multi-LLM review AND domain-specific agents, and iterates until ready. Outputs a validated plan ready for user approval.

This skill combines:
1. Structured plan generation (template for consistency)
2. Multi-LLM review (spec + technical)
3. **Plan-appropriate agent review** (MUST RUN + domain-specific)
4. Iteration on blockers

**Related**:
- Use `/review-plan` separately if you already have a plan
- Use `/multi-review` for ad-hoc architecture decisions

## CRITICAL: Plan Agents vs Code Agents

**Plans are NOT code.** Most agents in AGENT_REGISTRY.md are designed for code review, not plan review.

| Agent Type | Purpose | When to Use |
|------------|---------|-------------|
| **Plan Agents** | Review design, architecture, contracts | During `/create-plan` |
| **Code Agents** | Review implementation, bugs, patterns | During `/review-code` (after coding) |

### Plan Agents (review designs)

#### MUST RUN (Every Plan)

| Agent | Catches |
|-------|---------|
| `design-flaw-finder` | Logical flaws, missing steps, impossible sequences |
| `simplicity-reviewer` | Over-engineering, YAGNI violations, premature abstraction |

#### Architecture & Design (Complex Plans)

| Agent | When to Use |
|-------|-------------|
| `system-design-reviewer` | Multi-component features, service boundaries |
| `separation-of-concerns-reviewer` | Layer violations, mixed responsibilities |
| `client-server-alignment` | Frontend+backend changes, API contracts |
| `type-design-analyzer` | New type hierarchies, model changes |

#### Domain-Specific (Based on Plan Content)

| Agent | When to Use |
|-------|-------------|
| `api-contract-reviewer` | Plans with API endpoint changes |
| `rest-api-expert` | REST API design, versioning |
| `database-architecture-reviewer` | Schema changes, migrations, indexes |
| `ux-design-reviewer` | UI/UX changes, user flows |
| `edge-case-ux-analyst` | Error/empty/loading states in UI |
| `threat-modeler` | Security-sensitive features |
| `arch-system-design` | Large-scale system architecture |
| `repo-architect` | Repository structure changes |
| `prd-generator` | Generate PRD from plan (post-plan) |

### Code Agents (DO NOT use for plans)

These review **implementation code**, not plans:
- `race-condition-finder`, `error-handling-reviewer`
- `xss-reviewer`, `validation-reviewer`
- `concurrent-go-reviewer`, `go-pro`
- `react-pro`, `typescript-pro`
- `test-unit-expert`, `playwright-patterns-reviewer`

**Save code agents for `/review-code` after implementation.**

For complete agent listing (~140 agents), see `.claude/agents/AGENT_REGISTRY.md`.

## Usage

```
/create-plan <request>                    # Full workflow: create + review + agents + iterate
/create-plan <request> --draft            # Just create draft, skip review
/create-plan <request> --no-agents        # Multi-LLM only, skip domain agents
/create-plan <request> --output <file>    # Save to specific file
/create-plan <request> --minimal          # Lightweight plan for small features
/create-plan <request> --generic          # Force generic template
```

## MANDATORY: Save Plans to implementation-plans/

**⚠️ YOU MUST WRITE THE PLAN FILE BEFORE EXITING PLAN MODE. NO EXCEPTIONS. ⚠️**

If you skip this step, the plan is lost and the user cannot reference it later. This is the #1 most common failure mode — do NOT forget.

**ALWAYS save the final plan to:**
```
<project-root>/implementation-plans/YY-MM-DD-NN-<feature-name>.md
```

**Naming convention:**
- Prefix with today's date in `YY-MM-DD-` format
- Follow with a zero-padded sequential counter `NN` (01, 02, 03…)
- Before creating a file, glob for `implementation-plans/YY-MM-DD-*` and pick the next number
- Use kebab-case for the feature name after the counter
- Examples: `26-02-15-01-page-tree-reordering.md`, `26-03-01-02-oauth2-support.md`

**CRITICAL WORKFLOW (must follow in order):**

1. **Finalize plan** - Complete all reviews and iterations
2. **WRITE THE FILE** - Use the `Write` tool to save the complete plan content to `implementation-plans/YY-MM-DD-NN-<name>.md`. You MUST call the Write tool — thinking about it or planning to do it is NOT the same as doing it.
3. **VERIFY the file exists** - Use `Read` to confirm the file was written successfully
4. **Report saved location** - Tell user: "Plan saved to `implementation-plans/YY-MM-DD-NN-<name>.md`"
5. **Exit plan mode** - Use ExitPlanMode tool
6. **User approves** - User reviews and approves the plan

**SELF-CHECK before ExitPlanMode:**
- ❓ "Did I call the Write tool to save the plan?" — If NO, go back and do it NOW
- ❓ "Can I see the Write tool call in my recent actions?" — If NO, the file was NOT written

**Example:**
```
/create-plan "Add page reordering"
→ Write(implementation-plans/26-02-15-01-page-reordering.md, <full plan content>)
→ Read(implementation-plans/26-02-15-01-page-reordering.md)  # verify
→ "Plan saved to implementation-plans/26-02-15-01-page-reordering.md"
→ ExitPlanMode
```

This ensures all plans are discoverable and version-controlled with the codebase.

---

## MANDATORY: Implementation Reads from Saved File

**When implementing a plan, ALWAYS read from the saved file - never use inline/conversation context.**

After user approves the plan:

1. **Read the saved plan file**: `Read(implementation-plans/YY-MM-DD-NN-<name>.md)`
2. **Confirm source**: "Implementing from `implementation-plans/YY-MM-DD-NN-<name>.md`"
3. **Follow the plan** - Execute tasks from the file

**Why this matters:**
- Plan file is the source of truth (version-controlled)
- Conversation context may drift from what was saved
- User can edit the plan file before implementation
- Multiple sessions can reference the same plan

**Anti-pattern (DO NOT DO):**
```
❌ User: "Implement the plan"
❌ Claude: [Uses plan text from conversation memory]
```

**Correct pattern:**
```
✅ User: "Implement the plan"
✅ Claude: "Let me read the saved plan first."
✅ Claude: Read(implementation-plans/26-02-15-01-page-reordering.md)
✅ Claude: "Implementing from implementation-plans/26-02-15-01-page-reordering.md"
```

## What It Does

```
/create-plan "Add OAuth2 support"
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 1: RESEARCH                       │
│  - Explore codebase for similar features│
│  - Identify patterns to follow          │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 2: DRAFT PLAN                     │
│  - Use template for consistency         │
│  - Fill in all sections                 │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 3: MULTI-LLM REVIEW               │
│  - Spec review (requirements)           │
│  - Technical review (feasibility)       │
│  - 80/20 filter for blockers            │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 4: AGENT REVIEW                   │
│                                         │
│  MUST RUN (every plan):                 │
│  - design-flaw-finder                   │
│  - simplicity-reviewer                  │
│                                         │
│  OPTIONAL (based on domain):            │
│  - api-contract-reviewer (if API)       │
│  - database-architecture-reviewer (DB)  │
│  - ux-design-reviewer (if UI)           │
│  - threat-modeler (security-sensitive)  │
│  - rest-api-expert (if REST API)        │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 5: ITERATE (if needed)            │
│  - Fix MUST FIX blockers in plan        │
│  - Max 2 iterations                     │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 6: WRITE FILE (MANDATORY!)        │
│  - Call Write tool NOW, not later       │
│  - Write to implementation-plans/       │
│  - Call Read to verify file exists      │
│  - Tell user the saved location         │
│  ⚠️ DO NOT SKIP OR DEFER THIS STEP ⚠️  │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 7: EXIT PLAN MODE                 │
│  - STOP: Did you call Write in Step 6?  │
│  - If NO → go back and write the file   │
│  - If YES → Call ExitPlanMode tool      │
│  - User approves or requests changes    │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 8: IMPLEMENTATION (after approve) │
│  - READ from saved file (not memory!)   │
│  - "Implementing from <file>"           │
│  - Execute tasks from file              │
└─────────────────────────────────────────┘
```

## Agent Selection Logic

### MUST RUN Agents (Every Plan)

These catch fundamental issues in ANY plan:

```bash
# 1. Design flaws
Task(subagent_type="design-flaw-finder", prompt="Review this plan for logical flaws: [plan]")

# 2. Over-engineering
Task(subagent_type="simplicity-reviewer", prompt="Review this plan for YAGNI violations: [plan]")
```

### Architecture Agents (Complex Plans)

For plans with multiple components or layer changes:

```bash
# Multi-component features
Task(subagent_type="system-design-reviewer", prompt="Review architecture: [plan]")

# Frontend+backend changes
Task(subagent_type="client-server-alignment", prompt="Check alignment: [plan]")

# Layer responsibility
Task(subagent_type="separation-of-concerns-reviewer", prompt="Check layers: [plan]")
```

### OPTIONAL Agents (Domain-Based)

Detect plan domain and run relevant agents:

| Plan Contains | Agents to Run |
|---------------|---------------|
| API endpoints | `api-contract-reviewer`, `rest-api-expert` |
| Database changes | `database-architecture-reviewer` |
| UI/UX changes | `ux-design-reviewer`, `edge-case-ux-analyst` |
| New types/models | `type-design-analyzer` |
| Security-sensitive | `threat-modeler` |

### Domain Detection

Scan plan for keywords to determine domain:

```python
# Pseudo-logic for agent selection
domains = []

if plan contains ["API", "endpoint", "REST", "/api/v4"]:
    domains.append("api")

if plan contains ["database", "schema", "migration", "table", "index"]:
    domains.append("database")

if plan contains ["UI", "component", "modal", "button", "user interface"]:
    domains.append("ui")

if plan contains ["permission", "role", "RBAC", "authorization", "access control"]:
    domains.append("permissions")

if plan contains ["type", "struct", "interface", "model"]:
    domains.append("types")

# Select agents based on domains
agents = ["design-flaw-finder", "simplicity-reviewer"]  # MUST RUN

if "api" in domains:
    agents.extend(["api-contract-reviewer", "rest-api-expert"])
if "database" in domains:
    agents.append("database-architecture-reviewer")
if "ui" in domains:
    agents.extend(["ux-design-reviewer", "edge-case-ux-analyst"])
if "permissions" in domains:
    agents.append("threat-modeler")
if "types" in domains:
    agents.append("type-design-analyzer")
```

## Frontend Pattern References (Auto-Include When Planning UI)

When plan contains UI/component/frontend keywords, include these patterns in the Technical Approach:

### Component Patterns
```markdown
## Component Structure
- Folder-by-feature: `my_component/`, `my_component.tsx`, `my_component.scss`, `my_component.test.tsx`
- Functional components with hooks (`useSelector`, `useDispatch`, `useCallback`, `useMemo`)
- Code splitting with `makeAsyncComponent` for heavy routes

## Styling Requirements
- Co-located SCSS files imported in component
- BEM naming: `.MyComponent`, `.MyComponent__title`
- CSS variables for colors: `var(--center-channel-color)`
- No `!important`

## Accessibility (MANDATORY)
- Semantic HTML (`<button>`, `<input>`) over `<div>` with roles
- Keyboard support for all interactive elements
- Use `GenericModal`, `Menu`, `WithTooltip` primitives

## Internationalization (MANDATORY)
- All UI strings via `<FormattedMessage>` or `useIntl()`
```

### Redux Patterns
```markdown
## Actions
- Return `{data}` on success, `{error}` on failure
- Use `bindClientFunc` for simple API calls
- Call Client4 ONLY from actions, never components
- Handle errors with `forceLogoutIfNecessary` + `logError`

## Selectors
- Memoize with `createSelector` when returning arrays/objects
- First param is selector name: `createSelector('getSomething', ...)`
- Use `makeGet*` factory for parameterized selectors
- Use `useMemo(makeGetX, [])` in components

## Reducers
- Immutable updates (spread operator)
- Always handle `UserTypes.LOGOUT_SUCCESS` to clear state
- `state.entities.*` for server data, `state.views.*` for UI
```

### Testing Patterns
```markdown
- RTL tests alongside components (`*.test.tsx`)
- Use `userEvent` and `getByRole` queries
- No snapshots - assert visible behavior
- Mock store with `renderWithRedux`
```

---

## Workflow Details

### Step 1: Research Codebase and Document Patterns

**CRITICAL**: Don't just research - **document findings** in the plan.

```bash
# Use Explore agent for thorough research
Task(subagent_type="Explore", prompt="Find how the codebase handles [feature area].
Look for similar features. Return:
1. File:line references for patterns to follow
2. Conventions used (state management, action patterns, etc.)
3. Anti-patterns to avoid")
```

**Research output should include:**
- 2-3 similar features in codebase with file references
- Patterns they follow (with file:line)
- Why this approach vs alternatives
- Current state gaps (what's missing today)

### Step 2: Draft Plan Using Template

```markdown
# [Feature Name]

## Overview
[1-2 sentence summary]

## Problem Statement
[What problem does this solve? Why is it needed now?]

## Current State
[How does it work today? What hooks/components exist?]

### Current Gaps
- [Gap 1: e.g., No cancellation support]
- [Gap 2: e.g., No progress tracking]

## Design Principles
| Pattern | Our Approach | Avoid | Reference |
|---------|-------------|--------|-----------|
| [e.g., Navigation] | Allow freely, never block | useBlocker, Prompt dialogs | - |
| [e.g., Async tracking] | Redux state `{[id]: data}` | Local component state | `marketplace.installing` |
| [e.g., Cancellation] | `AbortController.abort()` | Just ignore results | `file_upload.tsx:570` |
| [e.g., Notifications] | Toast component | Modal popup | `toast/toast.tsx` |

## Reference Patterns
Similar features to follow:
- `[file:line]` - [what pattern it demonstrates]
- `[file:line]` - [what pattern it demonstrates]

## Requirements
- [ ] Requirement 1
- [ ] Requirement 2

## Out of Scope
- NOT doing X
- NOT doing Y

## Technical Approach
[How we'll build it - aligned with design principles above]

## Decisions
| Question | Decision | Rationale |
|----------|----------|-----------|
| [e.g., State complexity?] | [Minimal] | [Only track active ops] |
| [e.g., Error structure?] | [Simple string] | [Typed errors add complexity] |

## Files to Modify
| File | Change |
|------|--------|

## Tasks
1. [ ] Task 1
2. [ ] Task 2

## Risks & Mitigations
| Risk | Mitigation |
|------|------------|

## UX Summary (for UI features)
| Scenario | Behavior |
|----------|----------|
| [User starts operation] | [Progress bar appears] |
| [User clicks cancel] | [Operation dismissed, server may complete] |
| [Operation completes] | [Toast with action link] |

## Testing Plan
**Unit**: [e.g., Reducer state transitions, selector logic]
**Integration**: [e.g., Cancel during progress, navigation away/back]
**E2E**: [e.g., Full flow: start → progress → complete → navigate]

## Acceptance Criteria
- [ ] Criterion 1
- [ ] Criterion 2

## Checklist
- [ ] **Diagnostics**: Does this feature have user-initiated actions or error paths that should post to the diagnostics channel? (See `server/CLAUDE.md` → Diagnostics Channel)
- [ ] **Slash command**: Should this feature be accessible via `/template <subcommand>`? (See `server/CLAUDE.md` → Slash Commands)
```

### Template Section Guidelines

| Section | When to Include |
|---------|-----------------|
| **Problem Statement** | Always - provides context |
| **Current State / Gaps** | Always - shows what exists |
| **Design Principles** | Always - key decisions and trade-offs |
| **Reference Patterns** | When following existing patterns |
| **Decisions** | When making non-obvious choices |
| **UX Summary** | UI/frontend features only |
| **Testing Plan** | Features requiring new tests |
| **Checklist** | Always - ensures diagnostics and slash commands are considered |
| **Phase Strategy** | Large features (>1 week) - see below |

### Phase Strategy (for large features)

For features spanning multiple phases, add after Problem Statement:

```markdown
## Phase Strategy

| Phase | Focus | Value |
|-------|-------|-------|
| **Phase 1** | Core MVP: [key deliverables] | **80% of value** |
| **Phase 2** | Polish: [edge cases, cleanup] | Robustness |
| **Phase 3** | Enhanced: [nice-to-haves] | Optional |
| **Phase 4** | Future: [out of scope for now] | Deferred |

### Phase 1 Scope (this plan)
[Details for Phase 1 only - keep focused]

### Deferred to Phase 2+
- [Item] - Phase 2
- [Item] - Phase 3
```

### Step 3: Multi-LLM Review

Run **all models from `.claude/docs/multi-llm-review.md`** in parallel (single message, multiple tool calls). This includes Codex, Gemini, AND seq-server — do NOT skip any.

### Step 4: Agent Review

Run MUST RUN agents, then domain-specific:

```bash
# MUST RUN
Task(subagent_type="design-flaw-finder", prompt="[plan]")
Task(subagent_type="simplicity-reviewer", prompt="[plan]")

# Domain-specific (if API plan)
Task(subagent_type="api-contract-reviewer", prompt="[plan]")
Task(subagent_type="rest-api-expert", prompt="[plan]")
```

### Step 5: Iterate

If MUST FIX items found:
1. Update plan to address blockers
2. Re-run affected reviews if major changes
3. Maximum 2 iterations

## Output Format

```markdown
## Plan Review Summary

### Status: READY / NEEDS ATTENTION

### Multi-LLM Review
- [Resolved blockers]
- [Remaining SHOULD FIX]

### Agent Review
| Agent | Verdict | Key Findings |
|-------|---------|--------------|
| design-flaw-finder | PASS/FAIL | [summary] |
| simplicity-reviewer | PASS/FAIL | [summary] |
| [domain agents] | PASS/FAIL | [summary] |

### Resolved Blockers
- [What was fixed]

### Remaining SHOULD FIX
- [Item] - [recommendation]

---

[Full plan content]
```

## Flags

| Flag | Effect |
|------|--------|
| `--draft` | Skip all review, just generate plan |
| `--no-agents` | Skip agent review, only multi-LLM |
| `--minimal` | Abbreviated template for small features |
| `--output <path>` | Save to specific file |
| `--no-iterate` | Don't auto-fix, just report findings |
| `--generic` | Force generic template |

## Examples

```bash
# Full workflow - create, multi-LLM review, agent review, iterate
/create-plan "Add OAuth2 support with Google and GitHub providers"

# Skip agents (faster, less thorough)
/create-plan "Add OAuth2 support" --no-agents

# Just draft, no review at all
/create-plan "Add loading spinner" --draft

# Minimal plan for small feature
/create-plan "Fix pagination bug" --minimal
```

## When to Use

| Scenario | Use `/create-plan` | Just ask CC |
|----------|--------------------|-----------------------|
| New feature | ✅ | |
| Multi-file change | ✅ | |
| API/database changes | ✅ | |
| UI/UX changes | ✅ | |
| Simple bug fix | | ✅ |
| Single-file tweak | | ✅ |

## Tips

- **Don't skip agents** - They catch different issues than multi-LLM review
- **MUST RUN agents are cheap** - Always run design-flaw-finder + simplicity-reviewer
- **Domain detection is automatic** - Skill scans plan for keywords
- **Code agents come later** - Save implementation review for `/review-code`
- **Max 2 iterations** - If still blocked, rethink the approach
