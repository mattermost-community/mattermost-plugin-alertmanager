# Avoid Barrel File Imports

**Impact:** CRITICAL (200-800ms import cost, slow builds)
**Tags:** bundle, imports, tree-shaking, barrel-files, performance

## Problem

Barrel files re-export multiple modules, defeating tree-shaking:

```typescript
// BAD: Loads entire icon library
import { CheckIcon, XIcon, Menu } from 'lucide-react';
// Loads 1,583 modules, ~2.8s extra in dev

import { Button, TextField } from '@mui/material';
// Loads 2,225 modules, ~4.2s extra in dev
```

## Solution

Import directly from source files:

```typescript
// GOOD: Only loads what you need
import CheckIcon from 'lucide-react/dist/esm/icons/check';
import Button from '@mui/material/Button';
```

## Common Issues

### Icon Libraries

```typescript
// BAD: Barrel import
import {PlusIcon, TrashCanOutlineIcon} from '@project/icons/components';

// GOOD: Direct imports
import {PlusIcon} from '@project/icons/components/plus';
import {TrashCanOutlineIcon} from '@project/icons/components/trash-can-outline';
```

### lodash

```typescript
// BAD
import { debounce, throttle } from 'lodash';

// GOOD
import debounce from 'lodash/debounce';
import throttle from 'lodash/throttle';
```

### date-fns

```typescript
// BAD
import { format, parseISO } from 'date-fns';

// GOOD
import format from 'date-fns/format';
import parseISO from 'date-fns/parseISO';
```

## Performance Impact

- 15-70% faster dev boot
- 28% faster builds
- 40% faster cold starts
- Noticeably faster HMR
