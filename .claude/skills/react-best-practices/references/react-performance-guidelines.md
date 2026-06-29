# React Performance Guidelines

Comprehensive performance optimization guide for React/TypeScript code.
Adapted from Vercel Engineering guidelines for modern web application development.

## Priority Order

Optimizations are ranked by impact:

1. **CRITICAL**: Async waterfalls, barrel imports
2. **HIGH**: Dynamic imports, Map/Set lookups
3. **MEDIUM**: Re-render optimization, effect dependencies
4. **LOW**: Combined iterations, early returns

## 1. CRITICAL: Eliminate Async Waterfalls

The #1 performance killer is sequential await operations.

### Pattern: Promise.all for Independent Operations

```typescript
// Impact: 2-10x faster

// BAD
const page = await getPage(id);
const project = await getProject(projectId);
const comments = await getComments(id);

// GOOD
const [page, project, comments] = await Promise.all([
  getPage(id),
  getProject(projectId),
  getComments(id),
]);
```

See: `rules/async-parallel.md`

## 2. CRITICAL: Bundle Size Optimization

### Pattern: Avoid Barrel Imports

```typescript
// Impact: 200-800ms faster imports

// BAD - loads entire library
import { Icon } from '@project/icons/components';

// GOOD - loads single icon
import { Icon } from '@project/icons/components/icon-name';
```

Common offenders:
- Icon libraries (e.g., `@project/icons/components`)
- `lodash`
- `date-fns`

See: `rules/bundle-barrel-imports.md`

## 3. HIGH: Data Structure Optimization

### Pattern: Map for O(1) Lookups

```typescript
// Impact: O(n) to O(1) per lookup

// BAD - O(n) per find
const page = pages.find(p => p.id === pageId);

// GOOD - O(1) lookup
const pageMap = new Map(pages.map(p => [p.id, p]));
const page = pageMap.get(pageId);
```

### Pattern: Set for Membership Checks

```typescript
// BAD - O(n) per includes
if (deletedIds.includes(id)) { ... }

// GOOD - O(1) per has
const deletedSet = new Set(deletedIds);
if (deletedSet.has(id)) { ... }
```

See: `rules/js-index-maps.md`, `rules/js-set-map-lookups.md`

## 4. MEDIUM: Re-render Prevention

### Pattern: Narrow Effect Dependencies

```typescript
// BAD - runs on any page change
useEffect(() => { ... }, [page]);

// GOOD - runs only when id changes
useEffect(() => { ... }, [page.id]);
```

### Pattern: Use Transitions

```typescript
// BAD - blocks UI
setSearchQuery(query);
setFilteredTree(filter(tree, query));

// GOOD - non-blocking
setSearchQuery(query);
startTransition(() => {
  setFilteredTree(filter(tree, query));
});
```

See: `rules/rerender-dependencies.md`, `rules/rerender-transitions.md`

## 5. LOW-MEDIUM: Iteration Optimization

### Pattern: Single-Pass Processing

```typescript
// BAD - 3 passes
const a = items.filter(x => x.a);
const b = items.filter(x => x.b);
const c = items.filter(x => x.c);

// GOOD - 1 pass
const a = [], b = [], c = [];
for (const item of items) {
  if (item.a) a.push(item);
  if (item.b) b.push(item);
  if (item.c) c.push(item);
}
```

See: `rules/js-combine-iterations.md`

## Common Application Patterns

### Redux Selectors

- Use `createSelector` for derived data
- Return stable references (avoid creating new objects)
- Use Maps for ID-based lookups in selectors

### Rich Text Editor

- Debounce autosave (500ms minimum)
- Lazy load advanced features
- Memoize toolbar configurations
- Cache editor extensions

### Hierarchical Data Display

- Use Maps for parent/child lookups
- Debounce search input
- Consider virtualization for 100+ items
- Memoize tree node components

## Quick Reference

| Pattern | Impact | When to Use |
|---------|--------|-------------|
| Promise.all | CRITICAL | Independent async ops |
| Direct imports | CRITICAL | Any icon/utility import |
| Map lookups | HIGH | Multiple .find() calls |
| Set membership | HIGH | Multiple .includes() |
| Primitive deps | MEDIUM | useEffect/useCallback |
| startTransition | MEDIUM | Non-urgent updates |
| Single-pass | LOW | Multiple filters |
