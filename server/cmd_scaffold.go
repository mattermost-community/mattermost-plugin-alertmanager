package main

import (
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// scaffoldSet maps a set name to the slugs it includes. `all` covers
// every embedded runbook (resolved at runtime); the category names
// scope to subsets. Adding a new set means adding an entry here.
//
// The category boundaries match the headings in runbooks/INDEX.md.
var scaffoldSets = map[string][]string{
	"all": nil, // resolved at runtime to the full embedded list
	"compute": {
		"high-cpu-usage",
		"high-memory-usage",
		"pod-crashloopbackoff",
		"pod-not-ready",
		"deployment-replicas-unavailable",
		"node-not-ready",
		"cpu-throttling-high",
		"image-pull-backoff",
		"pods-unschedulable",
	},
	"application": {
		"high-http-error-rate",
		"high-api-latency",
		"service-endpoint-down",
		"request-rate-anomaly",
	},
	"database": {
		"database-connectivity-loss",
		"database-replication-lag",
		"database-high-latency",
		"postgres-connections-near-max",
	},
	"storage": {
		"persistent-volume-full",
		"disk-fill-rate-high",
	},
	"networking": {
		"ingress-high-5xx",
		"certificate-expiring-soon",
		"dns-resolution-failure",
	},
	"observability": {
		"prometheus-scrape-target-down",
		"alertmanager-notification-failure",
	},
	"security": {
		"unexpected-container-image",
		"apiserver-auth-failure-spike",
		"privileged-container-started",
		"interactive-shell-in-container",
		"rbac-privilege-escalation",
		"security-tooling-down",
	},
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
// Usage:
//
//	/alertmanager add <team> <channel> <am-url> [target] [on] [--webhook-host=<url>]
//
// `target` is one of:
//   - A category set keyword: `all` (default), `compute`, `application`,
//     `database`, `storage`, `networking`, `observability`
//   - An individual runbook slug: `high-cpu-usage`, `database-replication-lag`, etc.
//
// Trailing `on` opts the receivers in to rotation reminders. Optional
// `--webhook-host=<url>` overrides the host portion of the rendered
// api_url for the multi-cluster deployment pattern.
func (p *Plugin) handleAdd(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	rest := fields[2:]

	// Extract --webhook-host=<url> from anywhere in the args list.
	// Remaining args are positional. Allows usage like:
	//   /alertmanager add <team> <channel> <am-url> [target] [--webhook-host=<url>]
	//   /alertmanager add --webhook-host=<url> <team> <channel> <am-url> [target]
	webhookHostOverride, rest := extractFlagValue(rest, "--webhook-host=")
	if webhookHostOverride != "" {
		if err := validateWebhookHost(webhookHostOverride); err != nil {
			return fmt.Sprintf("Invalid --webhook-host value: %v", err), nil
		}
	}

	// --format=standard (default, alertmanager.yml) | crd (AlertmanagerConfig).
	// --namespace= applies to CRD output only; defaults to monitoring.
	format, rest := extractFlagValue(rest, "--format=")
	if format == "" {
		format = formatStandard
	}
	if format != formatStandard && format != formatCRD {
		return fmt.Sprintf(":warning: Unknown `--format=%s`. Use `standard` (alertmanager.yml, default) or `crd` (Prometheus Operator AlertmanagerConfig).", format), nil
	}
	namespace, rest := extractFlagValue(rest, "--namespace=")
	if namespace == "" {
		namespace = defaultCRDNamespace
	} else if err := validateCRDNamespace(namespace); err != nil {
		return fmt.Sprintf(":warning: Invalid `--namespace`: %v", err), nil
	}

	// --private creates the destination channel as PRIVATE when it doesn't yet
	// exist (the caller is added as a member so their token can create the
	// webhook). An existing channel keeps whatever type it already has.
	private, rest := extractBoolFlag(rest, "--private")

	// Extract optional `on` positional anywhere in the args list. Opts
	// the receivers being created into the rotation reminder system.
	// Default off — without this, the receivers we create here never
	// trigger rotation reminders regardless of the global
	// WebhookRotationDays setting. Two-tier opt-in: sysadmin sets the
	// global threshold; channel team-admins opt INTO rotation per-add.
	rotationOptIn := false
	filtered := make([]string, 0, len(rest))
	for _, arg := range rest {
		if strings.EqualFold(arg, "on") {
			rotationOptIn = true
			continue
		}
		filtered = append(filtered, arg)
	}
	rest = filtered

	if len(rest) < 3 || len(rest) > 4 {
		return addUsageMessage(), nil
	}

	team, channel, amURL := rest[0], rest[1], strings.TrimRight(rest[2], "/")
	// Validate the AM URL before any side effects so a bad value fails with a
	// clear message instead of creating a channel + webhook and rolling back
	// (CL-03/CL-21). IsValid re-checks it at persist time for the KV-import path.
	if err := validateAlertManagerURL(amURL); err != nil {
		return fmt.Sprintf("Invalid Alertmanager URL: %v", err), nil
	}

	// Authorize the DESTINATION team, not the invocation channel — a team_admin
	// must not be able to create a channel or bind a webhook in a team they do
	// not administer. System admins bypass (handled in the helper).
	if err := p.requireTeamAdminBySlug(args.UserId, team); err != nil {
		return err.Error(), nil
	}

	target := "all"
	if len(rest) == 4 {
		target = strings.ToLower(rest[3])
	}

	groupName, slugs, err := resolveAddTarget(target)
	if err != nil {
		return err.Error(), nil
	}
	if len(slugs) == 0 {
		return ":warning: Target `" + target + "` resolved to zero runbooks. Either the embedded runbook list is empty or the category map is misconfigured.", nil
	}

	// Resolve the destination channel ONCE rather than per-receiver. All
	// receivers we create here share a channel, so one lookup is enough.
	rc, err := p.resolveOrCreateChannel(team, channel, private, args.UserId)
	if err != nil {
		return fmt.Sprintf("Failed to resolve destination channel: %v", err), nil
	}
	// Adopt the canonical team/channel names from the resolved objects for every
	// downstream use — receiver-name construction, the skip check, and the stored
	// entries — so nothing persists a raw arg (CL-39).
	channelID, channelCreated := rc.channelID, rc.created
	team, channel = rc.teamName, rc.channelName

	// Atomic read-modify-write: acquire configWriteMu here, immediately
	// before the first getConfiguration read, and hold it through the save
	// below so a concurrent add/remove/reconcile can't clobber the merged
	// result (lost update). Deliberately NOT held during arg parsing or the
	// channel resolve above — those don't touch config state, so keeping the
	// lock off them minimizes contention.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

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
	// <group-or-slug>--<channel> in Mattermost.
	results := make([]scaffoldResult, 0, len(slugs))
	newSlugs := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		receiverName := receiverNameForChannel(slug, team, channel)
		if existingInThisChannel[receiverName] || existingInThisChannel[slug] {
			results = append(results, scaffoldResult{receiverName, "skipped", "already exists"})
			continue
		}
		newSlugs = append(newSlugs, slug)
	}

	newEntries := make([]alertConfig, 0, len(newSlugs))
	var sharedHookID string

	if len(newSlugs) > 0 {
		// One Mattermost webhook serves every receiver in this add
		// invocation. Display name follows <group-or-slug>--<channel>
		// so System Console → Integrations → Incoming Webhooks shows
		// the unit, not the per-receiver slug.
		webhookDisplayName := fmt.Sprintf("Alertmanager: %s--%s", groupName, channel)
		hookID, hookErr := p.createIncomingWebhook(args.UserId, channelID, webhookDisplayName)
		if hookErr != nil {
			// Webhook creation failed — every requested new slug fails.
			// Existing skipped slugs remain in the results; rendering
			// below shows the full picture. If we created the channel just
			// for this add, archive it so a failed add leaves no empty squat.
			p.rollbackCreatedChannel(channelCreated, channelID)
			for _, slug := range newSlugs {
				results = append(results, scaffoldResult{receiverNameForChannel(slug, team, channel), "failed", hookErr.Error()})
			}
		} else {
			sharedHookID = hookID
			now := time.Now().UTC()
			for _, slug := range newSlugs {
				receiverName := receiverNameForChannel(slug, team, channel)
				newEntries = append(newEntries, alertConfig{
					Name:                     receiverName,
					Team:                     team,
					Channel:                  channel,
					AlertManagerURL:          amURL,
					WebhookID:                sharedHookID,
					GroupName:                groupName,
					WebhookHostOverride:      webhookHostOverride,
					LastRotatedAt:            now,
					RotationRemindersEnabled: rotationOptIn,
				})
				results = append(results, scaffoldResult{receiverName, "created", sharedHookID})
			}
		}
	}

	// Persist all new entries in one save rather than N individual saves.
	// Single SavePluginConfig call = no race risk between OnConfigurationChange
	// firings = atomic-to-plugin-settings semantics. If the save fails, we
	// roll back the shared webhook so the user isn't left with an orphan.
	if len(newEntries) > 0 {
		// Append the new entries to the freshly-read KV list under compare-and-set
		// (CL-24). slices.Concat allocates a fresh backing array — guards against the
		// append-aliasing pitfall of reusing the source slice's capacity. A
		// concurrent add of a colliding name surfaces as a validation failure inside
		// updateConfigsAtomic, which rolls back the webhook + channel below.
		_, _, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
			return slices.Concat(current, newEntries), nil
		})
		if err != nil {
			_ = p.deleteIncomingWebhook(args.UserId, sharedHookID)
			p.rollbackCreatedChannel(channelCreated, channelID)
			return fmt.Sprintf("Failed to persist scaffold (rolled back shared webhook): %v", err), nil
		}
	}

	// Render the summary.
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

	b.WriteString(fmt.Sprintf(":white_check_mark: `/alertmanager add` complete: %d created, %d skipped, %d failed.\n\n", created, skipped, failed))
	b.WriteString(fmt.Sprintf("All receivers bound to channel `~%s`. Alertmanager URL: `%s`\n\n", channel, amURL))
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

	// If anything was created, deliver the assembled receivers.yml via
	// a DM from the bot to the calling sysadmin. Two reasons we use a
	// DM rather than attaching the file to the ephemeral summary post:
	//
	//   1. Ephemeral posts with file attachments are buggy in Mattermost
	//      — the file uploads but the post-file linkage doesn't persist
	//      reliably because the post itself isn't in the DB. Users see
	//      a broken attachment they can't download.
	//   2. DMs persist normally, so the file is fetchable, downloadable,
	//      and findable later in the user's DM history with the bot.
	//
	// The DM is between @alertmanagerbot and the calling user, so only
	// the calling sysadmin sees the YAML (which contains webhook URLs
	// = channel-bound bearer tokens).
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

		if format == formatCRD {
			// Prometheus Operator path: DM an AlertmanagerConfig + Secret
			// manifest instead of the alertmanager.yml receivers/routes files.
			manifest, crCount := p.assembleCRDManifest(newEntries, namespace)
			if dmErr := p.dmCRDBundle(args.UserId, manifest, crCount, namespace); dmErr != nil {
				p.API.LogWarn("scaffold: couldn't DM CRD manifest; falling back to inline", "err", dmErr.Error())
				b.WriteString(":warning: Couldn't DM the manifest (")
				b.WriteString(dmErr.Error())
				b.WriteString(fmt.Sprintf("). Inline copy below — review and `kubectl apply -n %s`:\n\n```yaml\n", namespace))
				b.WriteString(manifest)
				b.WriteString("```\n")
			} else {
				b.WriteString(fmt.Sprintf(":page_facing_up: **Sent `alertmanager-config.yaml` (%d AlertmanagerConfig, v1alpha1) to your DM with `@%s`.** Open it, review, then apply:\n\n```\nkubectl apply -n %s -f alertmanager-config.yaml\n```\n:book: `/alertmanager docs kubernetes`\n", crCount, webhookUsername, namespace))
			}
		} else {
			yamlFile := p.assembleReceiversYAML(newEntries, results, channel, amURL)
			routesFile := assembleRoutesYAML(newEntries)
			dmErr := p.dmYAMLBundle(args.UserId, yamlFile, routesFile, created, amURL)
			if dmErr != nil {
				// DM delivery failed — fall back to inline YAML in the
				// summary post. Long but functional.
				p.API.LogWarn("scaffold: couldn't DM assembled YAML; falling back to inline", "err", dmErr.Error())
				b.WriteString(":warning: Couldn't DM the assembled YAML file (")
				b.WriteString(dmErr.Error())
				b.WriteString("). Inline copy below — paste under `receivers:` in your `alertmanager.yml`:\n\n```yaml\n")
				b.WriteString(yamlFile)
				b.WriteString("```\n")
			} else {
				b.WriteString(":page_facing_up: **Sent `alertmanager-receivers.yml` to your DM with `@")
				b.WriteString(webhookUsername)
				b.WriteString("`** — open that conversation to download the file. Paste the contents under `receivers:` in your `alertmanager.yml`, then reload:\n\n```\ncurl -X POST ")
				b.WriteString(amURL)
				b.WriteString("/-/reload\n```\n")
			}
		}
	}

	return b.String(), nil
}

