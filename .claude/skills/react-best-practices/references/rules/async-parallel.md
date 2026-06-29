# Promise.all() for Independent Operations

**Impact:** CRITICAL (2-10x performance improvement)
**Tags:** async, parallelization, promises, waterfalls

## Problem

Sequential await statements that could run in parallel:

```typescript
// BAD: 3 round trips
const page = await getPage(pageId);
const children = await getPageChildren(pageId);
const comments = await getPageComments(pageId);
```

## Solution

Use `Promise.all()` for independent async operations:

```typescript
// GOOD: 1 round trip
const [page, children, comments] = await Promise.all([
  getPage(pageId),
  getPageChildren(pageId),
  getPageComments(pageId),
]);
```

## Examples

```typescript
// BAD: Sequential in useItemPageData
const channelResult = await dispatch(getChannel(channelId));
const memberResult = await dispatch(getChannelMember(channelId, currentUserId));

// GOOD: Parallel
const [channelResult, memberResult] = await Promise.all([
  dispatch(getChannel(channelId)),
  dispatch(getChannelMember(channelId, currentUserId)),
]);
```

## When NOT to Parallelize

- When operations depend on each other's results
- When order matters for side effects
- When error handling requires sequential processing
