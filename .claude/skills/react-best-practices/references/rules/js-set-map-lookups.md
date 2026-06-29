# Use Set/Map for O(1) Lookups

**Impact:** LOW-MEDIUM (O(n) to O(1) per check)
**Tags:** javascript, performance, set, map, membership

## Problem

`Array.includes()` is O(n) per check:

```typescript
// BAD: O(n) per includes check
const allowedIds = ['a', 'b', 'c', ...];
items.filter(item => allowedIds.includes(item.id));

// In a loop, this becomes O(n²)
deletedAnchorIds.forEach(id => {
  if (existingIds.includes(id)) { ... }
});
```

## Solution

Convert arrays to Set for O(1) membership checks:

```typescript
// GOOD: O(1) per has check
const allowedIds = new Set(['a', 'b', 'c', ...]);
items.filter(item => allowedIds.has(item.id));
```

## Example - Editor Component

```typescript
// BAD: Current pattern - O(n²) in nested loops
editor.state.doc.descendants((node, pos) => {
  node.marks.forEach((mark) => {
    if (mark.type.name === 'commentAnchor' &&
        deletedAnchorIds.includes(mark.attrs.anchorId)) {  // O(n)
      // ...
    }
  });
});

// GOOD: Convert once, O(1) lookups
const deletedAnchorSet = new Set(deletedAnchorIds);  // O(n) once
editor.state.doc.descendants((node, pos) => {
  node.marks.forEach((mark) => {
    if (mark.type.name === 'commentAnchor' &&
        deletedAnchorSet.has(mark.attrs.anchorId)) {  // O(1)
      // ...
    }
  });
});
```

## When to Use

- Checking membership in a list
- Filtering items against an allow/block list
- Deduplication checks
- Any repeated `.includes()` calls