// dmYAMLBundle opens a DM channel between the bot and the calling
// sysadmin, uploads both the assembled receivers YAML AND the matching
// routes YAML as files, and posts a single message attaching both. The
// DM channel persists across plugin reactivations, so the user can find
// the files again in their bot DM history.
//
// Two files instead of one combined: the user pastes each into a
// different section of their existing alertmanager.yml (receivers
// under `receivers:`, routes under `route.routes:`). Splitting them
// makes the copy-paste workflow explicit — no slicing one big file
// into two paste locations.
//
// `routesYAML` may be empty (e.g., from handleAdd called with the
// `noop` default) — if so, only the receivers file is sent.
func (p *Plugin) dmYAMLBundle(userID, receiversYAML, routesYAML string, createdCount int, amURL string) error {
	dm, appErr := p.API.GetDirectChannel(p.BotUserID, userID)
	if appErr != nil {
		return fmt.Errorf("open DM with user: %w", appErr)
	}

	// Upload the receivers file. File store + ACL behavior is normal
	// for DM channels (unlike ephemeral posts, see comment in handleAdd).
	receiversInfo, appErr := p.API.UploadFile([]byte(receiversYAML), dm.Id, "alertmanager-receivers.yml")
	if appErr != nil {
		return fmt.Errorf("upload receivers YAML to DM: %w", appErr)
	}

	fileIds := []string{receiversInfo.Id}
	hasRoutes := strings.TrimSpace(routesYAML) != ""
	if hasRoutes {
		routesInfo, routesErr := p.API.UploadFile([]byte(routesYAML), dm.Id, "alertmanager-routes.yml")
		if routesErr != nil {
			// Routes upload failure isn't fatal — receivers still useful
			// without them (user can hand-write routes). Log and proceed.
			p.API.LogWarn("scaffold: couldn't upload routes file to DM (receivers file still delivered)", "err", routesErr.Error())
		} else {
			fileIds = append(fileIds, routesInfo.Id)
		}
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Assembled YAML for the %d new receiver(s) you just created via `/alertmanager add`.\n\n", createdCount))
	msg.WriteString("**Paste `alertmanager-receivers.yml`** under `receivers:` in your `alertmanager.yml`.\n")
	if len(fileIds) > 1 {
		msg.WriteString("**Paste `alertmanager-routes.yml`** under `route.routes:` in your `alertmanager.yml`.\n")
	}
	msg.WriteString(fmt.Sprintf("\nThen reload Alertmanager:\n\n```\ncurl -X POST %s/-/reload\n```", amURL))

	dmPost := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: dm.Id,
		Message:   msg.String(),
		FileIds:   fileIds,
	}
	created, appErr := p.API.CreatePost(dmPost)
	if appErr != nil {
		return fmt.Errorf("post to DM: %w", appErr)
	}
	// Track the post for the auto-delete janitor. Deleting the post
	// (which the janitor does at TTL) unattaches the YAML files from
	// the user's view, limiting how long the webhook URLs persist in
	// reachable chat history.
	p.trackYAMLForAutoDelete(created.Id)
	return nil
}

