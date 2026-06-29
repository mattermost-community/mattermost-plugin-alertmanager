---
name: cleanup
description: Use to fully tear down feature-branch worktrees after their PRs have been merged. Surveys every registered worktree and classifies each as READY / DIRTY / UNPUSHED / OPEN_PR / NO_PR / PROTECTED, then for each target the user picks: verifies the branch is fully merged into the trunk on GitHub, runs `make nuke` to stop dev containers and remove build/data artifacts, deletes the remote and local branch, removes the git worktree, prunes the `project.code-workspace` entry, removes the directory, and finally fetches `.bare` with `--prune` and fast-forwards the `main/` worktree to the trunk. Refuses to run against the `main` worktree, the `main`/`master` branches, or any branch that is not yet merged on GitHub.
---

# Cleanup

Tear down the current feature-branch worktree once its work is merged. Designed for the bare-repo + worktree layout used by this project (see project `CLAUDE.md`): every feature lives in its own sibling worktree (e.g. `track-feed/`, `cleanup-settings/`) next to `main/`, and is expected to be removed once the PR lands on the trunk branch.

This skill assumes the trunk branch is `main`. If a repo uses `master` as its trunk, substitute `master` for `main` in the sync step; the protected-branch checks already cover both names.

## When to Use

- The feature PR for the current worktree has been merged into the trunk (`main`) on GitHub.
- You are sitting in the feature worktree directory (not `main/`), and want to fully remove it.
- After running `gh pr merge` to confirm the merge was clean and you're done with the branch.

## When NOT to Use

- You are in the `main/` worktree. **The skill refuses outright.** `main/` is shared infrastructure and is never removed.
- The current branch is `main` or `master`. **Refuses outright.**
- The feature PR is still open, draft, or closed-without-merging. The skill will abort and tell you to merge first.
- You have uncommitted changes or unpushed commits. The skill will abort. Commit and push (or stash) first.
- You are partway through a feature and want to "clean up" build artifacts only. Use `make docker-stop` or `make nuke` directly instead — this skill is about ending the branch's life entirely.

## Hard Safety Rule

**Never operate on the `main/` worktree or the `main`/`master` branches.** Every step in the workflow checks this twice (once in the pre-flight, once before the destructive step). Do not bypass either check.

## Survey: which worktrees are ready to clean up?

Before running the per-worktree workflow, survey every registered worktree so the user can see at a glance what's safe to remove. This survey runs from any worktree and is purely read-only.

```bash
# Run from inside any worktree.
REPO_ROOT=$(git rev-parse --git-common-dir)/..
cd "$REPO_ROOT" >/dev/null

# git worktree list emits one line per worktree:
#   <path>  <sha> [<branch>]   OR   <path>  (bare)
# Skip the bare repo entry and the main/ worktree.
git --git-dir=.bare worktree list --porcelain
```

For each non-bare, non-`main` entry, classify the worktree:

| Status | Meaning | Action |
|---|---|---|
| `PROTECTED` | path basename is `main` OR branch is `main`/`master` | **Never clean.** Skip. |
| `DIRTY` | `git -C <path> status --porcelain` is non-empty | Skip. User must commit/stash first. |
| `UNPUSHED` | local HEAD is ahead of `origin/<branch>` | Skip. User must push or rebase first. |
| `OPEN PR` | `gh pr list --state open --head <branch>` returns a PR | Skip. PR not merged yet. |
| `NO PR` | no merged or open PR for the branch | Skip. Either work in progress or branch was abandoned without merging. |
| `READY` | merged PR exists AND working tree clean AND HEAD pushed (or remote branch deleted post-merge) | Safe to clean. |

The classification commands per row:

