package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// splitAddWords pulls the plain words `standard`, `crd` and `on` out of the
// arguments; they can sit anywhere after the URL and are never a runbook slug,
// group, or custom name. `on` opts the receivers into rotation reminders:
// sysadmins set the global WebhookRotationDays, and channel team-admins opt
// INTO rotation per add. Without it these receivers are never reminded.
func splitAddWords(args []string) (format string, rotationOptIn bool, positional []string) {
	format = formatStandard
	positional = make([]string, 0, len(args))
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "on":
			rotationOptIn = true
		case formatStandard, formatCRD:
			format = strings.ToLower(arg)
		default:
			positional = append(positional, arg)
		}
	}
	return format, rotationOptIn, positional
}

// handleAdd creates Mattermost incoming webhooks for the chosen target
// (group or individual runbook), all bound to the same channel.
//
// Webhook consolidation (v1.0.3+): receivers created in one add invocation
// share a single Mattermost webhook. A group target (compute, all, etc.)
// produces N receivers all pointing at one webhook; an individual slug
// target produces one receiver with its own webhook. The shared-webhook
// name follows <group-or-slug>--<channel> form.
//
// Idempotent — existing receivers (by name) are skipped, not overwritten.
// When ALL targets in the group already exist, no new webhook is created.
//
// The destination is always the channel the command runs in, so there are no
// team or channel arguments. That keeps add in line with every other command and
// makes a mistyped slug or a cross-team misroute impossible.
//
// Usage:
//
//	/alertmanager config add <am-url> [target] [standard|crd] [on] [namespace=<ns>] [webhook-host=<url>]
//
// `target` is one of:
//   - A category set keyword: `all` (default), `compute`, `application`,
//     `database`, `storage`, `networking`, `observability`
//   - An individual runbook slug: `high-cpu-usage`, `database-replication-lag`, etc.
//
// `standard` (default) or `crd` picks the DM'd config format. Trailing `on`
// opts the receivers in to rotation reminders. Optional
// `webhook-host=<url>` overrides the host portion of the rendered
// api_url for the multi-cluster deployment pattern.
func (p *Plugin) handleAdd(args *model.CommandArgs) (string, error) {
	req, reply := p.parseAddRequest(args)
	if reply != "" {
		return reply, nil
	}

	dest, msg := p.currentChannelDestination(args)
	if msg != "" {
		return msg, nil
	}

	// Atomic read-modify-write: acquire configWriteMu here, immediately
	// before the first getConfiguration read, and hold it through the save
	// so a concurrent add/remove/reconcile can't clobber the merged
	// result (lost update). Deliberately NOT held during arg parsing or the
	// channel resolve above — those don't touch config state, so keeping the
	// lock off them minimizes contention.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	results, newEntries, hookID := p.createAddReceivers(args, req, dest)
	if reply := p.persistAddEntries(args, req, dest, newEntries, hookID); reply != "" {
		return reply, nil
	}
	return p.renderAddSummary(args, req, dest, results, newEntries), nil
}

// addRequest is the validated form of a `config add` command line.
type addRequest struct {
	amURL               string
	groupName           string
	slugs               []string // receivers to create, including the pack receiver
	runbookSlugs        []string // runbook receivers only; drives the sample rules
	format              string
	namespace           string
	rotationOptIn       bool
	webhookHostOverride string
	mmTarget            *mattermostTarget
}

