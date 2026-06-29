---
name: add-frontend-tests
description: Systematically find frontend test coverage gaps and add exhaustive Playwright unit and component tests. Use when you want to improve React/TypeScript test coverage, add missing tests, or harden existing test suites.
---

# Add Frontend Tests

Systematically analyze frontend coverage, identify gaps, and add exhaustive tests that follow project patterns.

## Workflow

This skill uses a plan-then-execute workflow:

1. **Enter plan mode** using EnterPlanMode
2. **Measure coverage** (Step 1) and **analyze gaps** (Step 2) while in plan mode
3. **Write the plan** to the plan file with the prioritized list of files and the tests you will add
4. **Build a task list** using TaskCreate with one task per test file you will create or modify
5. **Exit plan mode** using ExitPlanMode to get user approval
6. **Implement** (Steps 3-6) after approval, marking tasks complete with TaskUpdate as you go

## Step 1: Measure Current Coverage (in plan mode)

Run the frontend coverage target (typically `make coverage-frontend`) and capture the output. A typical setup has three coverage reports: unit test coverage, component test (CT) coverage, and a merged report combining both.

```bash
make coverage-frontend 2>&1
```

Typical commands this runs:
- `npm run test:coverage`: unit tests (`.spec.ts`) with C8 line coverage
- `npm run test:pw-ct-coverage`: component tests (`.pw.tsx`) with V8 browser coverage
- `npm run test:coverage-merged`: combines both into a single authoritative report

**CI environment warning.** Component test coverage collection may be skipped when `CI=true`. If you see the merged report showing very low coverage, check whether CT coverage was actually collected. If it was skipped, warn the user that the merged numbers only reflect unit test coverage.

### Coverage Threshold Check

**Two-gate exit check using the MERGED report.** Only stop if BOTH gates pass:

1. **Overall gate**: total line coverage in the merged report is >= 90%.
2. **Per-file floor gate**: no individual source file is below 80% line coverage. When evaluating this gate, exclude test files and test utilities: any `*.pw.tsx`, any `*.spec.ts`, and anything under `test-utils/`.

If both gates pass, report the merged coverage number plus confirmation that every non-test source file is at or above the 80% floor, congratulate the user, and exit plan mode.

If either gate fails, proceed to build the priority list below.

### Build Priority List (if either gate fails)

Parse the coverage output to build a prioritized list:
- **Tier 1**: Files/functions at 0% coverage (completely untested)
- **Tier 2**: Files/functions below 60% coverage (significant gaps)
- **Tier 3**: Files/functions below 80% coverage (moderate gaps)
- **Tier 4**: Complex components above 80% with untested edge cases

Skip test infrastructure files.

## Step 2: Understand What Needs Testing (in plan mode)

For each file in your priority list:

1. **Read the source file** to understand the component's logic, props, state, effects, and event handlers
2. **Read the existing test file** (if one exists) to see what's already covered
3. **Identify untested paths**: error states, loading states, empty/null props, user interactions, API failures, edge cases
4. **Note dependencies**: what needs mocking (API routes, store state, browser APIs)

Build a concrete test plan before writing any code. For each test, know:
- The specific behavior/state being tested
- The setup required (mounts, route mocks, props)
- The assertion that proves the behavior was exercised

### Write Plan and Build Task List

Write your plan to the plan file with:
- Current coverage baseline (overall and per-file)
- Prioritized list of files grouped by tier
- For each file: the specific tests you will add and which test file they go in

Then create tasks using TaskCreate with one task per test file.

Exit plan mode and wait for user approval before proceeding to Step 3.

## Step 3: Write Tests Using Project Patterns

### Two Test Types

Projects typically use two distinct test approaches:

**Unit Tests (`.spec.ts`)**, run in Node.js via Playwright Test:
- For pure logic, utility functions, state management, API clients
- File pattern: `src/<module>.spec.ts`
- Run: `npm run test:pw`

**Component Tests (`.pw.tsx`)**, run in browser via Playwright Component Testing:
- For React components with real DOM rendering
- File pattern: `src/components/<Component>.pw.tsx`
- Run: `npm run test:pw-ct`

### Test Infrastructure

**Store reset for test isolation:**
```typescript
import {myStore} from '../store/my_store';

test.beforeEach(() => {
    myStore._resetForTesting();
});
```

If a store lacks a `_resetForTesting()` export, add one. Each store should clear its module-level state and listener arrays.

**Route mocking for API calls:**
```typescript
await page.route('**/plugins/<plugin-id>/api/**', (route) => {
    route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(responseData),
    });
});
```

**Error route mocking:**
```typescript
await page.route('**/plugins/<plugin-id>/api/**', (route) => {
    route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({error: 'Internal server error'}),
    });
});

// Network failure simulation
await page.route('**/plugins/<plugin-id>/api/**', (route) => {
    route.abort('connectionrefused');
});
```

**Fetch mocking for unit tests:**
```typescript
const originalFetch = globalThis.fetch;
globalThis.fetch = (async () => ({
    ok: true,
    json: async () => mockData,
})) as typeof globalThis.fetch;

// Always restore in afterEach:
test.afterEach(() => {
    globalThis.fetch = originalFetch;
});
```

### Testing Patterns to Follow

**Hierarchical test organization:**
```typescript
test.describe('ComponentName', () => {
    test.describe('rendering', () => {
        test('displays title', async ({mount}) => { ... });
        test('shows loading state', async ({mount}) => { ... });
    });
    test.describe('interactions', () => {
        test('handles click', async ({mount, page}) => { ... });
    });
    test.describe('error handling', () => {
        test('shows error on API failure', async ({mount, page}) => { ... });
    });
});
```

