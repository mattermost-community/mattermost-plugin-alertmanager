# Search-First Workflow

**RULE**: If you're about to add something that "should probably exist already" → SEARCH FIRST

## Mandatory Pre-Implementation Searches

### 1. Constants/Types
Before adding ANY constant, type, or variable:
```bash
# Search for the concept
grep -r "DataSource\|data.*source" .
# Search for similar naming patterns
grep -r "const.*Source\|Source.*=" .
# Check target file for related constants
```

### 2. Functions/Methods
Before implementing ANY function:
```bash
# Search for similar function names
grep -r "functionPattern" .
# Search for the functionality
grep -r "what it does" .
# Check interfaces and existing implementations
```

### 3. Configuration/Settings
Before adding config fields:
- Search existing config structs and constants
- Check environment variable patterns
- Look for similar feature toggles

## Workflow

```
BEFORE: Adding new code
STEP 1: Search codebase for existing implementation
STEP 2: If found → use existing, extend if needed
STEP 3: If not found → proceed with implementation
NEVER: Skip the search step
```

## RED FLAGS - Stop and Search
- Adding constants to files with 100+ existing constants
- Creating types that sound like they might exist (DataSource, Protocol, Config)
- Implementing "basic" or "common" functionality (likely exists)
- Adding environment variables or config fields
