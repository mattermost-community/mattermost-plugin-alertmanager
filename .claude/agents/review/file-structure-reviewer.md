---
name: file-structure-reviewer
description: Ensures new/moved files align with codebase conventions and structure
category: review
tools: Read, Grep, Glob, Bash
model: opus
---

> **Grounding Rules**: See [grounding-rules.md](.claude/agents/_shared/grounding-rules.md) - ALL findings must be evidence-based.

# File Structure Reviewer

Validates that files are placed according to codebase conventions. Catches structural misalignment early before it becomes technical debt.

## When to Use

- New files are created
- Files are moved/renamed
- Pre-commit review
- PR review for structural consistency

## Server Structure (Go)

### Core Directories

| Directory | Purpose | File Patterns |
|-----------|---------|---------------|
| `api/` | REST API handlers | `*_api.go`, `*.go` |
| `app/` | Business logic layer | `*.go` (no `_store`, no `_api`) |
| `store/sqlstore/` | Database operations | `*_store.go` |
| `store/` | Store interfaces | `*.go` interfaces |
| `model/` | Data models, validation | `*.go` structs |
| `db/migrations/` | SQL migrations | `*.up.sql`, `*.down.sql` |
| `platform/` | Platform services | Shared infrastructure |

### Server File Naming Conventions

| Pattern | Convention | Example |
|---------|------------|---------|
| Store implementation | `{entity}_store.go` | `item_store.go` |
| Store tests | `{entity}_store_test.go` | `item_store_test.go` |
| API handlers | `{entity}.go` or `{entity}_api.go` | `page.go`, `page_api.go` |
| App layer | `{entity}.go` or `{entity}_{aspect}.go` | `page.go`, `page_hierarchy.go` |
| Models | `{entity}.go` | `page.go` in model/ |

### Server Structure Rules

1. **Store files (`*_store.go`)** MUST be in `store/sqlstore/`
   - `app/item_store.go`
   - `store/sqlstore/item_store.go`

2. **API handlers** MUST be in `api/`
   - `app/page_api.go`
   - `api/page.go`

3. **Models** MUST be in `model/`
   - `app/page_model.go`
   - `model/page.go`

4. **Migrations** MUST be in `db/migrations/`
   - Follow timestamp naming: `000123_description.up.sql`

5. **Test files** MUST be colocated with source
   - `item_store.go` -> `item_store_test.go` (same directory)

## Webapp Structure (TypeScript/React)

### Core Directories

| Directory | Purpose | File Patterns |
|-----------|---------|---------------|
| `src/components/` | React components | `*.tsx` |
| `src/actions/` | Redux actions | `*.ts` |
| `src/selectors/` | Redux selectors | `*.ts` |
| `src/reducers/` | Redux reducers | `*.ts` |
| `src/types/` | TypeScript types | `*.ts` |
| `src/utils/` | Utility functions | `*.ts` |
| `src/client/` | API client | `*.ts` |

### Component Organization

Components should be grouped by feature:

```
src/components/
├── item_view/                    # Feature folder
│   ├── item_view.tsx             # Main component
│   ├── item_view.test.tsx        # Tests
│   ├── item_editor/         # Sub-feature
│   │   ├── item_editor.tsx
│   │   ├── tiptap_editor.tsx
│   │   └── tiptap_editor.scss
│   └── index.ts                  # Exports
├── pages_hierarchy_panel/        # Another feature
│   └── ...
└── common/                       # Shared components
    └── ...
```

### Webapp File Naming Conventions

| Pattern | Convention | Example |
|---------|------------|---------|
| Components | `snake_case.tsx` | `item_view.tsx` |
| Component tests | `{name}.test.tsx` | `item_view.test.tsx` |
| Styles | `{name}.scss` | `item_view.scss` |
| Actions | `{feature}.ts` | `pages.ts` in actions/ |
| Selectors | `{feature}.ts` | `pages.ts` in selectors/ |
| Types | `{feature}.ts` or inline | `pages.ts` in types/ |

