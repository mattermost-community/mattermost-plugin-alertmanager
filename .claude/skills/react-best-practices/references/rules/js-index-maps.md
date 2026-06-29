# Build Index Maps for Repeated Lookups

**Impact:** HIGH (O(n) to O(1) per lookup)
**Tags:** javascript, performance, maps, lookups

## Problem

Repeated `.find()` calls on arrays are O(n) each:

```typescript
// BAD: O(n) per lookup, O(n²) total for n items
pages.forEach(page => {
  const author = users.find(u => u.id === page.authorId);
  const project = projects.find(p => p.id === doc.projectId);
});
```

## Solution

Build a Map once (O(n)), then all lookups are O(1):

```typescript
// GOOD: O(n) setup, O(1) per lookup
const userMap = new Map(users.map(u => [u.id, u]));
const projectMap = new Map(projects.map(p => [p.id, p]));

pages.forEach(page => {
  const author = userMap.get(page.authorId);
  const project = projectMap.get(doc.projectId);
});
```

## Example - Menu Handlers

```typescript
// BAD: Current pattern - 9+ .find() calls
const handleCreateChild = (pageId: string) => {
  const parentPage = allPagesRef.current.find((p) => p.id === pageId);
  // ...
};
const handleRename = (pageId: string) => {
  const page = allPagesRef.current.find((p) => p.id === pageId);
  // ...
};

// GOOD: Build map once, use O(1) lookups
const pageMapRef = useRef<Map<string, Post>>();
useMemo(() => {
  pageMapRef.current = new Map(allPages.map((p) => [p.id, p]));
}, [allPages]);

const handleCreateChild = (pageId: string) => {
  const parentPage = pageMapRef.current?.get(pageId);
};
```

## When to Use

- Multiple lookups against the same dataset
- Processing lists where items reference other lists
- Any hot path with repeated `.find()` calls