// assembleRoutesYAML generates the `routes:` block matching the
// receivers in the given list. Every emitted route carries
// `continue: true`.
//
// Why unconditionally continue: this generator is invoked once per
// /alertmanager add or /alertmanager export call. Those calls are
// channel-scoped — the generator only sees the receivers in ONE
// channel. When the user runs /alertmanager add twice (once per
// channel) and pastes both routes blocks under a single
// `route.routes:`, a fan-out runbook (same slug bound to two
// channels) ends up with two routes that have identical matchers.
// AM's default is "stop at first match" — without `continue: true`
// the second route is silently dead and the second channel never
// gets the alert. Setting continue on every plugin-generated route
// is defensive: each route's matcher is unique to one runbook slug,
// so continue only changes behavior in the fan-out case, where it
// fixes the dead-route bug.
//
// Output is a plain `routes:` block ready to paste under
// `route.routes:` in alertmanager.yml.
func assembleRoutesYAML(entries []alertConfig) string {
	if len(entries) == 0 {
		return ""
	}

	// Split runbook receivers (auto-routed on the `runbook` label) from custom
	// receivers (add-custom): the plugin can't know what labels a custom alert
	// carries, so custom receivers get a COMMENTED matcher stub the user fills
	// in manually rather than a live route.
	var customEntries []alertConfig
	grouped := make(map[string][]alertConfig)
	for _, ac := range entries {
		if ac.Custom {
			customEntries = append(customEntries, ac)
			continue
		}
		slug := receiverBaseSlug(ac.Name)
		grouped[slug] = append(grouped[slug], ac)
	}

	slugs := make([]string, 0, len(grouped))
	for s := range grouped {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)

	var b strings.Builder
	b.WriteString("# Alertmanager routes generated by /alertmanager add or /alertmanager export.\n")
	b.WriteString("# Paste this block under `route.routes:` in your alertmanager.yml.\n")
	b.WriteString("# Routes match on the `runbook` label of each alert — set that label\n")
	b.WriteString("# on your Prometheus rules to drive alerts to the right receiver.\n")
	b.WriteString("# Every route carries `continue: true` so fan-out (same runbook routed\n")
	b.WriteString("# to multiple channels via separate /alertmanager add calls) works\n")
	b.WriteString("# correctly when both blocks are pasted under one routes: list.\n")
	b.WriteString("\n")

	// Only emit the `routes:` key when there is at least one live (runbook)
	// route. A bare `routes:` with nothing but comments underneath is invalid
	// YAML; when every receiver is custom we emit only the commented stubs,
	// which the user pastes under their own existing `routes:`.
	if len(slugs) > 0 {
		b.WriteString("routes:\n")
		for _, slug := range slugs {
			group := grouped[slug]
			for _, ac := range group {
				b.WriteString(fmt.Sprintf("  - matchers: [runbook=%q]\n", slug))
				b.WriteString(fmt.Sprintf("    receiver: %s\n", ac.Name))
				b.WriteString("    continue: true\n")
			}
		}
	}

	if len(customEntries) > 0 {
		sort.Slice(customEntries, func(i, j int) bool { return customEntries[i].Name < customEntries[j].Name })
		b.WriteString("\n")
		b.WriteString("# Custom (non-runbook) receivers — created via /alertmanager add-custom.\n")
		b.WriteString("# The plugin cannot know which labels your custom alerts carry, so no\n")
		b.WriteString("# matcher is generated. Uncomment each block and fill in the matcher(s)\n")
		b.WriteString("# your Prometheus rules actually emit, then paste under `route.routes:`.\n")
		for _, ac := range customEntries {
			b.WriteString("#  - matchers: [ <your labels, e.g. alertname=\"MyCustomAlert\", severity=\"critical\"> ]\n")
			b.WriteString(fmt.Sprintf("#    receiver: %s\n", ac.Name))
			b.WriteString("#    continue: true\n")
		}
	}
	return b.String()
}

