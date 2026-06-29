# Git Safety Rules

## ABSOLUTE PROHIBITIONS
These commands DESTROY uncommitted work and are FORBIDDEN:

- **NEVER run `git checkout -- <path>`** - Permanently discards changes
- **NEVER run `git checkout HEAD -- <path>`** - Permanently discards changes
- **NEVER run `git restore <path>`** - Permanently discards changes
- **NEVER run `git reset --hard`** - Destroys all uncommitted work
- **NEVER use `git rebase`** - Rewrites history
- **NEVER use force flags** (`-f`) on any git command
- **NEVER unstage files** without explicit user permission

## Safe Alternatives

When you need to test with a clean state:
```bash
# CORRECT WAY:
git stash                    # Save changes safely
# ... run tests ...
git stash pop                # Restore changes

# WRONG WAY (FORBIDDEN):
git checkout -- webapp/      # NEVER DO THIS
```

Other safe commands:
- `git mv <source> <dest>` (without `-f`) - Will error if destination exists
- `git status` - Check state before operations
- `git diff --staged` - Review what will be committed
- `git diff <file>` - See uncommitted changes

## Recovery Awareness

Deleted/modified files may be **unrecoverable**:
- LLM-reconstructed code will have API mismatches and bugs
- Original implementation details, edge cases, and optimizations are lost
- Tests may pass but behavior may differ subtly
- **Prevention is the only reliable strategy**
