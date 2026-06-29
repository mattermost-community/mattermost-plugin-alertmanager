---
name: create-code
description: Implement code from an approved plan. Supports TDD workflow. Runs linters and tests before completion.
---

# Create Code

Implement code from an approved plan. Ensures quality through structured workflow with linting and testing.

> **Taxonomy**:
> - `/create-plan` → `/create-code` → `/review-code`
> - Plan first, then implement, then review

**Related**:
- `/create-plan` - Create and validate a plan first
- `/review-code` - Review implementation after coding
- `/tdd-workflow` - TDD-specific methodology (absorbed into this skill)

## CRITICAL: Plan File is Source of Truth

**ALWAYS read from the saved plan file - NEVER use inline/conversation context.**

```
✅ CORRECT:
/create-code implementation-plans/page-reordering.md
→ Reads plan from file
→ "Implementing from implementation-plans/page-reordering.md"

❌ WRONG:
User: "Implement the plan we discussed"
Claude: [Uses plan from conversation memory]
```

**Why this matters:**
- Plan file is version-controlled and reviewable
- Conversation context may drift from saved plan
- User can edit plan file before implementation
- Multiple sessions can reference the same plan
- Prevents implementing stale/modified plans

## Usage

```
/create-code <plan-file>                  # Implement from plan
/create-code <plan-file> --tdd            # Use TDD (write tests first)
/create-code <plan-file> --no-tests       # Skip test writing (not recommended)
/create-code <plan-file> --task <n>       # Implement specific task from plan
/create-code                              # Find most recent plan in implementation-plans/
```

### When No File Specified

If invoked without a plan file:
1. **List plans** in `implementation-plans/` directory
2. **Show most recent** plan (by modification time)
3. **Ask user** to confirm which plan to implement

```bash
# Example discovery
ls -lt implementation-plans/*.md | head -5

# Ask user
"Found these plans:
1. page-reordering.md (modified today)
2. oauth-support.md (modified yesterday)
Which plan should I implement?"
```

**Never guess or assume** - always confirm with user.

## What It Does

```
/create-code implementation-plans/feature.md
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 1: READ PLAN                      │
│  - Parse tasks from plan                │
│  - Identify files to modify             │
│  - Understand acceptance criteria       │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 2: IMPLEMENT (per task)           │
│                                         │
│  Standard mode:                         │
│  - Implement code                       │
│  - Write/update tests                   │
│  - Run tests to verify                  │
│                                         │
│  TDD mode (--tdd):                      │
│  - Write failing test first             │
│  - Implement minimal code to pass       │
│  - Refactor                             │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 3: QUALITY CHECKS                 │
│  - Run linters (go vet, eslint)         │
│  - Run type checks (tsc)                │
│  - Fix any issues                       │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  Step 4: TEST VERIFICATION              │
│  - Run all tests                        │
│  - Ensure no regressions                │
│  - Report coverage (if applicable)      │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│  OUTPUT: Working Code                   │
│  - All tasks implemented                │
│  - Tests passing                        │
│  - Linters clean                        │
│  - Ready for /review-code               │
└─────────────────────────────────────────┘
```

## Workflow Details

### Step 1: Read Plan

Parse the plan file to extract:
- Tasks list (ordered)
- Files to modify
- Acceptance criteria
- Technical approach

```bash
# Read the plan
Read(plan_file)

# Extract tasks
tasks = parse_tasks(plan)
files = parse_files_to_modify(plan)
criteria = parse_acceptance_criteria(plan)
```

### Step 2: Implement Each Task

For each task in the plan:

**Standard Mode:**
```
1. Read relevant existing code
2. Implement the change
3. Write/update tests for the change
4. Run tests to verify
5. Move to next task
```

**TDD Mode (`--tdd`):**
```
1. Write a failing test for the expected behavior
2. Run test - confirm it fails (RED)
3. Write minimal code to make test pass
4. Run test - confirm it passes (GREEN)
5. Refactor if needed (REFACTOR)
6. Move to next task
```