// assembleReceiversYAML concatenates the rendered slack_configs blocks
// for every newly-created receiver into one paste-ready file body.
// Header comments capture the binding context so anyone re-reading the
// file later (or extracting blocks back out of version control) can see
// which channel and Alertmanager URL it targets.
//
// `results` is iterated rather than `newEntries` directly so the output
// order matches the user-facing summary table.
func (p *Plugin) assembleReceiversYAML(newEntries []alertConfig, results []scaffoldResult, channel, amURL string) string {
	byName := make(map[string]alertConfig, len(newEntries))
	for _, e := range newEntries {
		byName[e.Name] = e
	}

	var y strings.Builder
	y.WriteString("# Alertmanager receivers generated by /alertmanager add\n")
	y.WriteString("# Append these blocks under `receivers:` in alertmanager.yml,\n")
	y.WriteString("# then update the routes block to dispatch each alert to its matching receiver.\n")
	y.WriteString(fmt.Sprintf("# Channel:  ~%s\n", channel))
	y.WriteString(fmt.Sprintf("# AM URL:   %s\n", amURL))
	y.WriteString("\n")

	for _, r := range results {
		if r.Status != "created" {
			continue
		}
		entry, ok := byName[r.Slug]
		if !ok {
			// Defensive: shouldn't happen because "created" implies an entry
			// was appended to newEntries, but log + continue so a stale
			// results entry can't crash the assembly.
			y.WriteString(fmt.Sprintf("# WARN: created result for %q has no matching entry — skipping\n\n", r.Slug))
			continue
		}
		rendered := renderReceiverYAMLForKind(entry.Name, p.webhookURLForReceiver(entry), entry.Channel, p.runbookDefaultURL(receiverBaseSlug(entry.Name)), p.siteURL()+webhookIconURL, entry.Custom)
		y.WriteString(rendered)
		y.WriteString("\n")
	}
	return y.String()
}

