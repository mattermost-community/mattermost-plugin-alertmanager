---
name: lint
description: Run all linters and auto-fix issues where possible
allowed-tools: Bash, Read, Grep, Glob
---

# Lint & Fix

Run all linters and auto-fix issues where possible.

## Steps

### Go Projects

1. **Go formatting fix**:
   ```bash
   gofmt -s -w .
   ```

2. **Go linting** (if golangci-lint configured):
   ```bash
   golangci-lint run ./...
   ```

3. **Go vet**:
   ```bash
   go vet ./...
   ```

### TypeScript/JavaScript Projects

4. **ESLint fix**:
   ```bash
   npm run lint -- --fix
   ```
   Or if using a different script name:
   ```bash
   npx eslint --fix .
   ```

5. **TypeScript type checking**:
   ```bash
   npm run check-types
   ```
   Or:
   ```bash
   npx tsc --noEmit
   ```

6. **Prettier formatting** (if configured):
   ```bash
   npx prettier --write .
   ```

### E2E Tests (if present)

7. **E2E lint fix**:
   ```bash
   cd e2e-tests && npm run lint -- --fix
   ```

8. **E2E type checking**:
   ```bash
   cd e2e-tests && npx tsc --noEmit
   ```

## Auto-Detection

Before running commands, detect the project type:
- Look for `go.mod` for Go projects
- Look for `package.json` for Node.js projects
- Look for `tsconfig.json` for TypeScript projects
- Check `package.json` scripts for available lint commands

Use `git rev-parse --show-toplevel` to detect repository root.

## Output Format

```
## Lint & Fix Results

### Go
- gofmt: FIXED/CLEAN
- golangci-lint: PASS/FAIL
- go vet: PASS/FAIL

### TypeScript
- ESLint: FIXED/CLEAN/FAIL
- Type check: PASS/FAIL
- Prettier: FIXED/CLEAN

### Summary
[CLEAN: Ready to commit | FIXED: X files auto-fixed | ISSUES: X problems need manual fix]

### Remaining Issues (if any)
[List errors that couldn't be auto-fixed with file:line references]
```

## Notes

- Run all steps even if one fails
- Most formatting/style issues are auto-fixed
- Manual fixes still needed for:
  - TypeScript type errors
  - Complex lint errors that can't be auto-fixed
  - Logic-related linting issues
