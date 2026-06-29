---
name: add-slash-cmds
description: Use when the plugin's REST endpoints in server/api.go (or equivalent) have grown out of sync with its slash commands in server/command.go. Surveys both files, identifies API operations that lack a corresponding slash command, and suggests new subcommands to add. Trigger after adding new REST endpoints, during periodic audits, or before cutting a release.
---

# Add Slash Commands

Find API endpoints in `server/api.go` (or wherever routes are registered) that have no corresponding slash command in `server/command.go`, then suggest and implement new subcommands to close the gap. Plugins generally follow a dual-interface pattern where most user-facing operations should be accessible via both the REST API and slash commands.

## When to Use

- After adding new REST endpoints without adding matching slash commands
- During a periodic audit of API-vs-command parity
- Before cutting a release, to ensure all user-facing operations have a slash command
- When a user reports that an operation is only available via the API and not the command line

## When NOT to Use

- For endpoints that are purely internal (action button handlers, dialog callbacks, interactive message button handlers)
- For bulk/admin-only query endpoints that do not map to a single-team or single-channel action
- When the API endpoint is only called by the webapp and has no meaningful CLI equivalent

## Workflow

Run these steps in order. Survey first, build the gap analysis, write and confirm a plan with the user, then implement.

### Task tracking

This workflow is driven by the harness task list. Do not work without a task.

1. Start by calling `TaskList` to check for pre-existing tasks from a prior run. Reuse or delete stale tasks instead of duplicating.
2. After completing Step 1 (Survey) and Step 3 (Write and confirm plan), use `TaskCreate` to create one task per new slash command identified in the approved plan.
3. Walk the task list top-to-bottom. For each task: call `TaskUpdate` with `status: "in_progress"` **before** editing any file, do the work, then call `TaskUpdate` with `status: "completed"` only after the step's checks pass.
4. Never hold more than one task in `in_progress` at a time.
5. If blocked, leave the task `in_progress`, create a new task describing the blocker, and move on.
6. After Step 7 (Report), call `TaskList` and confirm zero `pending` or `in_progress` tasks remain.

### 1. Survey API endpoints and slash commands

Read both files and build two inventories:

**API endpoints** (from `server/api.go` or equivalent):
```bash
grep -E 'HandleFunc\(' server/*.go
```

**Slash command subcommands** (from `server/command.go`):
```bash
grep -E '^\s+case\s+"' server/command.go
```

For each API endpoint, note:
- Route path and HTTP method
- Handler function name
- What operation it performs
- Permission model

For each slash command subcommand, note:
- Subcommand name and arguments
- Which function it calls
- Permission requirements
- Autocomplete description

Step 1 is read-only. Do not edit any file yet.

### 2. Build gap analysis

Classify each API endpoint internally as Paired (has a matching command), API-only internal (UI-driven, action handlers, dialog callbacks), API-only user-facing (candidate for a new command), or Command-only (no API equivalent). Use this classification to filter, but **only show the user the candidates**.

Silently exclude these categories from the output:
- **Paired** endpoints (already have a command)
- **API-only internal** endpoints (action button handlers, dialog callbacks, browser fallback pages, webapp-only fetches)
- **Command-only** commands (no API equivalent, no action needed)

Review `server/api.go` carefully each time: new endpoints may have been added since the last audit.

For each "API-only (user-facing)" endpoint, draft a slash command suggestion:
- **Subcommand name**: follow existing naming conventions (lowercase, hyphenated)
- **Arguments**: match the API endpoint's required parameters
- **Description**: one line for autocomplete
- **Permission level**: match the API handler's permission checks
- **Handler pattern**: reference a similar existing command handler to follow

### 3. Present numbered candidates and confirm plan

Present **only the candidates** as a **numbered list** so the user can pick by number. Do not show paired, internal, or command-only entries.

**Priority ordering**: List user-facing commands (any authenticated user, channel member) first as "high priority", then list System Admin commands separately as "lower priority".

