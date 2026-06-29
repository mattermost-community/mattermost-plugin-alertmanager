---
name: add-backend-tests
description: Systematically find Go backend test coverage gaps and add exhaustive unit/integration tests. Use when you want to improve Go test coverage, add missing tests, or harden existing test suites.
---

# Add Backend Tests

Systematically analyze Go backend coverage, identify gaps, and add exhaustive tests that follow project patterns.

## Workflow

This skill uses a plan-then-execute workflow:

1. **Enter plan mode** using EnterPlanMode
2. **Measure coverage** (Step 1) and **analyze gaps** (Step 2) while in plan mode
3. **Write the plan** to the plan file with the prioritized list of functions and the tests you will add
4. **Build a task list** using TaskCreate with one task per test file you will create or modify
5. **Exit plan mode** using ExitPlanMode to get user approval
6. **Implement** (Steps 3-6) after approval, marking tasks complete with TaskUpdate as you go

## Step 1: Measure Current Coverage (in plan mode)

Run the coverage target (typically `make coverage-backend` or `make coverage`) and capture the output. This runs `go test -coverprofile` then `go tool cover -func` to show per-function coverage.

```bash
make coverage-backend 2>&1
```

### Coverage Threshold Check

Use a **two-gate check**. Exit the skill only if BOTH conditions are met:

1. **Overall coverage is 90% or above**, AND
2. **No individual source file is below 80% statements**

If either gate fails, proceed to build the priority list below. A high overall average can mask serious gaps because `go tool cover` reports an unweighted mean across functions (lots of small 100% functions pull the average up and hide low-coverage files).

To check the per-file gate, aggregate the per-function output from `go tool cover -func` by source file: for each `.go` file, compute the statement-weighted coverage across its functions. Any non-test source file below 80% fails the gate.

**Excluded from the per-file check** (these should not block the exit gate):
- `*_test.go` (test files do not appear in coverage output anyway)
- Generated files: `server/manifest.go`, any file with `// Code generated` header
- `main.go` entrypoints and trivial files with only constants or package-level vars

When you exit because both gates pass, report the overall percentage and confirm no file is below the per-file floor. When you proceed because a file is below 80%, list the offending files and continue to the priority list.

### Build Priority List (if either gate fails)

Parse the output to build a prioritized list. Within each tier, sort by **file size descending** (largest uncovered files first) so the biggest coverage gains come earliest.

**Identifying large zero-coverage files:**
After parsing the coverage output, group functions by source file. For each file, count the number of 0%-coverage functions and note total function count. Files with many uncovered functions represent the highest-value targets because a single test file can cover many functions at once.

**Priority tiers (work top to bottom, largest files first within each tier):**
- **Tier 1, Large zero-coverage files**: Source files where **all or most functions are at 0%** and the file contains 3+ functions. These are the highest priority because they yield the biggest coverage gains.
- **Tier 2, Remaining 0% functions**: Individual functions at 0% in files that otherwise have partial coverage.
- **Tier 3**: Functions below 60% coverage (significant gaps)
- **Tier 4**: Functions below 80% coverage (moderate gaps)
- **Tier 5**: Complex functions above 80% that have untested edge cases

Skip trivial functions (main, manifest constants, simple getters under 5 lines).

## Step 2: Understand What Needs Testing (in plan mode)

For each function in your priority list:

1. **Read the source file** to understand the function's logic, branches, and error paths
2. **Read the existing test file** (if one exists) to see what's already covered
3. **Identify untested paths**: error returns, edge cases, branch conditions, concurrent scenarios
4. **Note dependencies**: what needs mocking (API calls, KV store, plugin API)

Build a concrete test plan before writing any code. For each test, know:
- The specific branch/path being tested
- The setup required (mocks, test data)
- The assertion that proves the path was exercised

### Write Plan and Build Task List

Write your plan to the plan file with:
- Current coverage baseline (overall and per-package)
- Prioritized list of functions grouped by tier
- For each function: the specific tests you will add and which test file they go in

Then create tasks using TaskCreate with one task per test file.

Exit plan mode and wait for user approval before proceeding to Step 3.

## Step 3: Write Tests Using Project Patterns

### Test Infrastructure

Mattermost plugins typically use `github.com/mattermost/mattermost/server/public/plugin/plugintest` for mocking the plugin API:

```go
api := &plugintest.API{}
api.On("LogError", mock.Anything, mock.Anything, mock.Anything).Return()
api.On("GetTeamByName", "team-a").Return(&model.Team{Id: "team-id"}, nil)
defer api.AssertExpectations(t)

p := &Plugin{}
p.SetAPI(api)
```