```bash
# Branch + path of one worktree (parsed from `git worktree list --porcelain`):
WORKTREE_PATH=...
WORKTREE_BRANCH=...
WORKTREE_NAME=$(basename "$WORKTREE_PATH")

# 1. PROTECTED check
if [ "$WORKTREE_NAME" = "main" ] || [ "$WORKTREE_BRANCH" = "main" ] || [ "$WORKTREE_BRANCH" = "master" ]; then
    STATUS=PROTECTED
fi

# 2. DIRTY check
if [ -n "$(git -C "$WORKTREE_PATH" status --porcelain)" ]; then
    STATUS=DIRTY
fi

# 3. UNPUSHED check
git -C "$WORKTREE_PATH" fetch origin "$WORKTREE_BRANCH" 2>/dev/null || true
LOCAL=$(git -C "$WORKTREE_PATH" rev-parse HEAD)
REMOTE=$(git -C "$WORKTREE_PATH" rev-parse "origin/$WORKTREE_BRANCH" 2>/dev/null || echo "")
if [ -n "$REMOTE" ] && [ "$LOCAL" != "$REMOTE" ]; then
    STATUS=UNPUSHED
fi

# 4. PR state via gh (preferred over `git branch --merged main` because squash/rebase merges are invisible to it)
MERGED=$(gh pr list --state merged --head "$WORKTREE_BRANCH" --json number --limit 1 --jq 'length')
OPEN=$(gh pr list --state open --head "$WORKTREE_BRANCH" --json number --limit 1 --jq 'length')
if [ "$MERGED" = "1" ]; then
    STATUS=READY
elif [ "$OPEN" = "1" ]; then
    STATUS=OPEN_PR
else
    STATUS=NO_PR
fi
```

Render a table summarizing the survey for the user:

```
Worktree         Branch              Status     Notes
----------------------------------------------------
main             main                PROTECTED  never cleaned
feature-a        feature-a           READY      PR #99 merged
feature-b        feature-b           OPEN_PR    PR #100 still open
feature-c        feature-c           DIRTY      3 modified files
feature-d        feature-d           UNPUSHED   2 commits ahead of origin
feature-e        feature-e           NO_PR      no PR opened yet
```

Then ask the user which `READY` worktrees to clean up. Accept "all", a specific name, or a comma-separated list. **Only `READY` worktrees are eligible.** Do not offer `DIRTY`, `UNPUSHED`, `OPEN_PR`, or `NO_PR` — those need user action that is out of scope for this skill.

If the user invokes `/cleanup` while sitting inside a `READY` worktree and does not specify a target, default to cleaning that worktree. If they invoke from `main/`, the survey output is the whole answer: list candidates and ask which to operate on.

For each chosen target, run the per-worktree workflow below in sequence. Each target gets its own pass through steps 1 through 11.

## Workflow (per worktree)

Walk through every numbered step for the chosen target worktree. **If any pre-flight check fails, stop and tell the user; do not proceed to step 5 or beyond.** The destructive steps (5 through 9) only run after every pre-flight check has passed.

Use `TaskCreate` for each step so progress is visible: one task per pre-flight (1-4), one per destructive step (5-9), one for the sync (10), and one for the final verification (11). Mark each `in_progress` before starting and `completed` only after its check passes. If multiple worktrees are being cleaned in one invocation, repeat the eleven-step list per worktree (e.g., `Cleanup feature-a: step 4 -- confirm PR merged`).

### 1. Confirm the worktree is not `main`

Reject `main/` with a hard stop. Compute the worktree name from the cwd basename:

```bash
WORKTREE_DIR=$(basename "$PWD")
echo "Working tree: $WORKTREE_DIR"
```

If `WORKTREE_DIR` is `main`, abort immediately with a clear message: `Refusing to clean up the main worktree. /cleanup only operates on feature worktrees.` Do not continue.

### 2. Confirm the branch is not `main`/`master`

```bash
BRANCH=$(git rev-parse --abbrev-ref HEAD)
echo "Branch: $BRANCH"
```

If `BRANCH` is `main`, `master`, `HEAD` (detached), or empty, abort: `Refusing to clean up branch '$BRANCH'. /cleanup only operates on feature branches.`

### 3. Confirm a clean working tree

The skill must not destroy uncommitted work. Run:

```bash
git status --porcelain
```