### Step 3: Quality Checks

Run all relevant linters:

**Go (server/):**
```bash
cd server && make check-style
# or: golangci-lint run ./...
```

**TypeScript (webapp/):**
```bash
cd webapp/channels && npm run check-types
cd webapp/channels && npm run check  # ESLint + Stylelint
```

**Auto-fix when possible:**
```bash
cd server && gofmt -s -w .
cd webapp/channels && npm run fix
```

### Step 4: Test Verification

Run all tests to ensure no regressions:

**Go tests:**
```bash
cd server && go test ./channels/app/... -v
# or specific: go test ./channels/app -run TestFeatureName -v
```

**TypeScript tests:**
```bash
cd webapp/channels && npm test -- --testPathPattern="feature"
```

## TDD Workflow (`--tdd` flag)

When using TDD mode, follow the RED-GREEN-REFACTOR cycle:

```
┌─────────────────────────────────────────┐
│                 RED                      │
│  Write a test that fails                │
│  - Test expected behavior               │
│  - Run test, see it fail                │
│  - Confirms test is valid               │
└─────────────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│                GREEN                     │
│  Write minimal code to pass             │
│  - Just enough to pass the test         │
│  - No premature optimization            │
│  - Run test, see it pass                │
└─────────────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│              REFACTOR                    │
│  Improve code quality                   │
│  - Remove duplication                   │
│  - Improve naming                       │
│  - Tests still pass                     │
└─────────────────────────────────────────┘
```

## Output Format

```markdown
## Implementation Summary

### Status: COMPLETE / PARTIAL / BLOCKED

### Tasks Completed
- [x] Task 1: [description]
- [x] Task 2: [description]
- [ ] Task 3: [blocked - reason]

### Files Modified
| File | Changes |
|------|---------|
| `path/to/file.go` | Added X function |
| `path/to/file.ts` | Updated Y component |

### Tests
- **Added**: 5 new tests
- **Modified**: 2 existing tests
- **All passing**: ✅

### Linting
- **Go**: ✅ Clean
- **TypeScript**: ✅ Clean

### Ready for Review
Use `/review-code` to review these changes before committing.
```

## Flags

| Flag | Effect |
|------|--------|
| `--tdd` | Use TDD workflow (write tests first) |
| `--no-tests` | Skip test writing (use sparingly) |
| `--task <n>` | Implement only task N from plan |
| `--continue` | Resume from last incomplete task |
| `--dry-run` | Show what would be done without doing it |

## Examples

```bash
# Implement all tasks from plan
/create-code implementation-plans/oauth-support.md

# Use TDD workflow
/create-code implementation-plans/oauth-support.md --tdd

# Implement specific task
/create-code implementation-plans/oauth-support.md --task 3

# Resume interrupted implementation
/create-code implementation-plans/oauth-support.md --continue
```

## When to Use

| Scenario | Use `/create-code` | Just implement |
|----------|--------------------|-----------------------|
| Have an approved plan | ✅ | |
| Multi-task implementation | ✅ | |
| Want structured workflow | ✅ | |
| Need TDD enforcement | ✅ | |
| Quick one-off fix | | ✅ |
| Exploratory coding | | ✅ |

## Integration with Workflow

```
User request
    │
    ▼
/create-plan "feature"     # Create and validate plan
    │
    ▼
User approves plan
    │
    ▼
/create-code plan.md       # Implement from plan  ← THIS SKILL
    │
    ▼
/review-code               # Review implementation
    │
    ▼
/commit (if approved)
```

## Tips

- **Always have a plan first** - Don't use this for ad-hoc coding
- **Use `--tdd` for complex logic** - TDD catches bugs early
- **Don't skip tests** - `--no-tests` is for rare exceptions only
- **Run linters early** - Catch style issues before they accumulate
- **Check tests before moving on** - Each task should leave tests green