// parseAddRequest validates the command line. A non-empty reply is the
// user-facing message to return instead of continuing.
func (p *Plugin) parseAddRequest(args *model.CommandArgs) (addRequest, string) {
	fields := strings.Fields(args.Command)
	rest := fields[2:]

	if msg := removedAddSyntaxMessage(rest); msg != "" {
		return addRequest{}, msg
	}

	webhookHostOverride, rest := extractFlagValue(rest, "webhook-host=")
	if webhookHostOverride != "" {
		if err := validateWebhookHost(webhookHostOverride); err != nil {
			return addRequest{}, fmt.Sprintf("Invalid `webhook-host` value: %v", err)
		}
	}

	// namespace= applies to CRD output only; defaults to monitoring.
	namespace, rest := extractFlagValue(rest, "namespace=")
	if namespace == "" {
		namespace = defaultCRDNamespace
	} else if err := validateCRDNamespace(namespace); err != nil {
		return addRequest{}, fmt.Sprintf(":warning: Invalid `namespace`: %v", err)
	}

	mmNamespace, rest := extractFlagValue(rest, mmNamespaceFlag)
	mmName, rest := extractFlagValue(rest, mmNameFlag)

	format, rotationOptIn, positional := splitAddWords(rest)

	if len(positional) < 1 || len(positional) > 2 {
		return addRequest{}, addUsageMessage()
	}

	amURL := strings.TrimRight(positional[0], "/")
	// Validate the AM URL before any side effects so a bad value fails with a
	// clear message instead of creating a webhook and rolling back
	// (CL-03/CL-21). IsValid re-checks it at persist time for the KV-import path.
	if err := validateAlertManagerURL(amURL); err != nil {
		return addRequest{}, fmt.Sprintf("Invalid Alertmanager URL: %s", p.amErrorText(args.UserId, err))
	}

	target := "all"
	if len(positional) == 2 {
		target = strings.ToLower(positional[1])
	}

	groupName, slugs, err := resolveAddTarget(target)
	if err != nil {
		return addRequest{}, err.Error()
	}
	if len(slugs) == 0 {
		return addRequest{}, ":warning: Target `" + target + "` resolved to zero runbooks. Either the embedded runbook list is empty or the category map is misconfigured."
	}

	// mm-namespace turns the operator alerts on for the `mattermost` group: one
	// extra receiver claims them by alertname. Without it the group is the
	// runbook alerts only.
	var mmTarget *mattermostTarget
	runbookSlugs := slugs
	if mmNamespace != "" || mmName != "" {
		if groupName != mattermostGroup {
			return addRequest{}, fmt.Sprintf(":warning: `mm-namespace` and `mm-name` only apply to the `%s` target.", mattermostGroup)
		}
		t, tErr := newMattermostTarget(mmNamespace, mmName)
		if tErr != nil {
			return addRequest{}, fmt.Sprintf(":warning: %v\n\n%s", tErr, addUsageMessage())
		}
		mmTarget = &t
		slugs = append(slices.Clone(slugs), packReceiverSlug)
	}

	return addRequest{
		amURL:               amURL,
		groupName:           groupName,
		slugs:               slugs,
		runbookSlugs:        runbookSlugs,
		format:              format,
		namespace:           namespace,
		rotationOptIn:       rotationOptIn,
		webhookHostOverride: webhookHostOverride,
		mmTarget:            mmTarget,
	}, ""
}

