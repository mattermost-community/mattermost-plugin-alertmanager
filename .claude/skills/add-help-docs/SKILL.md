---
name: add-help-docs
description: Use when plugin code changes need to be reflected in user-facing documentation (help pages under public/, auto-generated reference pages, or any committed generated artifact). Trigger after adding or modifying REST endpoints, admin settings, slash commands, or any other user-visible surface, or before cutting a release.
---

# Add Help Docs

Keep user-facing documentation in sync with plugin code. Covers hand-edited HTML help pages, auto-generated reference pages, and any generated artifacts committed to the repo.

## When to Use

- After adding, modifying, or removing a REST endpoint
- After changing admin settings in `server/configuration.go` or `plugin.json`
- After adding or changing a slash command
- After changing any source that feeds a generator script committed to the repo
- Before cutting a release

## When NOT to Use

- Pure refactors with no user-visible or API-surface change
- Test-only or build-only changes
- Changes already documented in a previous commit on the same branch

## Workflow

Run these steps in order. **Always produce a plan and get explicit user confirmation before editing any documentation file or running any generator script.**

Before starting, create tasks using TaskCreate for each applicable step. Mark each task complete with TaskUpdate as you finish it.

### 1. Survey what changed

Look at the current branch's diff against `main` and identify user-visible changes:

```bash
git diff main...HEAD -- server/ plugin.json webapp/src/ public/
git log main..HEAD --oneline
```

Focus on:
- New or changed HTTP routes, request bodies, response shapes, status codes
- New or renamed config settings, defaults, validation rules
- New slash commands or changed command arguments
- Changes to source files that feed generator scripts

### 2. Produce a plan

Before touching any doc file or running any generator script, write a short plan and present it to the user. The plan must list:

- Which docs will be edited, and the specific user-visible change driving each edit
- Whether any generator scripts will be run (which ones, and why)
- Anything intentionally left alone and the reason

Wait for explicit user confirmation before continuing.

### 3. Update docs

Edit only the docs whose scope actually changed. Preserve existing anchor IDs so cross-links stay intact. Match the existing tone, heading structure, and variable names. Never use em dashes.

### 4. Regenerate artifacts (if applicable)

If a generator script's source files changed, run the script from the repo root and commit the regenerated output alongside the source change. Do not hand-edit files that are marked auto-generated; fix the source or the generator.

### 5. Verify

- `git status` shows doc and/or generated-artifact changes matching the scope from step 1
- Spot-check a changed HTML file or generated artifact renders correctly

### 6. Report

Summarize:
- Which docs changed and why
- Which generators were run (if any)
- Anything intentionally left alone

## Common Mistakes

- Skipping the plan step. Always write the plan and get user confirmation first.
- Hand-editing auto-generated files. Fix the source or the generator script instead.
- Breaking existing anchor IDs, which silently breaks any links or URL templates that reference those anchors.
- Using em dashes. The repo convention forbids them in docs and code.
