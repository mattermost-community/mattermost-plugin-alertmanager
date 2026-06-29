# Combine Multiple Array Iterations

**Impact:** LOW-MEDIUM (reduces iterations)
**Tags:** javascript, performance, arrays, iteration

## Problem

Multiple `.filter()` or `.map()` calls iterate the array multiple times:

```typescript
// BAD: 3 passes through array
const published = pages.filter(p => p.isPublished);
const drafts = pages.filter(p => p.isDraft);
const archived = pages.filter(p => p.isArchived);

// BAD: 2 passes
const validPages = pages.filter(p => p.isValid).map(p => p.id);
```

## Solution

Combine into single iteration:

```typescript
// GOOD: 1 pass
const published: Page[] = [];
const drafts: Page[] = [];
const archived: Page[] = [];

for (const page of pages) {
  if (page.isPublished) published.push(page);
  if (page.isDraft) drafts.push(page);
  if (page.isArchived) archived.push(page);
}

// GOOD: Single pass with reduce
const validPageIds = pages.reduce<string[]>((acc, page) => {
  if (page.isValid) {
    acc.push(page.id);
  }
  return acc;
}, []);
```

## Example - Selectors

```typescript
// BAD: Current pattern in pages.ts
return pageIds
  .map((id) => posts[id])
  .filter((post) => Boolean(post) && post.type === PostTypes.PAGE);

// GOOD: Single pass
return pageIds.reduce<Post[]>((result, id) => {
  const post = posts[id];
  if (post && post.type === PostTypes.PAGE && post.state !== 'DELETED') {
    result.push(post);
  }
  return result;
}, []);
```

## When to Use

- Hot paths (selectors, frequently-called functions)
- Large arrays (100+ items)
- Multiple categorization operations
- Chained `.filter().map()` calls