**Component mount and assert:**
```typescript
test('renders with props', async ({mount, page}) => {
    await mount(<MyComponent data={testData} />);
    await expect(page.getByText('Expected Text')).toBeVisible();
});
```

**Reactive update testing:**
```typescript
test('updates when props change', async ({mount, page}) => {
    const component = await mount(<MyComponent value="" />);
    await expect(page.getByTestId('indicator')).toBeHidden();

    await component.update(<MyComponent value="new-value" />);
    await expect(page.getByTestId('indicator')).toBeVisible();
});
```

**Locator scoping for bare elements:**
```typescript
// When a component returns a bare element (e.g., <a> or <span> with no wrapper div),
// component.locator('a') searches INSIDE the component root.
// Use page.locator('a') instead to find the root element itself:
await mount(<MyLink href="/test" />);
await expect(page.locator('a')).toHaveAttribute('href', '/test');
```

### What Makes a Good Frontend Test

- **Tests user-visible behavior**: assert on what the user sees (text, visibility, enabled/disabled), not internal state
- **One behavior per test**: each test verifies one specific interaction or state
- **Descriptive names**: `'shows error message when API returns 500'` not `'test error 1'`
- **Isolated**: each test mounts its own component, no shared state between tests
- **No flaky selectors**: prefer `getByRole`, `getByText`, `getByTestId` over CSS selectors
- **Proper cleanup**: restore mocked globals in `test.afterEach()`, call `_resetForTesting()` in `test.beforeEach()`
- **No synthetic data to fix failures**: if a test fails, fix the code or the test logic

### What to Test in Each Component

For every component, aim to cover:

1. **Initial render**: correct elements visible with default/provided props
2. **Empty/null states**: empty arrays, null values, undefined props, empty strings
3. **Loading states**: spinners, placeholders, skeleton UI
4. **Error states**: API failures, network errors, invalid data
5. **User interactions**: clicks, form inputs, modal open/close, keyboard events
6. **Reactive updates**: prop changes, state changes, subscription callbacks
7. **Edge cases**: malformed JSON, very long strings, special characters, rapid interactions

For utility modules:

1. **Happy path**: normal successful execution
2. **Error handling**: thrown errors, rejected promises, invalid inputs
3. **Concurrent operations**: multiple calls, race conditions, cleanup during pending operations
4. **State management**: subscription lifecycle, cleanup, memory leaks

### File Organization

Add tests to existing test files or create new ones following the pattern:
- Unit tests: `src/foo.ts` to `src/foo.spec.ts`
- Component tests: `src/components/Foo.tsx` to `src/components/Foo.pw.tsx`

## Step 4: Implement in Phases (after plan approval)

Work through tiers in order. Mark each task complete using TaskUpdate as you finish it. After each phase, validate before moving on.

**Phase 1, Quick wins (0% coverage, simple modules):**
Utility functions, formatters, and stores with no tests at all. Start with the simplest ones to build momentum.

**Phase 2, Component tests (low coverage components):**
Components that have some tests but miss important states (error, loading, empty).

**Phase 3, Interaction and integration tests:**
Complex user flows, admin settings changes, form validation, multi-step interactions, API call chains.

**Phase 4, Edge cases and error resilience:**
Malformed JSON inputs, network failures, rapid user interactions, concurrent state updates, cleanup during unmount.

**Phase 5, Store isolation improvements (if needed):**
If a store lacks a `_resetForTesting()` export, add one following the existing pattern.

## Step 5: Validate After Each Phase

After writing each batch of tests:

```bash
# Unit tests pass
cd webapp && npm run test:pw

# Component tests pass
cd webapp && npm run test:pw-ct

# No lint issues
make check-style

# Coverage improved
make coverage-frontend 2>&1
```

Compare the merged coverage numbers against the baseline from Step 1. If a file you targeted still shows low coverage, your tests aren't exercising the right code paths.

## Step 6: Final Verification

After all phases complete:

```bash
make test
make check-style
make coverage-frontend 2>&1
```

Report the before/after coverage delta per file and overall from the merged coverage report.

## Common Pitfalls

- **Not restoring mocked globals**: if you mock `globalThis.fetch` or `console.warn`, always restore in `test.afterEach()`. Leaked mocks cause cascading test failures.
- **Forgetting `_resetForTesting()` in `beforeEach`**: store state leaks between tests and causes nondeterministic failures.
- **Not reading existing tests first**: you'll write duplicates or miss established helper functions.
- **Flaky async assertions**: use `await expect(locator).toBeVisible()` (auto-retrying) instead of checking once.
- **Forgetting route mocks**: component tests that make API calls will fail or hang without `page.route()` setup.
- **Using wrong plugin ID in route mocks**: match the plugin ID from `plugin.json`. API calls will not be intercepted with the wrong path.
- **CSS selector fragility**: prefer semantic locators (`getByRole`, `getByText`, `getByTestId`) over CSS class selectors.
- **Missing cleanup in afterEach**: tracked state must be cleaned up to prevent test pollution.
- **Testing implementation details**: don't assert on internal state or React hooks. Test what the user sees and what callbacks fire.
- **Locator scoping mistakes**: when a component returns a bare element, use `page.locator()` not `component.locator()` to find the root element itself.
