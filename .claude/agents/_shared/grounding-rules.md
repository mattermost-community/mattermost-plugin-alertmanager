# Evidence-Based Grounding Rules

**MANDATORY** - All agent findings MUST be grounded in actual code/files.

## Core Rules

1. **READ BEFORE REPORTING**: You MUST read a file using the Read tool BEFORE reporting any issue in that file. Never report issues in files you have not read in this session.

2. **VERIFY FILE EXISTS**: Before referencing any file path, use Glob to verify it exists. If Glob returns no results, the file does not exist - do not report issues for non-existent files.

3. **QUOTE ACTUAL CODE**: Every finding MUST include a direct quote of the problematic code copied from your Read tool output. No paraphrasing or reconstructing from memory.

4. **VERIFY LINE NUMBERS**: When reporting `file:line`, the line number must match your Read output. Count the lines if necessary.

5. **NO HALLUCINATED PATHS**: Never invent or guess file paths. Only use paths you have confirmed exist via Glob or Read.

6. **NO ASSUMPTIONS**: If you cannot verify something exists, do not report it. Say "I could not verify..." instead.

## Verification Templates

### For Code Issues
```
**Issue**: [type] in `verified/path/file.go:NN`
**Evidence** (from Read output):
```go
// Actual code copied from Read tool
```
**Problem**: [description based on evidence]
```

### For "Missing" Claims
Before claiming validation, auth, or any pattern is "missing":
- Search for it with Grep across the codebase
- Check if it's handled in middleware, app layer, or elsewhere
- Only report as "missing" after verifying it's not done anywhere in the call chain

```
**Claim**: [X] is missing
**Verification**:
grep -r "pattern" path/
**Results**: [paste actual grep output]
**Conclusion**: [CONFIRMED missing / Actually handled at Y]
```

### For "Function/Type Does Not Exist" Claims
Before claiming a function, type, or variable doesn't exist:
- Search for its definition with Grep (not just its usage)
- Check the same package (other files), imports, and dependencies
- Only report as "doesn't exist" after confirming no definition found

```
**Claim**: [Function X] does not exist
**Verification**:
grep -r "func X\|func.*X\|type X" --include="*.go" .
**Results**: [paste actual grep output]
**Conclusion**: [CONFIRMED missing / Actually defined at path/file.go:NN]
```

### For "Unused Code" Claims
```
**Claim**: [X] appears unused
**Verification**:
grep -r "X" --include="*.go" server/
grep -r "X" --include="*.ts" webapp/
**Results**: [paste actual grep output]
**Conclusion**: [CONFIRMED unused / Actually used in N locations]
```

## Before Submitting Review

- [ ] Every file path mentioned has been verified with Glob or Read
- [ ] Every code snippet is copied from Read output, not reconstructed
- [ ] Every "missing" claim has grep verification showing no results
- [ ] Every "doesn't exist" claim has grep verification for definitions
- [ ] Line numbers match actual Read output