// receiverNameForChannel constructs the receiver name from a runbook slug,
// team slug, and channel slug. Pattern: <slug>--<team>-<channel>.
//
// Team is part of the name because channel names are only unique PER TEAM —
// `town-square` exists in every team by default. Without the team segment,
// receivers for the same runbook in same-named channels across teams would
// collide (Alertmanager requires globally-unique receiver names), silently
// misrouting one team's alerts to another's channel.
//
// The double-hyphen after the slug is the load-bearing separator:
// receiverBaseSlug splits on the FIRST `--` to recover the runbook slug, so
// the slug boundary stays unambiguous. The team-channel tail uses a single
// `-` and is NOT parsed back out — team and channel live in their own
// alertConfig fields; the tail is display/uniqueness only. Global uniqueness
// is enforced by the duplicate-name rejection in parseAlertConfigs, not by
// the separator, so a (rare) team/channel hyphen ambiguity fails loud at
// config-save rather than misrouting.
func receiverNameForChannel(slug, teamSlug, channelSlug string) string {
	return slug + "--" + teamSlug + "-" + channelSlug
}

// receiverBaseSlug returns the runbook slug portion of a receiver name.
// For new-style names like `high-cpu-usage--alert-slo-channel`,
// returns `high-cpu-usage`. For legacy unsuffixed names (created before
// channel-suffixing), returns the whole name unchanged. Used to derive
// the runbook fallback URL, which is keyed by runbook slug not by full
// receiver name.
func receiverBaseSlug(receiverName string) string {
	if idx := strings.Index(receiverName, "--"); idx > 0 {
		return receiverName[:idx]
	}
	return receiverName
}