If the output is non-empty, abort with the file list and tell the user to commit, stash, or discard those changes first. Do not interpret "small" or "ignorable" changes as safe; the user decides.

Also verify the local branch tip has been pushed:

```bash
git fetch origin "$BRANCH" 2>/dev/null || true
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse "origin/$BRANCH" 2>/dev/null || echo "")
```

If `REMOTE` is empty, the branch may have been deleted on the remote already (likely because the PR was merged and the merge cleanup ran). That is fine; continue. But if `REMOTE` is non-empty and `LOCAL != REMOTE`, abort: there are unpushed commits.

### 4. Confirm the PR is merged on GitHub

This is the load-bearing safety check. Use the `gh` CLI to look up a merged PR for this branch:

```bash
gh pr list --state merged --head "$BRANCH" --json number,mergeCommit,mergedAt,title --limit 1
```

If the JSON array is empty, abort with: `No merged PR found for branch '$BRANCH'. /cleanup refuses to proceed because work on this branch may not be reflected in the trunk.` Tell the user to either open + merge a PR, or rebase the unmerged work onto another branch.

If a merged PR is returned, surface it to the user:

```
Confirmed merged: PR #<number> "<title>" merged at <mergedAt>, trunk commit <mergeCommit.oid>.
```

Squash and rebase merges create a NEW commit hash on the trunk, so `git branch --merged main` will NOT show the branch as merged. **Trust the GitHub PR state, not local git's "merged" detection.** This is why the gh check is the authoritative gate.

### 5. Run `make nuke`

Once all pre-flight checks pass, run the project's full teardown from inside the worktree:

```bash
make nuke
```

`make nuke` stops the dev containers and removes the worktree's build and data artifacts (see the `nuke` target in the project `Makefile` for exactly what it removes). After this completes, the worktree directory should contain only source files and any untracked dev scratch.

If `make nuke` fails, do not continue. Surface the error to the user; their dev environment may need manual attention.

### 6. Delete the remote branch (if still present)

```bash
if git ls-remote --exit-code --heads origin "$BRANCH" >/dev/null 2>&1; then
    git push origin --delete "$BRANCH"
fi
```

The `gh pr merge --delete-branch` flow may have already removed it. The conditional avoids a noisy error in that case. If the remote branch was merged via "rebase and merge" or kept around for some other reason, this deletes it now.

### 7. Remove the git worktree

The bare repo lives at `<repo-root>/.bare`. Worktree removal requires running from outside the worktree being removed. Move to `main/` first, then remove:

```bash
cd "$(git rev-parse --git-common-dir)/../main"
git --git-dir=../.bare worktree remove --force "../$WORKTREE_DIR"
```

The `--force` flag handles untracked files that may remain if `make nuke` did not remove every artifact. **Re-verify `$WORKTREE_DIR` is not `main` immediately before this command.** If it is, abort.

If the `worktree remove` command fails because the worktree directory is "Directory not empty", that is a real problem — `make nuke` should have cleaned everything. Surface the error and stop.

### 8. Delete the local branch

Squash-merged and rebase-merged branches always need `-D` because the local branch tip is not reachable from the trunk's tip. The PR-merged check at step 4 is what makes this safe:

```bash
git --git-dir=../.bare branch -D "$BRANCH"
```

### 9. Update `project.code-workspace` and remove the directory

The repo root has a `project.code-workspace` file listing every active worktree as a folder entry. Remove the entry for `$WORKTREE_DIR`:

```bash
WORKSPACE="$(git rev-parse --git-common-dir)/../project.code-workspace"
```

Use the `Edit` tool to remove the matching `{ "name": "$WORKTREE_DIR", "path": "$WORKTREE_DIR" }` block, preserving the `main` and any other entries.

Then remove the directory itself if `worktree remove` did not already do so (it usually does):

```bash
WORKTREE_PATH="$(git rev-parse --git-common-dir)/../$WORKTREE_DIR"
if [ -d "$WORKTREE_PATH" ]; then
    rm -rf "$WORKTREE_PATH"
fi
```

