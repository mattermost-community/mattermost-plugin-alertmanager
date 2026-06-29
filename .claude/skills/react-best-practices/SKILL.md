---
name: react-best-practices
description: React/TypeScript performance optimization guidelines
version: 1.0.0
triggers:
  - review react performance
  - optimize component
  - audit react code
  - check re-renders
  - bundle optimization
---

# React Best Practices

Performance optimization guidelines for React/TypeScript code.
Adapted from [Vercel Engineering's React Performance Guidelines](https://github.com/vercel-labs/agent-skills).

## When to Use This Skill

Activate when:
- Reviewing or writing React components
- Optimizing slow components or pages
- Auditing for performance issues
- Code review for frontend changes

## Priority-Ordered Guidelines

### 1. CRITICAL: Eliminate Async Waterfalls

**Impact: 2-10x performance improvement**

```typescript
// BAD: Sequential fetches
const data = await getData(id);
const children = await getChildren(id);
const comments = await getComments(id);

// GOOD: Parallel fetches
const [data, children, comments] = await Promise.all([
  getData(id),
  getChildren(id),
  getComments(id),
]);
```

See: `references/rules/async-parallel.md`

### 2. CRITICAL: Avoid Barrel Imports

**Impact: 200-800ms import cost, slow builds**

```typescript
// BAD: Barrel imports
import { CheckIcon, XIcon } from 'lucide-react';

// GOOD: Direct imports
import CheckIcon from 'lucide-react/dist/esm/icons/check';
import XIcon from 'lucide-react/dist/esm/icons/x';
```

**Common offenders:**
- `lucide-react` - Use direct icon imports
- `lodash` - Use `lodash/functionName` not `lodash`
- `date-fns` - Use direct function imports

See: `references/rules/bundle-barrel-imports.md`

### 3. HIGH: Dynamic Imports for Heavy Components

**Impact: Reduces initial bundle, improves TTI**

```typescript
// BAD: Static import of heavy component
import { MonacoEditor } from 'monaco-editor';

// GOOD: Dynamic import
const MonacoEditor = React.lazy(() => import('monaco-editor'));
```

### 4. MEDIUM: Re-render Optimization

#### 4a. Extract Memoized Components

```typescript
// BAD: useMemo inside component still runs
function ItemList({ items, isLoading }) {
  const sortedItems = useMemo(() => expensiveSort(items), [items]);
  if (isLoading) return <Loading />;
  return <List items={sortedItems} />;
}

// GOOD: Separate component enables early return
function ItemList({ items, isLoading }) {
  if (isLoading) return <Loading />;
  return <SortedItemList items={items} />;
}

const SortedItemList = memo(({ items }) => {
  const sortedItems = useMemo(() => expensiveSort(items), [items]);
  return <List items={sortedItems} />;
});
```

#### 4b. Narrow Effect Dependencies

```typescript
// BAD: Effect runs on any item change
useEffect(() => {
  loadComments(item.id);
}, [item]);

// GOOD: Effect only runs when ID changes
useEffect(() => {
  loadComments(itemId);
}, [itemId]);
```

#### 4c. Use Transitions for Non-Urgent Updates

```typescript
// BAD: Immediate state update blocks UI
const handleScroll = () => setScrollPosition(window.scrollY);

// GOOD: Transition allows React to prioritize
const handleScroll = () => {
  startTransition(() => setScrollPosition(window.scrollY));
};
```

See: `references/rules/rerender-*.md`

### 5. MEDIUM: JavaScript Performance

#### 5a. Build Index Maps for Repeated Lookups

```typescript
// BAD: O(n) lookup for each item
items.forEach(item => {
  const author = users.find(u => u.id === item.authorId);
});

// GOOD: O(1) lookup after O(n) map creation
const userMap = new Map(users.map(u => [u.id, u]));
items.forEach(item => {
  const author = userMap.get(item.authorId);
});
```

#### 5b. Use Set for Membership Checks

```typescript
// BAD: O(n) per check
const isSelected = selectedIds.includes(itemId);

// GOOD: O(1) per check
const selectedSet = new Set(selectedIds);
const isSelected = selectedSet.has(itemId);
```

#### 5c. Combine Array Iterations

```typescript
// BAD: 3 passes through array
const published = items.filter(i => i.isPublished);
const drafts = items.filter(i => i.isDraft);
const archived = items.filter(i => i.isArchived);

// GOOD: Single pass
const published: Item[] = [];
const drafts: Item[] = [];
const archived: Item[] = [];
for (const item of items) {
  if (item.isPublished) published.push(item);
  if (item.isDraft) drafts.push(item);
  if (item.isArchived) archived.push(item);
}
```

See: `references/rules/js-*.md`

### 6. LOW-MEDIUM: Rendering Optimization

#### 6a. Hoist Static JSX

```typescript
// BAD: Recreated every render
function Editor() {
  const loadingSkeleton = <EditorSkeleton lines={10} />;
  // ...
}

// GOOD: Created once
const LOADING_SKELETON = <EditorSkeleton lines={10} />;
function Editor() {
  // Use LOADING_SKELETON
}
```

See: `references/rules/rendering-*.md`

## Common Patterns

### Redux Selector Optimization

```typescript
// BAD: Creates new object every call
const getItemWithChildren = (state, itemId) => ({
  item: state.items[itemId],
  children: Object.values(state.items).filter(i => i.parentId === itemId),
});

// GOOD: Use reselect with proper memoization
const getItemWithChildren = createSelector(
  [getItem, getItemChildren],
  (item, children) => ({ item, children })
);
```

### Rich Text Editor Performance

When working with rich text editors (TipTap, ProseMirror, etc.):
- Debounce autosave operations
- Use `useMemo` for toolbar configurations
- Lazy load heavy modules
- Avoid re-creating editor extensions on every render

### Tree/Hierarchy Views

For large tree structures:
- Virtualize long lists (react-window or similar)
- Use `content-visibility: auto` for off-screen nodes
- Memoize tree node components
- Use index maps for parent/child lookups

## Audit Checklist

When reviewing React code:

- [ ] **Barrel imports**: Check lucide-react, lodash, date-fns imports
- [ ] **Async waterfalls**: Look for sequential await statements
- [ ] **Effect dependencies**: Ensure primitives, not objects
- [ ] **Repeated lookups**: Convert arrays to Maps for O(1) access
- [ ] **Multiple filters**: Combine into single iteration
- [ ] **Static JSX**: Hoist unchanging elements
- [ ] **Heavy components**: Consider lazy loading

## References

Full rule documentation in `references/rules/`:
- `async-*.md` - Async optimization patterns
- `bundle-*.md` - Bundle size optimization
- `rerender-*.md` - Re-render prevention
- `js-*.md` - JavaScript performance
- `rendering-*.md` - DOM rendering optimization
