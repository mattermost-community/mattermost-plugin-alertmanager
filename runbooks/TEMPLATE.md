# <Alert Name>

!!! danger "Severity: <critical|warning|info>"
    **Target response: <Nm for warning, Nm for critical>.** One-line
    business impact statement — what users see, what's degraded.

## What this alert means

Plain-language explanation of the condition. Include the actual PromQL
expression where it helps. Don't assume the reader is the rule author.

```promql
# The expression that fires this alert
<expression>
```

What sustained breaching of this threshold causes downstream
(latency, queueing, OOM, etc.) and why it matters.

## Quick diagnostics

The first three things to run. The plugin extracts the first three
fenced code blocks from this section and embeds them inline in the
alert message that lands in Mattermost — so when the alert fires at
3am, the operator can copy-paste these without opening the runbook.

Keep them short, copy-paste-ready, and ordered by "would I run this
first?" — not by completeness. Use `bash`, `promql`, or `sql` as the
language hint so Mattermost renders syntax highlighting.

**Inline template structure** (every command block follows this):

```
# WHERE: which tool to run the command in (shell + kubectl context /
#   Grafana → Explore / Prometheus /graph / psql with $DATABASE_URL).
# WHAT: one or two sentences on what the command queries and why.
#   Include surrounding theory only when it makes the output legible
#   (e.g., "CFS throttling = Linux scheduler capping the container").
# READ: how to interpret the output — what value is "bad", what's
#   normal, what the next action is. Concrete numbers ("> 0.1 means
#   throttled 10%+ of the time") beat vague hedging.
<command>
```

**Label placeholders for per-alert auto-fill:** if the command should
include label values from the firing alert (e.g., the failing pod,
namespace, host), use `<labelname>` in the command line itself (not
in the comment — comments are skipped by the substituter). At alert
render time, Alertmanager fills in the real value. Allowed labels:
`alertname`, `app`, `cluster`, `container`, `deployment`, `instance`,
`job`, `namespace`, `node`, `pod`, `service`. Unknown placeholders
pass through unchanged.

```bash
# WHERE: shell with kubectl context set.
# WHAT: describe the affected pod.
# READ: see the Conditions block for what's False.
kubectl describe pod -n <namespace> <pod>
```

If you add placeholders, also add a `## Required Prometheus labels`
section near the end of the runbook (before `## Related runbooks`)
listing the labels your diagnostics expect. See existing runbooks
for the format.

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No — chat only | N min | <observed impact> |
| Critical | Yes — page on-call | N min | <observed impact> |

## Diagnostic steps

Run in order. Stop as soon as the cause is obvious.

### 1. Confirm the alert is current

```bash
# Command to verify the condition is still firing
```

If it's no longer firing, move to historical analysis (step N).

### 2. <Next narrowing step>

```bash
# Command
```

### 3. <Continue narrowing>

```bash
# Command
```

### 4. <Continue narrowing>

```bash
# Command
```

### 5. <If reached here, deep-dive area>

```bash
# Command
```

## Common causes & fixes

### A. <Most common cause>

| Symptom | Diagnosis | Fix |
|---|---|---|
| <observable> | <how to confirm> | <command or steps> |

### B. <Next most common>

| Symptom | Diagnosis | Fix |
|---|---|---|
| <observable> | <how to confirm> | <command or steps> |

### C. <Continue with remaining cause categories>

| Symptom | Diagnosis | Fix |
|---|---|---|
| <observable> | <how to confirm> | <command or steps> |

## Escalation

If unresolved within the target response time:

1. **Primary on-call** — `@<team>-oncall` in `#mm-incidents`. PagerDuty
   service: `<service-name>`.
2. **Secondary** — `@<other-team>-oncall` if the cause is in their
   domain (DB, network, infra). PagerDuty service: `<other-service>`.
3. **Vendor support** — if cause is suspected in third-party
   software. Open a P1 ticket.

**Severity ladder:**

| Time elapsed | Action |
|---|---|
| 0–N min | Primary on-call works the alert |
| N–N min | Escalate to secondary, post status in #mm-incidents |
| N+ min | Engage incident commander, declare incident |

## Post-incident

After the immediate fix lands:

1. **File a follow-up issue** with root cause and corrective action.
   Use the team's incident template.
2. **Update this runbook** if the cause wasn't covered — open a merge
   request against this repo with the new entry.
3. **If a rollback was needed, file a regression bug** against the
   service that shipped the offending change.
4. **Tune thresholds** if the alert fired too late (you found out
   from a user) or too early (it was noise) — both are signals.

## Related runbooks

- [Other Related Alert](other-alert.md) — when this alert co-fires
  with that one
- [Yet Another Alert](yet-another-alert.md) — common upstream cause

## Appendix: useful PromQL queries (optional)

Section to drop helpful exploratory queries for the on-call who's
already in the runbook and wants to dig further:

```promql
# Top N offenders ranked by the alert's metric
topk(10, <expression>)
```

```promql
# Correlated metric that often points at the cause
<related expression>
```
