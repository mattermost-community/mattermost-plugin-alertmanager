# Code Quality Standards

## Code Comments Guidelines
- **NEVER add migration/legacy comments**: No "functionality removed", "deprecated", "legacy"
- **NEVER explain what was removed**: Just remove code cleanly
- **NEVER add implementation detail comments**: No "// Reusing same struct"
- **NEVER add obvious comments**: No "// Create variable X" - code should be self-explanatory
- **NEVER add statistical comments**: No "// Channel with 1000 messages"
- **NEVER add provenance comments**: No "COPIED from X", "Moved from Y"

## Code Movement/Refactoring
When moving or reorganizing code:
- **ALWAYS COPY, NEVER RECREATE**: Copy exact implementation, don't retype
- **Use Read tool first**: Get exact implementation before moving
- **Preserve comments**: Copy existing non-violating comments
- **No provenance comments**: Don't add "COPIED from", "Moved from"
- **Verify after move**: Check both old and new locations

## Implementation Rules
- **NEVER optimize for "getting tests passing quickly"**: Fix actual issues
- **NEVER hardcode values**: Always define constants
- **NEVER add stubs/placeholders/TODOs**: Implement complete solutions
- **NO OVERENGINEERING**: Simplest solution that works, follow YAGNI
- **Align with existing patterns**: Copy structure and style of similar code

## Quality Checks
- Make surgical changes only - preserve existing patterns
- Never create commits without explicit instruction
- Follow existing code style exactly
- Fix tests to match correct behavior
- Add explicit TypeScript types, fix all linter errors
- Mock only what you use, test only what you call
- **NEVER write empty or skipped tests**
- **ENSURE existing tests pass** after implementation
