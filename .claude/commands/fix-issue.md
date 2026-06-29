---
name: fix-issue
description: GitHub issue to implementation workflow. Analyzes issue, locates code, implements fix, adds tests.
allowed-tools: Read, Write, Edit, Bash, Grep, Glob, Task, WebFetch
---

# Fix Issue Command

Implement a fix for GitHub issue: $ARGUMENTS

## Workflow

### Phase 1: Understand the Issue

1. **Parse issue reference** (e.g., `#123` or URL)
2. **Fetch issue details**:
   ```bash
   gh issue view <number> --json title,body,labels,comments
   ```
3. **Identify issue type**:
   - Bug fix
   - Feature request
   - Enhancement
   - Documentation

### Phase 2: Locate Relevant Code

Based on issue description, find affected areas:

```bash
# Search for keywords from issue
Grep pattern="keyword" path="."

# Find related files by pattern
Glob pattern="**/*related*"
```

**Categorize by layer**:
| Keywords | Likely Layer | Examples |
|----------|--------------|---------|
| API, endpoint, handler | API handlers | Routes, controllers |
| business logic, validation | Business logic | Services, app layer |
| database, query, SQL | Data access | Stores, repositories |
| component, UI, render | Frontend | Components, views |
| action, dispatch, redux | State management | Actions, reducers |

### Phase 3: Analyze Root Cause

For bugs:
1. Reproduce the issue (understand the failing case)
2. Trace the code path
3. Identify the root cause
4. Determine the fix location (correct layer!)

For features:
1. Understand the requirement
2. Identify integration points
3. Design the implementation
4. Plan the changes by layer

### Phase 4: Implement the Fix

Follow existing codebase patterns:

**API Layer** (if needed):
- Add/modify handler
- Check permissions
- Call business logic methods

**Business Logic Layer**:
- Add validation
- Implement business rules
- Call data access layer
- Handle errors properly

**Data Access Layer** (if needed):
- Add/modify data access methods
- Return proper errors

**Model Layer** (if needed):
- Add/modify types
- Add validation methods

**Frontend** (if needed):
- Update types
- Modify actions/reducers
- Update components

### Phase 5: Add Tests

**Go tests**:
```bash
# Run existing tests first
go test ./path/to/package -run TestRelated -v

# Add new tests in *_test.go files
```

**TypeScript tests**:
```bash
# Run existing tests
npm test -- --testPathPattern="related"

# Add new tests in *.test.tsx files
```

### Phase 6: Verify

```bash
# Go
go vet ./...
go test ./...

# TypeScript
npm run check-types
npm run lint
npm test
```

## Output Format

```markdown
## Issue: [Title]

**Reference**: [#123]
**Type**: [Bug | Feature | Enhancement]

### Analysis

**Root cause**: [explanation]
**Affected layers**: [list affected layers]
**Files to modify**:
- `path/to/file` - [change needed]

### Implementation Plan

1. [ ] [Step 1]
2. [ ] [Step 2]
3. [ ] [Step 3]
4. [ ] Add tests
5. [ ] Run verification

### Changes Made

#### [Layer]
- `file:line` - [change description]

#### Tests Added
- `file_test.go` - [test description]

### Verification

- [ ] Linting passes
- [ ] Go tests pass
- [ ] TypeScript types pass
- [ ] Frontend tests pass

### PR Ready

**Title**: [Fix/Feature] [Brief description]
**Description**: [What and why]
```

## Notes

- Always fix in the **correct layer**
- Add tests for the specific issue
- Follow existing patterns in the codebase
- Don't over-engineer - minimal fix for the issue