**Re-verify `$WORKTREE_DIR` is not `main` immediately before `rm -rf`.** Hard-stop if it is.

### 10. Sync `main/` and `.bare/` with origin

Bring the shared bare repo and the `main/` worktree current with the remote so the just-landed merge commit is reflected locally and any stale remote-tracking refs are pruned.

`git fetch --prune` updates the bare repo's refs and removes any `refs/remotes/origin/*` entries whose remote branches no longer exist (this catches branches deleted by the merge cleanup at step 6 and any others tidied up server-side). `git pull --ff-only` advances `main/`'s checked-out tree to the latest trunk without creating a merge commit; if `main/` cannot fast-forward (someone committed locally on the trunk directly, against project convention), the pull fails loudly so the user can investigate.

```bash
# We are already in main/ from step 7. Confirm before any further work.
test "$(basename "$PWD")" = "main" || { echo "Not in main/, aborting sync"; exit 1; }

git fetch --prune origin
git pull --ff-only origin main
```

If `git pull --ff-only` fails with "Not possible to fast-forward", do not force the update. Surface the error to the user and stop. They likely have local commits on the trunk that need to be addressed manually (push them, rebase them, or discard them depending on intent).

### 11. Verify and report

```bash
git --git-dir=../.bare worktree list
git --git-dir=../.bare branch --list
ls -d ../*/ 2>&1 | head
```

The output should show:
- `main` worktree present, no entry for `$WORKTREE_DIR`
- trunk branch present locally, no `$BRANCH` entry
- No `$WORKTREE_DIR` directory under the repo root

Report a one-block summary:

- PR #N (`<branch>`) confirmed merged at `<mergeCommit>`
- `make nuke` ran cleanly
- Remote branch deleted
- Worktree unregistered
- Local branch deleted
- `project.code-workspace` entry removed
- Directory removed
- `.bare` fetched and `main/` fast-forwarded to the trunk

## Critical Files

- `<repo-root>/.bare` -- bare git repo all worktrees attach to
- `<repo-root>/main/` -- protected worktree, never operated on
- `<repo-root>/project.code-workspace` -- VS Code workspace folder list, kept in sync with `git worktree list`
- `<worktree>/Makefile` -- provides the `nuke` target this skill depends on

## Common Mistakes

- **Skipping the gh PR merged check.** Local `git branch --merged main` does NOT see squash or rebase merges. Trusting it can silently destroy unmerged work. Always use `gh pr list --state merged --head <branch>`.
- **Treating a `READY` worktree from the survey as authoritative without re-running the per-worktree pre-flight.** The survey is a quick scan; classification can drift between the survey and the actual cleanup (e.g. someone pushed a new commit, someone opened a PR). Always re-run steps 1-4 immediately before steps 5-9 for each target.
- **Offering to clean up a `DIRTY`, `UNPUSHED`, `OPEN_PR`, or `NO_PR` worktree as a bulk action.** Only `READY` worktrees are eligible. The other states require user action that is out of scope for this skill.
- **Running `git worktree remove` from inside the worktree being removed.** Git refuses. Always `cd ../main` (or any other worktree) first.
- **Removing the `main` directory** because the script forgot which worktree it was in. Re-check `$WORKTREE_DIR != main` immediately before each destructive command, not only in step 1.
- **Treating `git status` clean as sufficient.** A branch can be locally clean but have unpushed commits. Step 3 must check both.
- **Using `git push --force-with-lease` to "fix" a remote-vs-local divergence at step 3.** That is feature work, not cleanup. Abort and let the user resolve it manually.
- **Running `make nuke` before the safety checks.** `make nuke` permanently destroys local dev data; the user expects all four pre-flight checks to pass before that happens.
- **Trying to update `project.code-workspace` with a regex over the whole file.** Use the `Edit` tool with the literal block; the file is JSON and a careless `sed` will break it.
- **Using em dashes** anywhere in code, comments, or commit/PR text. Repo convention forbids them.