// scaffoldResult is the per-receiver outcome captured during a scaffold
// run. Lifted to the package scope so helper functions can take it
// without re-declaring an anonymous type.
type scaffoldResult struct {
	Slug   string
	Status string // "created" | "skipped" | "failed"
	Detail string
}

// resolveAddTarget classifies the [target] arg of /alertmanager add as
// either a group set keyword or an individual runbook slug. Returns
// (groupName, slugs) where groupName is the unit name baked into the
// shared webhook's display name + each receiver's GroupName field, and
// slugs is the runbooks to create receivers for (whole set for groups,
// single-element for individual).
//
// "all" resolves to every embedded runbook (groupName = "all").
// Category names (compute, application, ...) resolve to their subset.
// Otherwise we check if the arg matches a known runbook slug — if so,
// it's an individual add and groupName = the slug itself.
// Anything else is an error with a discoverability hint.
func resolveAddTarget(target string) (groupName string, slugs []string, err error) {
	target = strings.ToLower(strings.TrimSpace(target))

	if target == "all" {
		return "all", runbookSlugs(), nil
	}
	if setSlugs, ok := scaffoldSets[target]; ok && setSlugs != nil {
		return target, setSlugs, nil
	}
	// Individual add path: must match a known runbook slug exactly.
	if slices.Contains(runbookSlugs(), target) {
		return target, []string{target}, nil
	}
	return "", nil, fmt.Errorf("unknown target `%s` — must be a category set (`%s`) OR a specific runbook slug (e.g. `high-cpu-usage`). Run `/alertmanager add` with no args for the full list",
		target, strings.Join(scaffoldSetNames(), "`, `"))
}

