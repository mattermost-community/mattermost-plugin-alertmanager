---
name: redux-patterns
description: Redux state management patterns for React applications. Use when working with Redux actions, reducers, selectors, thunks, and state architecture.
---

# Redux State Management Patterns

## When to Use This Skill

- Implementing Redux actions and reducers
- Creating selectors with reselect/memoization
- Working with async actions (thunks)
- Designing state shape and normalization
- Debugging Redux state issues
- Optimizing Redux performance

## Core Patterns

### Actions & Action Creators

```typescript
// Action types as const
export const FETCH_ITEMS_REQUEST = 'FETCH_ITEMS_REQUEST';
export const FETCH_ITEMS_SUCCESS = 'FETCH_ITEMS_SUCCESS';
export const FETCH_ITEMS_FAILURE = 'FETCH_ITEMS_FAILURE';

// Type-safe action creators
export interface FetchItemsRequestAction {
    type: typeof FETCH_ITEMS_REQUEST;
    parentId: string;
}

export interface FetchItemsSuccessAction {
    type: typeof FETCH_ITEMS_SUCCESS;
    data: Item[];
}

export type ItemsActionTypes =
    | FetchItemsRequestAction
    | FetchItemsSuccessAction
    | FetchItemsFailureAction;

// Action creator
export function fetchItemsSuccess(data: Item[]): FetchItemsSuccessAction {
    return {
        type: FETCH_ITEMS_SUCCESS,
        data,
    };
}
```

### Thunks (Async Actions)

```typescript
export function fetchItems(parentId: string): ThunkAction<void, GlobalState, unknown, AnyAction> {
    return async (dispatch, getState) => {
        dispatch({ type: FETCH_ITEMS_REQUEST, parentId });

        try {
            const items = await Client4.getItems(parentId);
            dispatch(fetchItemsSuccess(items));
        } catch (error) {
            dispatch({ type: FETCH_ITEMS_FAILURE, error });
        }
    };
}
```

### Reducers

```typescript
const initialState: ItemsState = {
    items: {},
    itemsByParent: {},
    loading: false,
    error: null,
};

export function itemsReducer(
    state = initialState,
    action: ItemsActionTypes
): ItemsState {
    switch (action.type) {
        case FETCH_ITEMS_REQUEST:
            return {
                ...state,
                loading: true,
                error: null,
            };

        case FETCH_ITEMS_SUCCESS:
            return {
                ...state,
                loading: false,
                items: {
                    ...state.items,
                    ...action.data.reduce((acc, item) => {
                        acc[item.id] = item;
                        return acc;
                    }, {} as Record<string, Item>),
                },
            };

        default:
            return state;
    }
}
```

### Selectors with Memoization

```typescript
import { createSelector } from 'reselect';

// Base selectors
export const getItemsState = (state: GlobalState) => state.entities.items;
export const getItems = (state: GlobalState) => getItemsState(state).items;

// Memoized selector
export const getItemsByParent = createSelector(
    [getItems, (state: GlobalState, parentId: string) => parentId],
    (items, parentId) => {
        return Object.values(items).filter(
            item => item.parentId === parentId
        );
    }
);

// Derived selector
export const getItemHierarchy = createSelector(
    [getItemsByParent],
    (items) => {
        const rootItems = items.filter(i => !i.parentItemId);
        return buildHierarchy(rootItems, items);
    }
);
```

### State Normalization

```typescript
// Normalized state shape
interface NormalizedState {
    entities: {
        items: Record<string, Item>;
        categories: Record<string, Category>;
        users: Record<string, User>;
    };
    ui: {
        selectedItemId: string | null;
        isLoading: boolean;
    };
    requests: {
        items: RequestStatus;
    };
}

// Entity adapter pattern
const itemsAdapter = createEntityAdapter<Item>({
    selectId: (item) => item.id,
    sortComparer: (a, b) => a.createAt - b.createAt,
});
```

## Best Practices

1. **Keep state normalized** - Use IDs as keys, avoid nested structures
2. **Derive data with selectors** - Don't store computed values
3. **Use memoized selectors** - Prevent unnecessary re-renders
4. **Immutable updates** - Always return new state objects
5. **Action naming** - Use NOUN_VERB pattern (ITEMS_FETCH_SUCCESS)
6. **Separate concerns** - UI state vs entity state vs request state
7. **Type everything** - Full TypeScript coverage for actions/state

## Common Patterns