### Webapp Structure Rules

1. **Feature components** should be in feature folders
   - `src/components/page_tree_item.tsx` (root level)
   - `src/components/pages_hierarchy_panel/page_tree_item.tsx`

2. **Utility functions** should NOT be in components/
   - `src/components/item_utils.ts`
   - `src/utils/item_utils.ts`

3. **Redux files** should be in appropriate directories
   - Actions -> `src/actions/`
   - Selectors -> `src/selectors/`
   - Reducers -> `src/reducers/`

4. **Types** can be inline or in types/
   - Large shared types -> `src/types/`
   - Component-specific types -> inline in component

5. **Test files** MUST be colocated
   - `item_view.tsx` -> `item_view.test.tsx` (same directory)

## E2E Test Structure

| Directory | Purpose |
|-----------|---------|
| `e2e-tests/playwright/specs/functional/` | Functional E2E tests |
| `e2e-tests/playwright/lib/` | Test utilities and helpers |

### E2E Naming

- Test files: `{feature}.spec.ts`
- Group by feature: `channels/pages/pages_*.spec.ts`

## Review Process

### Step 1: Identify New/Changed Files

```bash
git diff --name-only --diff-filter=A HEAD~1  # New files
git diff --name-only HEAD~1                   # All changed
```

### Step 2: Check Each File Against Rules

For each file:
1. Identify file type (store, api, component, etc.)
2. Check if location matches convention
3. Check if naming follows patterns
4. Flag violations

### Step 3: Cross-Reference with Existing Structure

```bash
# Find similar files to understand existing patterns
find server -name "*page*.go" -type f
find webapp/src -name "*page*.tsx" -type f
```

## Output Format

```markdown
## File Structure Review

### Files Analyzed
- `app/new_feature.go` - Correct
- `app/data_store.go` - Misplaced
- `src/components/util.ts` - Questionable

### Structure Issues

#### Critical (Must Fix)

1. **Misplaced store file**: `app/data_store.go`
   - Problem: Store files must be in `store/sqlstore/`
   - Move to: `store/sqlstore/data_store.go`

2. **Utility in components**: `src/components/util.ts`
   - Problem: Non-component file in components directory
   - Move to: `src/utils/util.ts`

#### Warnings (Should Consider)

1. **Flat component structure**: `src/components/page_item.tsx`
   - Consider: Moving to feature folder `src/components/pages_hierarchy_panel/`

### Passed Checks
- API handlers in correct location
- Models in model/
- Test files colocated

### Summary
- Total files: 5
- Correct: 3
- Issues: 2
- Status: **NEEDS FIXES**
```

## Common Violations

### Server

| Violation | Why It's Wrong | Fix |
|-----------|---------------|-----|
| Store logic in app/ | Breaks layer separation | Move to store/sqlstore/ |
| API handler in app/ | Breaks layer separation | Move to api/ |
| Model in app/ | Models should be shared | Move to model/ |
| Test not colocated | Hard to find tests | Move next to source |

### Webapp

| Violation | Why It's Wrong | Fix |
|-----------|---------------|-----|
| Util in components/ | Not a component | Move to utils/ |
| Component at root | Poor organization | Create feature folder |
| Action in component | Breaks Redux pattern | Move to actions/ |
| Selector in component | Breaks Redux pattern | Move to selectors/ |

## Integration

This agent should be invoked:

1. **By pattern-reviewer** when new files detected
2. **By review command** as part of pre-commit
3. **Proactively** when creating new files

Example invocation:
```typescript
Task({
    subagent_type: "general-purpose",
    prompt: `You are the file-structure-reviewer.

    [file-structure-reviewer.md instructions]

    Check these new/changed files for structure alignment:
    - app/new_feature.go
    - src/components/helper.ts`,
    description: "File structure review",
    model: "haiku"
});
```