// handleAddCustom creates ONE generic (non-runbook) receiver named
// <name>--<team>-<channel> with its own Mattermost webhook. Unlike handleAdd it
// does NOT emit a `runbook=` route — the receiver is marked Custom and the user
// wires the matcher manually (export renders a commented stub). Mirrors the
// handleAdd persistence discipline: configWriteMu held across the read-modify-write,
// webhook rolled back if the save fails.
//
//	/alertmanager add-custom <team> <channel> <am-url> <name> [--webhook-host=<url>]
func (p *Plugin) handleAddCustom(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	rest := fields[2:]

	webhookHostOverride, rest := extractFlagValue(rest, "--webhook-host=")
	if webhookHostOverride != "" {
		if err := validateWebhookHost(webhookHostOverride); err != nil {
			return fmt.Sprintf("Invalid --webhook-host value: %v", err), nil
		}
	}

	// --private creates the destination channel as PRIVATE when it's new.
	private, rest := extractBoolFlag(rest, "--private")

	if len(rest) != 4 {
		return addCustomUsageMessage(), nil
	}
	team, channel, amURL, rawName := rest[0], rest[1], strings.TrimRight(rest[2], "/"), rest[3]
	// Reject a dangerous AM URL up front (CL-03/CL-21) — see handleAdd.
	if err := validateAlertManagerURL(amURL); err != nil {
		return fmt.Sprintf("Invalid Alertmanager URL: %v", err), nil
	}

	// Authorize the DESTINATION team, not the invocation channel — otherwise a
	// team_admin could create a channel and bind a webhook in a team they do
	// not administer. System admins bypass (handled in the helper).
	if err := p.requireTeamAdminBySlug(args.UserId, team); err != nil {
		return err.Error(), nil
	}

	receiverName, err := validateCustomReceiverName(rawName, team, channel)
	if err != nil {
		return ":warning: " + err.Error(), nil
	}

	rc, err := p.resolveOrCreateChannel(team, channel, private, args.UserId)
	if err != nil {
		return fmt.Sprintf("Failed to resolve destination channel: %v", err), nil
	}
	// Persist canonical names (CL-39). receiverName was already validated and
	// built from the raw args above, which equal these canonical values for any
	// name Mattermost accepts on lookup.
	channelID, channelCreated := rc.channelID, rc.created
	team, channel = rc.teamName, rc.channelName

	// Atomic read-modify-write — same discipline as handleAdd.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	for _, c := range p.getConfiguration().AlertConfigs {
		if c.Team == team && c.Channel == channel && c.Name == receiverName {
			// A duplicate means the channel already had this receiver, so it
			// pre-existed — channelCreated is false and nothing is rolled back.
			return fmt.Sprintf(":warning: Receiver `%s` already exists in this channel.", receiverName), nil
		}
	}

	webhookDisplayName := fmt.Sprintf("Alertmanager: %s--%s", receiverBaseSlug(receiverName), channel)
	hookID, hookErr := p.createIncomingWebhook(args.UserId, channelID, webhookDisplayName)
	if hookErr != nil {
		p.rollbackCreatedChannel(channelCreated, channelID)
		return fmt.Sprintf("Failed to create Mattermost webhook: %v", hookErr), nil
	}

	entry := alertConfig{
		Name:                receiverName,
		Team:                team,
		Channel:             channel,
		AlertManagerURL:     amURL,
		WebhookID:           hookID,
		GroupName:           receiverBaseSlug(receiverName),
		Custom:              true,
		WebhookHostOverride: webhookHostOverride,
		LastRotatedAt:       time.Now().UTC(),
	}

	_, _, err = p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		return slices.Concat(current, []alertConfig{entry}), nil
	})
	if err != nil {
		_ = p.deleteIncomingWebhook(args.UserId, hookID)
		p.rollbackCreatedChannel(channelCreated, channelID)
		return fmt.Sprintf("Failed to persist custom receiver (rolled back webhook): %v", err), nil
	}

	// Build the runbook-free receiver block + the commented route stub and DM
	// them to the caller. add-custom can target a channel the caller is not a
	// member of, so pointing them at channel-scoped `/alertmanager export` could
	// leave them unable to retrieve the block — DM it directly (mirrors handleAdd).
	receiverYAML := "# Custom (non-runbook) receiver created by /alertmanager add-custom.\n" +
		"# Paste under `receivers:` in your alertmanager.yml.\n\n" +
		renderReceiverYAMLForKind(entry.Name, p.webhookURLForReceiver(entry), channel, "", p.siteURL()+webhookIconURL, true)
	routesYAML := assembleRoutesYAML([]alertConfig{entry})

	var b strings.Builder
	fmt.Fprintf(&b, ":white_check_mark: Created custom receiver `%s`\n", receiverName)
	fmt.Fprintf(&b, "Webhook created and bound to `%s` (team `%s`). No runbook is attached — alerts render as raw content.\n\n", channel, team)
	b.WriteString(":warning: **No route exists yet — you must wire it manually.** Add a matcher under `route.routes:` selecting the alerts you want here, e.g.:\n\n")
	b.WriteString("```yaml\n")
	b.WriteString("  - matchers: [ alertname=\"MyCustomAlert\" ]   # <-- set to labels YOUR rules emit\n")
	fmt.Fprintf(&b, "    receiver: %s\n", receiverName)
	b.WriteString("    continue: true\n")
	b.WriteString("```\n\n")

	if dmErr := p.dmYAMLBundle(args.UserId, receiverYAML, routesYAML, 1, amURL); dmErr != nil {
		// DM failed — inline the receiver block so the caller isn't left unable
		// to retrieve it (export is channel-scoped and may be unreachable).
		p.API.LogWarn("add-custom: couldn't DM receiver YAML; falling back to inline", "err", dmErr.Error())
		b.WriteString(":warning: Couldn't DM the receiver block (")
		b.WriteString(dmErr.Error())
		b.WriteString("). Inline copy below — paste under `receivers:` in your `alertmanager.yml`:\n\n```yaml\n")
		b.WriteString(receiverYAML)
		b.WriteString("```\n")
	} else {
		fmt.Fprintf(&b, ":page_facing_up: **Sent the receiver block + route stub to your DM with `@%s`.** See `/alertmanager docs configuration` for the walkthrough.", webhookUsername)
	}
	return b.String(), nil
}