// createAddReceivers creates the shared webhook and builds the new receiver
// entries. Receivers already in this channel are skipped, and a failed webhook
// fails every new slug. Callers hold configWriteMu.
func (p *Plugin) createAddReceivers(args *model.CommandArgs, req addRequest, dest addDestination) (results []scaffoldResult, newEntries []alertConfig, hookID string) {
	team, channel := dest.team, dest.channel

	// Skip-check is scoped to the destination TEAM + channel. Channel names
	// are unique only per team (`town-square` exists in every team), so a
	// receiver in team-a's `town-square` must NOT block creating one in
	// team-b's `town-square` — matching on channel name alone did exactly
	// that, silently skipping the second team's add. Walk current config once.
	current := p.getConfiguration().AlertConfigs
	existingInThisChannel := make(map[string]bool)
	for _, c := range current {
		if c.Team == team && c.Channel == channel {
			existingInThisChannel[c.Name] = true
		}
	}

	// Two-pass: identify slugs that need creation, then create one shared
	// webhook for the whole batch. Team+channel-qualify every receiver name
	// (pattern <slug>--<team>-<channel>); the shared webhook itself is named
	// <category>--<team>-<channel> in Mattermost, mirroring the receiver-name
	// format so the two line up in System Console.
	results = make([]scaffoldResult, 0, len(req.slugs))
	newSlugs := make([]string, 0, len(req.slugs))
	for _, slug := range req.slugs {
		receiverName := receiverNameForChannel(slug, team, channel)
		if existingInThisChannel[receiverName] || existingInThisChannel[slug] {
			results = append(results, scaffoldResult{receiverName, "skipped", "already exists"})
			continue
		}
		newSlugs = append(newSlugs, slug)
	}

	newEntries = make([]alertConfig, 0, len(newSlugs))
	if len(newSlugs) == 0 {
		return results, newEntries, ""
	}

	// One Mattermost webhook serves every receiver in this add
	// invocation. Display name follows the receiver-name format
	// <category>--<team>-<channel> so System Console → Integrations →
	// Incoming Webhooks shows the unit (and disambiguates channels that
	// repeat across teams), not the per-receiver slug.
	webhookDisplayName := webhookDisplayNameFor(req.groupName, team, channel)
	created, hookErr := p.createIncomingWebhook(args.UserId, dest.channelID, webhookDisplayName)
	if hookErr != nil {
		// Existing skipped slugs remain in the results; rendering
		// shows the full picture.
		for _, slug := range newSlugs {
			results = append(results, scaffoldResult{receiverNameForChannel(slug, team, channel), "failed", hookErr.Error()})
		}
		return results, newEntries, ""
	}

	hookID = created
	now := time.Now().UTC()
	for _, slug := range newSlugs {
		receiverName := receiverNameForChannel(slug, team, channel)
		newEntries = append(newEntries, alertConfig{
			Name:                     receiverName,
			Team:                     team,
			Channel:                  channel,
			AlertManagerURL:          req.amURL,
			WebhookID:                hookID,
			GroupName:                req.groupName,
			Kind:                     kindFor(slug),
			Pack:                     packFor(slug),
			WebhookHostOverride:      req.webhookHostOverride,
			LastRotatedAt:            now,
			RotationRemindersEnabled: req.rotationOptIn,
		})
		// Detail is rendered into the chat response table; show the redacted
		// fingerprint, never the raw hook ID (a reusable bearer token — F-002).
		// The real webhook URL reaches the admin only in the DM'd YAML/CRD.
		results = append(results, scaffoldResult{receiverName, "created", redactHookID(hookID)})
	}
	return results, newEntries, hookID
}

// persistAddEntries saves all new entries in one write rather than N saves.
// If the save fails the shared webhook is rolled back so the user isn't left
// with an orphan. A non-empty reply is the failure message. Callers hold
// configWriteMu.
func (p *Plugin) persistAddEntries(args *model.CommandArgs, req addRequest, dest addDestination, newEntries []alertConfig, hookID string) string {
	if len(newEntries) == 0 {
		return ""
	}
	// Append the new entries to the freshly-read KV list under compare-and-set
	// (CL-24). slices.Concat allocates a fresh backing array — guards against the
	// append-aliasing pitfall of reusing the source slice's capacity. A
	// concurrent add of a colliding name surfaces as a validation failure inside
	// updateConfigsAtomic, which rolls back the webhook + channel below.
	_, _, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		if capErr := enforceReceiverCap(len(current), len(newEntries)); capErr != nil {
			return nil, capErr
		}
		return slices.Concat(current, newEntries), nil
	})
	if err == nil {
		return ""
	}
	warn := ""
	if delErr := p.deleteIncomingWebhook(args.UserId, hookID); delErr != nil {
		p.API.LogWarn("scaffold rollback: could not delete shared webhook (orphan may remain)", "webhook", redactHookID(hookID), "err", delErr.Error())
		warn = webhookRollbackWarning(webhookDisplayNameFor(req.groupName, dest.team, dest.channel))
	}
	return fmt.Sprintf("Failed to persist scaffold (rolled back shared webhook): %s%s", p.amErrorText(args.UserId, err), warn)
}

