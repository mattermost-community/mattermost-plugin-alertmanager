# Narrow Effect Dependencies

**Impact:** MEDIUM (minimizes effect re-runs)
**Tags:** rerender, useEffect, dependencies, optimization

## Problem

Using objects as dependencies causes unnecessary re-runs:

```typescript
// BAD: Effect runs when ANY page property changes
useEffect(() => {
  loadComments(page.id);
}, [page]);  // Object dependency

// BAD: Effect runs on location object changes
useEffect(() => {
  trackPageView(location.pathname);
}, [location]);  // Object dependency
```

## Solution

Use primitive dependencies instead:

```typescript
// GOOD: Effect only runs when ID changes
useEffect(() => {
  loadComments(pageId);
}, [pageId]);

// GOOD: Only depend on what you use
useEffect(() => {
  trackPageView(pathname);
}, [pathname]);
```

## Example - Component Callbacks

```typescript
// BAD: Depends on location object
const onEdit = React.useCallback(async () => {
  // ...
}, [handleEdit, projectId, channelId, history, location]);

// GOOD: Extract specific properties
const { pathname, search } = location;
const onEdit = React.useCallback(async () => {
  // ...
}, [handleEdit, projectId, channelId, history, pathname, search]);
```

## Derived State Pattern

```typescript
// BAD: Effect runs on every pixel change
useEffect(() => {
  setLayout(width < 768 ? 'mobile' : 'desktop');
}, [width]);

// GOOD: Effect runs only on breakpoint change
const isMobile = width < 768;
useEffect(() => {
  setLayout(isMobile ? 'mobile' : 'desktop');
}, [isMobile]);
```