// addCustomUsageMessage is shown when /alertmanager add-custom is called with
// the wrong number of positional args.
func addCustomUsageMessage() string {
	return "Usage: `/alertmanager add-custom <team> <channel> <am-url> <name> [--webhook-host=<url>]`\n\n" +
		"Creates ONE generic (non-runbook) receiver named `<name>--<team>-<channel>` with its own Mattermost webhook. " +
		"Unlike `/alertmanager add`, it does NOT generate a `runbook=` route — you wire the matcher yourself " +
		"(see `/alertmanager docs configuration`). `<name>`: lowercase `[a-z0-9_-]`, no `--`, and not a runbook slug or category set. " +
		"Requires channel team-admin or sysadmin."
}

// validateCustomReceiverName checks a user-supplied custom name and returns the
// full team+channel-qualified receiver name. It rejects names that would break
// the `<slug>--<team>-<channel>` parsing contract or collide with the runbook
// flow, and defers final shape/length enforcement to alertConfigNameRegex on the
// assembled name.
func validateCustomReceiverName(rawName, team, channel string) (fullName string, err error) {
	name := strings.ToLower(strings.TrimSpace(rawName))
	if name == "" {
		return "", fmt.Errorf("a receiver name is required")
	}
	if strings.Contains(name, "--") {
		return "", fmt.Errorf("name %q must not contain `--` (that's the reserved slug/team-channel separator)", name)
	}
	if slices.Contains(runbookSlugs(), name) {
		return "", fmt.Errorf("name %q is a runbook slug — use `/alertmanager add %s %s %s` for the runbook flow, or pick a different custom name", name, team, channel, name)
	}
	if _, ok := scaffoldSets[name]; ok {
		return "", fmt.Errorf("name %q is a reserved category set — pick a different custom name", name)
	}
	full := receiverNameForChannel(name, team, channel)
	if !alertConfigNameRegex.MatchString(full) {
		return "", fmt.Errorf("resulting receiver name %q is invalid or too long (max 190 chars; allowed [a-z0-9_-], must start [a-z0-9]) — shorten the custom name or channel/team slug", full)
	}
	return full, nil
}

// scaffoldSetNames returns the sorted list of known set names for help
// text and error messages. `all` is the canonical "full set" name and
// is listed first; categories follow alphabetically.
func scaffoldSetNames() []string {
	names := []string{"all"}
	for k := range scaffoldSets {
		if k == "all" {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names[1:])
	return names
}

// addUsageMessage renders the help shown when the user runs
// /alertmanager add with wrong arity. Lists every available set,
// the individual-slug path, and the optional flags. Discoverability
// matters because this is the bootstrap step and the user shouldn't
// have to read source to use it.
func addUsageMessage() string {
	var b strings.Builder
	b.WriteString("**Usage:** `/alertmanager add <team> <channel> <am-url> [target] [on] [--webhook-host=<url>]`\n\n")
	b.WriteString("Creates Mattermost incoming webhook(s) for the chosen target, all bound to the named channel.\n")
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

	b.WriteString("\n**Individual slug targets:** any runbook slug. Run `/alertmanager docs` to see what ships.\n\n")
	b.WriteString("**Optional args:**\n")
	b.WriteString("- `on` — opt these receivers in to webhook rotation reminders (see `WebhookRotationDays` in System Console)\n")
	b.WriteString("- `--webhook-host=<url>` — override the host portion of the rendered `api_url` for the multi-cluster pattern\n")
	return b.String()
}

// extractFlagValue pulls a "--name=value" style flag out of an args
// list. Returns the value (empty if absent) and the remaining args
// with the flag removed. Multiple matches: last one wins. Used for
// optional flags like --webhook-host in /alertmanager add.
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

// extractBoolFlag removes a bare boolean flag (e.g. `--private`) from anywhere in
// args, returning whether it was present and the remaining positionals.
func extractBoolFlag(args []string, flag string) (present bool, rest []string) {
	rest = make([]string, 0, len(args))
	for _, a := range args {
		if a == flag {
			present = true
			continue
		}
		rest = append(rest, a)
	}
	return present, rest
}

// runbookSlugs reads the embedded runbooks/ directory and returns the
// slugs (filename without .md, lowercased) in stable alphabetical order.
// Filters out INDEX.md and TEMPLATE.md which are meta files.
func runbookSlugs() []string {
	skip := map[string]bool{"INDEX.md": true, "TEMPLATE.md": true}

	var slugs []string
	_ = fs.WalkDir(root.RunbooksFS, "runbooks", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") || skip[name] {
			return nil
		}
		slug := strings.ToLower(strings.TrimSuffix(name, ".md"))
		slugs = append(slugs, slug)
		return nil
	})
	sort.Strings(slugs)
	return slugs
}