// renderAddSummary builds the reply: the per-receiver table, the DM delivery
// of the config bundle, and any group-specific prerequisites.
func (p *Plugin) renderAddSummary(args *model.CommandArgs, req addRequest, dest addDestination, results []scaffoldResult, newEntries []alertConfig) string {
	var b strings.Builder
	created := 0
	skipped := 0
	failed := 0
	for _, r := range results {
		switch r.Status {
		case "created":
			created++
		case "skipped":
			skipped++
		case "failed":
			failed++
		}
	}

	b.WriteString(fmt.Sprintf(":white_check_mark: `/alertmanager config add` complete: %d created, %d skipped, %d failed.\n\n", created, skipped, failed))
	b.WriteString(fmt.Sprintf("All receivers bound to channel `~%s`. Alertmanager URL: `%s`\n\n", dest.channel, req.amURL))
	b.WriteString("| Receiver | Status | Detail |\n")
	b.WriteString("|----------|--------|--------|\n")
	for _, r := range results {
		marker := ":white_check_mark:"
		if r.Status == "skipped" {
			marker = ":fast_forward:"
		} else if r.Status == "failed" {
			marker = ":x:"
		}
		b.WriteString(fmt.Sprintf("| `%s` | %s %s | %s |\n", r.Slug, marker, r.Status, r.Detail))
	}

	if created > 0 {
		// Always list the receiver names in the in-channel summary —
		// they're the primary handoff to /alertmanager config <name>,
		// independent of whether the DM/file delivery worked.
		b.WriteString("\n**Receivers ready for `/alertmanager config <name>`:**\n\n```\n")
		for _, r := range results {
			if r.Status == "created" {
				b.WriteString(r.Slug + "\n")
			}
		}
		b.WriteString("```\n\n")
		b.WriteString(p.deliverAddBundle(args, req, dest, results, newEntries))
	}

	// The plugin has no Prometheus access to probe for the exporter, so a
	// static warning at scaffold time is the best signal we can give.
	if req.groupName == "database" {
		b.WriteString("\n\n⚠ **Database receivers require `postgres_exporter`** — the rules for this group use `pg_replication_lag_seconds`, `pg_stat_activity_count`, and `pg_stat_statements_*` metrics that only exist when a postgres_exporter is running and scraped by Prometheus. Without it, these receivers will never fire.\n\n")
		b.WriteString("Deploy: `helm install postgres-exporter prometheus-community/prometheus-postgres-exporter` and configure a ServiceMonitor pointing at your Postgres instance. Also enable `shared_preload_libraries = 'pg_stat_statements'` in PostgreSQL for the DatabaseHighLatency rule.")
	}

	return b.String()
}

