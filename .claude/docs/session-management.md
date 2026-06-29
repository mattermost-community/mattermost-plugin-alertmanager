# Session Management

## Context Hygiene
- `/clear` between unrelated tasks - prevents context contamination
- After 2+ failed corrections, `/clear` and write a better initial prompt
- Run `/context` mid-session to check token usage

## Session Resume
```bash
claude --continue          # Resume most recent session
claude --resume            # Pick from recent sessions
claude --from-pr 123       # Resume session linked to PR
```

## Session Naming
Use `/rename` to give sessions descriptive names (e.g., "oauth-migration", "debugging-memory-leak") so you can find them later.

## Checkpoints & Rewind
- Every Claude action creates a checkpoint
- Double-tap `Escape` or `/rewind` to open checkpoint menu
- Can restore: conversation only, code only, or both
- Checkpoints persist across sessions

## Subagent Context Strategy
Use subagents to keep main context clean:
```
Use subagents to investigate how authentication handles token refresh.
```
The subagent explores in separate context and reports back a summary.

## When to Clear vs Continue
- **Clear**: New unrelated task, context polluted with failed attempts
- **Continue**: Same task spanning multiple sessions, need prior context