Format each candidate as:

```
N. `/<trigger> subcommand <args>` -- One-line description. Permission: <level>.
```

After presenting the list, ask the user which numbers they want to implement. Accept responses like "all", "1,3,5", "1-4", etc. Wait for user confirmation before proceeding.

### 4. Implement new slash commands

For each approved new command, follow this pattern:

1. **Add a case** to the command dispatcher switch statement in `command.go`:
   ```go
   case "new-command":
       return p.executeNewCommand(args, fields[2:])
   ```

2. **Implement the handler** following the pattern of the most similar existing command. Two handler signatures are common:

   **Pattern A** (with error): use when the handler can fail in ways the dispatcher should know about:
   ```go
   func (p *Plugin) executeNewCommand(args *model.CommandArgs, params []string) (*model.CommandResponse, error) {
       // Permission check
       // Validation
       if len(params) < 1 {
           return &model.CommandResponse{
               ResponseType: model.CommandResponseTypeEphemeral,
               Text:         "Usage: `/<trigger> new-command <arg>`",
           }, nil
       }

       // Business logic (call shared functions, not inline logic)
       result, err := p.sharedBusinessLogic(params[0])
       if err != nil {
           p.API.LogError("new-command failed", "error", err.Error())
           return &model.CommandResponse{
               ResponseType: model.CommandResponseTypeEphemeral,
               Text:         "Failed to execute command. Please try again.",
           }, nil
       }

       return &model.CommandResponse{
           ResponseType: model.CommandResponseTypeEphemeral,
           Text:         fmt.Sprintf("Success: %s", result),
       }, nil
   }
   ```

   **Pattern B** (without error): use for simpler handlers that handle all errors internally.

   In both cases:
   - Check permissions before doing any work.
   - Return `model.CommandResponseTypeEphemeral` for all responses.
   - Call the same shared business logic that the API handler already uses. Do not reimplement business logic in the command handler.
   - For async operations, return an immediate ephemeral response and do the work in a goroutine with a `defer recover()`.

### 5. Update autocomplete data

In the command registration function:

1. Create a `model.NewAutocompleteData` entry for each new subcommand.
2. Add text arguments with `.AddTextArgument()` or static list arguments with `.AddStaticListArgument()`.
3. Add the entry to the parent autocomplete.
4. Update any help text to include the new subcommand.

### 6. Verify

- Run `make check-style` to ensure Go formatting and lint pass
- Run `make test` to confirm existing tests still pass
- Verify the new switch cases and autocomplete entries are consistent:
  ```bash
  grep -cE '^\s+case\s+"' server/command.go
  grep -c 'autocomplete.AddCommand' server/command.go
  ```
- Read through each new handler to confirm it calls the same shared logic as the corresponding API handler.

### 7. Report

Summarize:
- Which new slash commands were added and what they do
- Which API endpoints remain API-only and why
- Any follow-up work needed (tests, help docs, etc.)

Remind the user to run `/add-help-docs` if new commands were added, since user-facing documentation will need updating.

## Critical Files

- `server/command.go`: slash command registration, autocomplete, dispatcher, and handlers
- `server/api.go`: REST endpoint registration and handlers (source of truth for operations)
- `server/plugin.go`: Plugin struct and shared helpers used by both API and command handlers

## Common Mistakes

- Adding a switch case but forgetting to add the autocomplete entry (or vice versa). Both must stay in sync.
- Not updating the help text to include the new subcommand.
- Not following the existing permission model. Check what the API handler requires and match it.
- Implementing business logic directly in the command handler instead of calling the shared function that the API handler already uses.
- Classifying an internal/UI-driven endpoint as needing a slash command.
- Using em dashes in descriptions or comments. The repo convention forbids them.
- Not running `make check-style` after editing Go files.
- Forgetting the async pattern for long-running operations. If the API handler runs async, the command should too.