// deliverAddBundle DMs the assembled config (receivers.yml or CRD manifest)
// plus the matching sample rules to the calling admin, and returns the reply
// text describing where it went. Two reasons for a DM rather than attaching
// the file to the ephemeral summary post:
//
//  1. Ephemeral posts with file attachments are buggy in Mattermost
//     — the file uploads but the post-file linkage doesn't persist
//     reliably because the post itself isn't in the DB. Users see
//     a broken attachment they can't download.
//  2. DMs persist normally, so the file is fetchable, downloadable,
//     and findable later in the user's DM history with the bot.
//
// The DM is between @alertmanagerbot and the calling user, so only
// the calling sysadmin sees the YAML (which contains webhook URLs
// = channel-bound bearer tokens).
func (p *Plugin) deliverAddBundle(args *model.CommandArgs, req addRequest, dest addDestination, results []scaffoldResult, newEntries []alertConfig) string {
	var b strings.Builder
	created := len(newEntries)

	// The receivers only route alerts. The matching sample rules ride in the
	// same DM so one message holds everything to apply.
	var rules *rulesFile
	var rulesErr error
	rc := rulesContext{namespace: req.namespace, format: req.format, allowlist: true}
	if req.mmTarget != nil {
		rules, rulesErr = p.mattermostRulesFile(rc, req.mmTarget)
	} else {
		rules, rulesErr = p.rulesFileForAdd(rc, req.groupName, req.runbookSlugs, "")
	}
	rulesReason := ""
	if rulesErr != nil {
		p.API.LogWarn("scaffold: couldn't build the rules file for this group", "err", rulesErr.Error(), "group", req.groupName)
		rulesReason = fmt.Sprintf(":warning: Couldn't build the rules file for this group (%v). Run `/alertmanager format rules` for the full set.", rulesErr)
		rules = nil
	}
	delivered := false

	if req.format == formatCRD {
		// Prometheus Operator path: DM an AlertmanagerConfig + Secret
		// manifest instead of the alertmanager.yml receivers/routes files.
		manifest, crCount := p.assembleCRDManifest(newEntries, req.namespace)
		if dmErr := p.dmCRDBundle(args.UserId, manifest, crCount, req.namespace, rules); dmErr != nil {
			p.API.LogWarn("scaffold: couldn't DM CRD manifest; falling back to inline", "err", dmErr.Error())
			b.WriteString(":warning: Couldn't DM the manifest (")
			b.WriteString(dmErr.Error())
			b.WriteString(fmt.Sprintf("). Inline copy below — review and `kubectl apply -n %s`:\n\n```yaml\n", req.namespace))
			b.WriteString(manifest)
			b.WriteString("```\n")
		} else {
			delivered = true
			b.WriteString(fmt.Sprintf(":page_facing_up: **Sent `alertmanager-config.yaml` (%d AlertmanagerConfig, v1alpha1) to your DM with `@%s`.** Open it, review, then apply:\n\n```\nkubectl apply -n %s -f alertmanager-config.yaml\n```\n:book: [Kubernetes guide](%s)\n", crCount, webhookUsername, req.namespace, p.helpPageURL("KUBERNETES.md")))
		}
	} else {
		yamlFile := p.assembleReceiversYAML(newEntries, results, dest.channel, req.amURL)
		routesFile := assembleRoutesYAML(newEntries)
		if dmErr := p.dmYAMLBundle(args.UserId, yamlFile, routesFile, created, req.amURL, rules); dmErr != nil {
			// DM delivery failed — fall back to inline YAML in the
			// summary post. Long but functional.
			p.API.LogWarn("scaffold: couldn't DM assembled YAML; falling back to inline", "err", dmErr.Error())
			b.WriteString(":warning: Couldn't DM the assembled YAML file (")
			b.WriteString(dmErr.Error())
			b.WriteString("). Inline copy below — paste under `receivers:` in your `alertmanager.yml`:\n\n```yaml\n")
			b.WriteString(yamlFile)
			b.WriteString("```\n")
		} else {
			delivered = true
			b.WriteString(":page_facing_up: **Sent `alertmanager-receivers.yml` to your DM with `@")
			b.WriteString(webhookUsername)
			b.WriteString("`** — open that conversation to download the file. Paste the contents under `receivers:` in your `alertmanager.yml`, then reload:\n\n```\ncurl -X POST ")
			b.WriteString(req.amURL)
			b.WriteString("/-/reload\n```\n")
		}
	}
	b.WriteString(rulesReply(rules, rulesReason, delivered))
	return b.String()
}

// scaffoldResult is the per-receiver outcome captured during a scaffold
// run. Lifted to the package scope so helper functions can take it
// without re-declaring an anonymous type.
type scaffoldResult struct {
	Slug   string
	Status string // "created" | "skipped" | "failed"
	Detail string
}