**HTTP handler testing:**
```go
req := httptest.NewRequest(http.MethodGet, "/api/v1/example", nil)
req.Header.Set("Mattermost-User-ID", "user123")
w := httptest.NewRecorder()

p.ServeHTTP(nil, w, req)

assert.Equal(t, http.StatusOK, w.Code)
```

**KV store mocking**: if the plugin defines its own KV store interface, implement a test double that satisfies the interface and override only the methods the function under test actually calls. Interface-based KV stores are much easier to mock than calling `p.API.KVSet` directly.

### Testing Patterns to Follow

**Subtests for related scenarios:**
```go
func TestFunctionName(t *testing.T) {
    t.Run("success case", func(t *testing.T) { ... })
    t.Run("error from dependency", func(t *testing.T) { ... })
    t.Run("edge case description", func(t *testing.T) { ... })
}
```

**Table-driven tests for input variations (primary pattern):**
```go
tests := []struct {
    name    string
    input   string
    want    string
    wantErr bool
}{
    {"valid input", "hello", "HELLO", false},
    {"empty input", "", "", true},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got, err := Transform(tt.input)
        if tt.wantErr {
            require.Error(t, err)
        } else {
            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        }
    })
}
```

### What Makes a Good Test

- **Tests behavior, not implementation**: assert on observable outcomes (HTTP status, return values, mock expectations), not internal state
- **One logical assertion per subtest**: test one path/branch per t.Run
- **Descriptive names**: `TestHandleFoo_NotFound_Returns404` not `TestHandleFoo3`
- **Isolated**: each subtest sets up its own state, no shared mutable state between subtests
- **Fast**: prefer mocks over real I/O
- **No synthetic/mock data to fix failures**: if a test fails, fix the code or the test logic, never fabricate data

### What to Test in Each Function

For every function, aim to cover:

1. **Happy path**: normal successful execution
2. **Each error return**: every `return err` or `return nil, err` branch
3. **Nil/empty inputs**: nil pointers, empty strings, empty slices
4. **Boundary conditions**: exact limits, off-by-one, size thresholds
5. **Permission checks**: unauthorized user (missing Mattermost-User-ID header)
6. **API error paths**: plugin API returning errors, KV store failures

### File Organization

Add tests to `*_test.go` files next to the source. The standard mapping is `server/foo.go` to `server/foo_test.go`.

## Step 4: Implement in Phases (after plan approval)

Work through tiers in order. Mark each task complete using TaskUpdate as you finish it. After each phase, validate before moving on.

**Phase 1, Large zero-coverage files (Tier 1, biggest coverage gains):**
Start with the largest completely-untested files. Write a full test file for each, covering happy paths and primary error branches for every exported function.

**Phase 2, Remaining 0% functions (Tier 2):**
Individual uncovered functions in files that already have partial test coverage. Add subtests to the existing test file following its established mock patterns.

**Phase 3, KV store and dependency mocks:**
Functions that read/write via the plugin API or a KV store interface. Create mock implementations that override only the methods the function under test actually calls.

**Phase 4, Branch coverage (Tiers 3-4, low coverage functions):**
Functions that have tests but miss important branches. Read existing tests carefully to avoid duplication.

**Phase 5, Edge cases and complex logic:**
Message parsing (malformed input, boundary conditions), API handlers (concurrent requests, large payloads), configuration changes (nil config, invalid values).

**Phase 6, Interface extraction for testability (if needed):**
If a dependency uses a concrete type that cannot be mocked, extract an interface to enable unit testing.

## Step 5: Validate After Each Phase

After writing each batch of tests:

```bash
# Tests compile and pass
make test

# No lint issues (fixes import formatting)
make check-style

# Coverage improved
make coverage-backend 2>&1
```

Compare coverage numbers against the baseline from Step 1. If a function you targeted is still at 0%, your test isn't exercising the right code path. Re-read the source and fix.

## Step 6: Final Verification

After all phases complete:

```bash
make test
make check-style
make coverage-backend 2>&1
```

Report the before/after coverage delta per package and overall.

## Common Pitfalls

- **Testing the mock, not the code**: ensure your mock setup actually forces the code path you intend. If a mock returns nil where the real code would return data, you may be testing a different branch.
- **Forgetting `defer api.AssertExpectations(t)`**: without this, unmet mock expectations silently pass.
- **Not reading existing tests first**: you'll write duplicates or miss established patterns.
- **Over-mocking**: if a function is pure logic with no dependencies, test it directly without mocks.
- **Ignoring goroutine cleanup**: any test that spawns goroutines must cancel the context and wait for completion to avoid test pollution.
- **Flaky time-dependent tests**: use short, generous timeouts in tests. Prefer channels and synchronization over `time.Sleep`.
