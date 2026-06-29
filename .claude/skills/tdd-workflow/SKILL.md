---
name: tdd-workflow
description: Test-Driven Development workflow for Go and TypeScript. Use when implementing new features or fixing bugs using RED-GREEN-REFACTOR cycle.
---

# Test-Driven Development Workflow

## When to Use This Skill

- Implementing new features
- Fixing bugs with regression tests
- Refactoring with confidence
- Building APIs with clear contracts

## The RED-GREEN-REFACTOR Cycle

### 1. RED: Write a Failing Test First

**Before writing any production code**, write a test that:
- Describes the expected behavior
- Fails because the feature doesn't exist yet
- Provides a clear error message

```go
// Go example
func TestCreatePage_WithValidInput_ReturnsPage(t *testing.T) {
    th := Setup(t).InitBasic()
    defer th.TearDown()

    page, appErr := th.App.CreatePage(th.Context, &model.Page{
        ChannelId: th.BasicChannel.Id,
        Title:     "Test Page",
        UserId:    th.BasicUser.Id,
    })

    require.Nil(t, appErr)
    require.NotEmpty(t, page.Id)
    require.Equal(t, "Test Page", page.Title)
}
```

```typescript
// TypeScript example
describe('PageEditor', () => {
    it('saves content when save button clicked', async () => {
        const onSave = jest.fn();
        render(<PageEditor onSave={onSave} />);

        await userEvent.type(screen.getByRole('textbox'), 'New content');
        await userEvent.click(screen.getByRole('button', { name: /save/i }));

        expect(onSave).toHaveBeenCalledWith(
            expect.objectContaining({ content: 'New content' })
        );
    });
});
```

### 2. GREEN: Write Minimal Code to Pass

Write the **simplest possible code** that makes the test pass:
- Don't optimize yet
- Don't handle edge cases yet
- Just make it work

```go
func (a *App) CreatePage(ctx request.CTX, page *model.Page) (*model.Page, *model.AppError) {
    page.Id = model.NewId()
    page.CreateAt = model.GetMillis()

    savedPage, err := a.Srv().Store().Page().Save(page)
    if err != nil {
        return nil, model.NewAppError("CreatePage", "app.page.save.error", nil, "", http.StatusInternalServerError)
    }

    return savedPage, nil
}
```

### 3. REFACTOR: Improve the Code

Now that tests pass, improve the code:
- Remove duplication
- Improve naming
- Extract methods
- **Run tests after each change**

## TDD Anti-Patterns to Avoid

### ❌ Writing Tests After Code
Tests written after are biased toward implementation, not behavior.

### ❌ Testing Implementation Details
```typescript
// BAD: Tests internal state
expect(component.state.isLoading).toBe(true);

// GOOD: Tests observable behavior
expect(screen.getByRole('progressbar')).toBeInTheDocument();
```

### ❌ Skipping the RED Phase
If the test passes immediately, either:
- The feature already exists
- The test is wrong

### ❌ Writing Too Much Code in GREEN
Only write enough to pass the current test.

### ❌ Skipping Refactor
Technical debt accumulates without refactoring.

## Go Testing Patterns

### Table-Driven Tests
```go
func TestValidatePageTitle(t *testing.T) {
    tests := []struct {
        name    string
        title   string
        wantErr bool
    }{
        {"valid title", "My Page", false},
        {"empty title", "", true},
        {"too long", strings.Repeat("a", 300), true},
        {"with emoji", "Page 🎉", false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validatePageTitle(tt.title)
            if tt.wantErr {
                require.Error(t, err)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

### Subtests for Setup Reuse
```go
func TestPageStore(t *testing.T) {
    th := Setup(t)
    defer th.TearDown()

    t.Run("Save", func(t *testing.T) {
        // test save
    })

    t.Run("Get", func(t *testing.T) {
        // test get
    })
}
```

## TypeScript Testing Patterns

### Arrange-Act-Assert
```typescript
it('displays error when save fails', async () => {
    // Arrange
    const mockSave = jest.fn().mockRejectedValue(new Error('Network error'));
    render(<PageEditor onSave={mockSave} />);

    // Act
    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    // Assert
    expect(await screen.findByText(/failed to save/i)).toBeInTheDocument();
});
```

### Testing Async Behavior
```typescript
it('loads page content on mount', async () => {
    render(<PageView pageId="123" />);

    // Wait for loading to complete
    await waitForElementToBeRemoved(() => screen.queryByRole('progressbar'));

    expect(screen.getByText('Page Content')).toBeInTheDocument();
});
```

## Workflow Commands

```bash
# Go: Run specific test
go test ./channels/app -run TestCreatePage -v

# Go: Run with race detection
go test ./channels/app -race -run TestCreatePage

# TypeScript: Watch mode
npm test -- --watch

# TypeScript: Single test
npm test -- --testNamePattern="PageEditor"
```

## Remember

1. **Test behavior, not implementation**
2. **One assertion per test** (when possible)
3. **Tests are documentation** - make them readable
4. **Fast tests run more often** - keep them quick
5. **Delete tests that don't add value**
