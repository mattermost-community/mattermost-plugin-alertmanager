# Pattern Alignment - ALWAYS Match Existing Code

**CRITICAL**: Before adding or modifying ANY code in ANY layer, ALWAYS audit existing code to understand and match established patterns.

## Why This Matters
- Consistency is critical for maintainability
- Existing patterns encode years of learned best practices
- Pattern violations create technical debt and future bugs

## Workflow

**BEFORE writing ANY code:**

1. **Identify the layer** you're modifying (e.g., API handlers, business logic, data access, models, frontend components, database migrations)

2. **Find similar existing code**: Use Grep to find 3-5 similar functions, Read actual implementations

3. **Identify patterns**: Function calls, error handling, naming, validation, transactions

4. **Match EXACTLY**: Same patterns, same style, same conventions

## Layer Rules

### API Layer
**RULE**: API handlers MUST call business logic layer methods, NEVER direct database access.

```go
// CORRECT: API → Business logic layer
result, err := app.GetResource(ctx, resourceId)

// WRONG: API → Database (bypasses permissions, caching, events)
result, err := store.Resource().Get(resourceId)
```

### Business Logic Layer
**RULE**: Business logic methods handle permissions, caching, events, and call the data access layer.

### Data Access Layer
**RULE**: Data access methods handle database operations only, no business logic.

### Model Layer
**RULE**: Models define data structures and validation, no dependencies on other layers.

## Audit Checklist

1. **Search**: Find 3-5 similar implementations in the same layer
2. **Read**: Study actual code (not just signatures)
3. **Document**: Write down patterns you observe
4. **Match**: Follow those patterns EXACTLY
5. **Verify**: Your code should look like it "belongs" in that file

## RED FLAGS - Stop and Audit
- Your code looks different from surrounding code
- You're calling a layer that similar functions don't call
- You're inventing new conventions
- You're adding abstractions not present in similar code

**"When in Rome, do as the Romans do"** - Your code must be indistinguishable from existing code.