// addUsageMessage renders the help shown when the user runs
// /alertmanager config add with wrong arity. Lists every available set,
// the individual-slug path, and the optional flags. Discoverability
// matters because this is the bootstrap step and the user shouldn't
// have to read source to use it.
func addUsageMessage() string {
	var b strings.Builder
	b.WriteString("**Usage:** `/alertmanager config add <am-url> [target] [standard|crd] [on] [namespace=<ns>] [webhook-host=<url>] [mm-namespace=<ns>] [mm-name=<cr>]`\n\n")
	b.WriteString("Run it in the channel that should receive the alerts. Creates Mattermost incoming webhook(s) for the chosen target, all bound to this channel.\n")
	b.WriteString("- **Group target** (e.g. `compute`, `all`): one shared webhook serves every receiver in the set.\n")
	b.WriteString("- **Individual slug target** (e.g. `high-cpu-usage`): one dedicated webhook for that one receiver.\n")
	b.WriteString("Existing receivers (by name) are skipped — re-run safely.\n\n")
	b.WriteString("**Group targets:**\n\n")
	b.WriteString("| Set | Count | Includes |\n")
	b.WriteString("|-----|-------|----------|\n")

	allCount := len(runbookSlugs())
	b.WriteString(fmt.Sprintf("| `all` (default) | %d | every embedded runbook |\n", allCount))

	for _, name := range scaffoldSetNames() {
		if name == "all" {
			continue
		}
		slugs := scaffoldSets[name]
		b.WriteString(fmt.Sprintf("| `%s` | %d | %s |\n", name, len(slugs), strings.Join(slugs, ", ")))
	}

	b.WriteString(fmt.Sprintf("\n**Mattermost alerts:** `%s` creates the runbook receivers. Add `mm-namespace=<ns>`, the namespace Mattermost runs in, to also create one receiver for the alerts the Mattermost Operator generates (they carry no `runbook` label, so its route matches alert names). The matching Prometheus rules come in the same DM. `all` never includes them.\n", mattermostGroup))
	b.WriteString("\n**Individual slug targets:** any runbook slug. Run `/alertmanager docs` for the runbook library link.\n\n")
	b.WriteString("**Optional args:**\n")
	b.WriteString("- `standard` (default) or `crd` — format of the config DM'd to you. `standard` is `alertmanager.yml` receivers and routes. `crd` is a Prometheus Operator AlertmanagerConfig plus Secret.\n")
	b.WriteString("- `on` — opt these receivers in to webhook rotation reminders (see `WebhookRotationDays` in System Console)\n")
	b.WriteString("- `webhook-host=<url>` — override the host portion of the rendered `api_url` for the multi-cluster pattern\n")
	b.WriteString(fmt.Sprintf("- `mm-namespace=<ns>` and `mm-name=<cr>` — `%s` only: where Mattermost runs and its custom resource name (default `%s`)\n", mattermostGroup, defaultMattermostName))
	return b.String()
}

// extractFlagValue pulls a "--name=value" style flag out of an args
// list. Returns the value (empty if absent) and the remaining args
// with the flag removed. Multiple matches: last one wins. Used for
// optional key=value words like webhook-host in /alertmanager config add.
func extractFlagValue(args []string, prefix string) (value string, rest []string) {
	rest = make([]string, 0, len(args))
	for _, a := range args {
		if after, ok := strings.CutPrefix(a, prefix); ok {
			value = after
			continue
		}
		rest = append(rest, a)
	}
	return value, rest
}

// removedAddSyntaxMessage catches the arguments add and add-custom used to take,
// so someone with the old form in muscle memory gets the new one instead of a
// confusing URL or target error. It returns "" when none are present.
func removedAddSyntaxMessage(args []string) string {
	var positional []string
	for _, a := range args {
		if !strings.EqualFold(a, "on") {
			positional = append(positional, a)
		}
	}
	if len(positional) >= 3 && strings.Contains(positional[2], "://") && !strings.Contains(positional[0], "://") {
		return ":warning: Team and channel are no longer arguments. The receivers go in the channel you run this in. " +
			"Go to that channel and run `/alertmanager config add <am-url> [target]` (or `config add-custom <am-url> <name>`)."
	}
	return ""
}
