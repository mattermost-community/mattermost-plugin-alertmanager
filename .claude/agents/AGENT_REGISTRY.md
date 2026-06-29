# Agent Registry

Index of all available agents organized by category.

---

## Quick Reference

| Task | Agents |
|------|--------|
| Review a plan | `design-flaw-finder`, `simplicity-reviewer`, + domain agents |
| Review code | `race-condition-finder`, `error-handling-reviewer`, + tier agents |
| Debug failures | `debugger`, `e2e-debugger` |
| Refactor code | `refactorer` |

---

## Core

| Agent | Purpose | Location |
|-------|---------|----------|
| `coder` | Write production-quality code | `core/` |

## Review

| Agent | Purpose | Location |
|-------|---------|----------|
| `api-contract-reviewer` | API design completeness and consistency | `review/` |
| `client-server-alignment` | Client SDK matches server API | `review/` |
| `comment-analyzer` | Comment accuracy and completeness | `review/` |
| `database-architecture-reviewer` | Schema design review | `review/` |
| `design-flaw-finder` | Logical flaws, missing steps | `review/` |
| `doc-consistency-reviewer` | Internal doc inconsistencies | `review/` |
| `duplication-reviewer` | Code duplication | `review/` |
| `edge-case-ux-analyst` | Error/empty states UX | `review/` |
| `error-handling-reviewer` | Missing error checks | `review/` |
| `hardcoded-values-reviewer` | Secrets, magic numbers | `review/` |
| `race-condition-finder` | Concurrency bugs | `review/` |
| `separation-of-concerns-reviewer` | Layer violations | `review/` |
| `simplicity-reviewer` | Over-engineering, YAGNI | `review/` |
| `type-design-analyzer` | Type hierarchies | `review/` |
| `ux-design-reviewer` | UI/UX design review | `review/` |

## Tech

| Agent | Purpose | Location |
|-------|---------|----------|
| `concurrent-go-reviewer` | Go concurrency safety | `tech/` |
| `go-pro` | Go language expert | `tech/` |
| `postgres-expert` | PostgreSQL optimization | `tech/` |
| `react-pro` | Advanced React patterns | `tech/` |
| `typescript-pro` | TypeScript type systems | `tech/` |
| `websocket-expert` | Real-time communication | `tech/` |

## Debug

| Agent | Purpose | Location |
|-------|---------|----------|
| `debugger` | Root cause analysis | `debug/` |
| `e2e-coordinator` | Coordinate E2E runs | `debug/` |
| `e2e-debugger` | Debug E2E failures | `debug/` |
| `refactorer` | Code restructuring | `debug/` |

## Testing

| Agent | Purpose | Location |
|-------|---------|----------|
| `playwright-patterns-reviewer` | E2E test patterns | `testing/` |
| `test-e2e-expert` | E2E test expert | `testing/` |
| `test-unit-expert` | Unit test quality | `testing/` |
| `test-writer` | Generate tests | `testing/` |
| `production-validator` | No mocks in production | `testing/validation/` |

## Security

| Agent | Purpose | Location |
|-------|---------|----------|
| `accessibility-guardian` | Accessibility checks | `security/` |
| `owasp-security` | OWASP security checks | `security/` |
| `threat-modeler` | Threat modeling | `security/` |

## GitHub

| Agent | Purpose | Location |
|-------|---------|----------|
| `pr-manager` | PR management | `github/` |
| `issue-tracker` | Issue tracking | `github/` |
| `release-manager` | Release management | `github/` |
| `code-review-swarm` | Multi-agent code review | `github/` |

## Orchestration

| Agent | Purpose | Location |
|-------|---------|----------|
| `hierarchical-coordinator` | Hierarchical swarm | `swarm/` |
| `mesh-coordinator` | Mesh swarm | `swarm/` |
| `queen-coordinator` | Main coordinator | `hive-mind/` |
| `swarm-memory-manager` | Memory management | `hive-mind/` |

---

## How to Use

### For Plan/Code Review
Use the skills - they select the right agents automatically:
```bash
/create-plan "feature description"
/review-plan plan.md
/review-code
```

### For Direct Agent Use
```bash
Task(subagent_type="general-purpose", prompt="<agent instructions> + <context>")
```