### Standard Directory Structure
- Actions in `src/actions/`
- Reducers in `src/reducers/`
- Selectors in `src/selectors/`
- Types in `src/types/`

### Connect Pattern
```typescript
const mapStateToProps = (state: GlobalState, ownProps: OwnProps) => ({
    items: getItemsByParent(state, ownProps.parentId),
    loading: getItemsLoading(state),
});

const mapDispatchToProps = {
    fetchItems,
    createItem,
    updateItem,
};

export default connect(mapStateToProps, mapDispatchToProps)(ItemsList);
```

### Hooks Pattern (Modern)
```typescript
function ItemsList({ parentId }: Props) {
    const dispatch = useDispatch();
    const items = useSelector((state) => getItemsByParent(state, parentId));
    const loading = useSelector(getItemsLoading);

    useEffect(() => {
        dispatch(fetchItems(parentId));
    }, [dispatch, parentId]);

    return <div>{/* render items */}</div>;
}
```

---

## Redux Toolkit (RTK) Patterns

Modern Redux uses Redux Toolkit for less boilerplate and better DX.

### createSlice (Recommended)

```typescript
import { createSlice, PayloadAction } from '@reduxjs/toolkit';

interface ItemsState {
    items: Record<string, Item>;
    loading: boolean;
    error: string | null;
}

const initialState: ItemsState = {
    items: {},
    loading: false,
    error: null,
};

const itemsSlice = createSlice({
    name: 'items',
    initialState,
    reducers: {
        setLoading(state, action: PayloadAction<boolean>) {
            state.loading = action.payload;
        },
        itemReceived(state, action: PayloadAction<Item>) {
            state.items[action.payload.id] = action.payload;
        },
        itemsReceived(state, action: PayloadAction<Item[]>) {
            action.payload.forEach(item => {
                state.items[item.id] = item;
            });
        },
        itemRemoved(state, action: PayloadAction<string>) {
            delete state.items[action.payload];
        },
    },
});

export const { setLoading, itemReceived, itemsReceived, itemRemoved } = itemsSlice.actions;
export default itemsSlice.reducer;
```

### createAsyncThunk

```typescript
import { createAsyncThunk } from '@reduxjs/toolkit';

export const fetchItems = createAsyncThunk(
    'items/fetchItems',
    async (parentId: string, { rejectWithValue }) => {
        try {
            const items = await Client4.getItems(parentId);
            return items;
        } catch (error) {
            return rejectWithValue(error.message);
        }
    }
);

// Handle in slice with extraReducers:
const itemsSlice = createSlice({
    name: 'items',
    initialState,
    reducers: { /* ... */ },
    extraReducers: (builder) => {
        builder
            .addCase(fetchItems.pending, (state) => {
                state.loading = true;
                state.error = null;
            })
            .addCase(fetchItems.fulfilled, (state, action) => {
                state.loading = false;
                action.payload.forEach(item => {
                    state.items[item.id] = item;
                });
            })
            .addCase(fetchItems.rejected, (state, action) => {
                state.loading = false;
                state.error = action.payload as string;
            });
    },
});
```

### RTK Query (API Caching)

```typescript
import { createApi, fetchBaseQuery } from '@reduxjs/toolkit/query/react';

export const itemsApi = createApi({
    reducerPath: 'itemsApi',
    baseQuery: fetchBaseQuery({ baseUrl: '/api/v4' }),
    tagTypes: ['Item'],
    endpoints: (builder) => ({
        getItem: builder.query<Item, string>({
            query: (itemId) => `items/${itemId}`,
            providesTags: (result, error, id) => [{ type: 'Item', id }],
        }),
        updateItem: builder.mutation<Item, Partial<Item> & { id: string }>({
            query: ({ id, ...patch }) => ({
                url: `items/${id}`,
                method: 'PUT',
                body: patch,
            }),
            invalidatesTags: (result, error, { id }) => [{ type: 'Item', id }],
        }),
    }),
});

export const { useGetItemQuery, useUpdateItemMutation } = itemsApi;
```

### When to Use RTK vs Legacy

| Scenario | Recommendation |
|----------|----------------|
| New features | Use RTK (createSlice, createAsyncThunk) |
| Existing legacy code | Follow existing patterns for consistency |
| API-heavy features | Consider RTK Query |
| Simple state | createSlice is sufficient |
| Complex async flows | createAsyncThunk with extraReducers |
