# File Safety Rules

Rules to prevent accidental data loss during refactoring.

## Required Pre-Operation Checks

Before ANY file move, deletion, or destructive operation:
1. **Read the file first**: Use `Read` tool to verify contents
2. **Check file size**: Use `wc -l <file>` to see line count
3. **Verify git status**: Run `git status` to understand current state
4. **Review staged changes**: Use `git diff --staged` before committing

## Package Structure Rules

When moving code between packages:
1. **Check method receivers**: Methods on a parent package type (e.g., `MMToolProvider` in `mmtools`) CANNOT be moved to subpackages
2. **Verify imports**: Ensure no circular dependencies will be created
3. **Test the move**: Use plain `git mv` (no `-f` flag) so Git warns of issues

## Large File Operations

For files with >500 lines of code:
1. **Explain intent**: Describe what you're doing and why
2. **Wait for confirmation**: Don't proceed until user approves
3. **Document the plan**: Use TodoWrite to track multi-step operations

## Recovery Awareness

Deleted/modified files may be **unrecoverable**:
- LLM-reconstructed code will have API mismatches and bugs
- Original implementation details, edge cases, and optimizations are lost
- Tests may pass but behavior may differ subtly
- **Prevention is the only reliable strategy**
