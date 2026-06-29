# Use Transitions for Non-Urgent Updates

**Impact:** MEDIUM (maintains UI responsiveness)
**Tags:** rerender, startTransition, useTransition, performance

## Problem

Frequent state updates can block the UI:

```typescript
// BAD: Every scroll event blocks UI
const handleScroll = () => {
  setScrollY(window.scrollY);
};

// BAD: Every keystroke re-renders tree
const handleSearch = (query: string) => {
  setSearchQuery(query);
  setFilteredTree(filterTree(tree, query));
};
```

## Solution

Wrap non-urgent updates in `startTransition`:

```typescript
import { startTransition, useTransition } from 'react';

// GOOD: UI stays responsive
const handleScroll = () => {
  startTransition(() => {
    setScrollY(window.scrollY);
  });
};

// GOOD: With pending state
const [isPending, startTransition] = useTransition();
const handleSearch = (query: string) => {
  setSearchQuery(query);  // Urgent - update input immediately
  startTransition(() => {
    setFilteredTree(filterTree(tree, query));  // Non-urgent
  });
};
```

## Example - Modal State Updates

```typescript
// BAD: Modal state updates block rendering
const handleMove = (pageId: string) => {
  setPageToMove({ pageId, title, projectId });
  dispatch(openModal({ ... }));
};

// GOOD: Mark modal updates as non-urgent
const [isPending, startTransition] = useTransition();
const handleMove = (pageId: string) => {
  startTransition(() => {
    setPageToMove({ pageId, title, projectId });
    dispatch(openModal({ ... }));
  });
};
```

## When to Use

- Scroll event handlers
- Search/filter operations
- Expanding/collapsing tree nodes
- Opening modals with complex content
- Any update that doesn't need immediate visual feedback
