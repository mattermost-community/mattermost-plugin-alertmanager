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
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return err.Error(), nil
	}

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
	channelID, err := p.resolveOrCreateChannel(team, channel)
	if err != nil {
		return fmt.Sprintf("Failed to resolve destination channel: %v", err), nil
	}

	// Atomic read-modify-write: acquire configWriteMu here, immediately
	// before the first getConfiguration read, and hold it through the save
	// below so a concurrent add/remove/reconcile can't clobber the merged
	// result (lost update). Deliberately NOT held during arg parsing or the
	// channel resolve above — those don't touch config state, so keeping the
	// lock off them minimizes contention.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	// Skip-check is scoped to the destination channel only. A receiver
	// named `high-cpu-usage--alert-slo-channel` MUST block creating it
	// again, but `high-cpu-usage--alert-sre-channel` in another channel
	// is independent (fan-out pattern). Walk current config once.
	current := p.getConfiguration().AlertConfigs
	existingInThisChannel := make(map[string]bool)
	for _, c := range current {
		if c.Channel == channel {
			existingInThisChannel[c.Name] = true
		}
	}

	// Two-pass: identify slugs that need creation, then create one shared
	// webhook for the whole batch. Channel-suffix every receiver name
	// (pattern <slug>--<channel>); the shared webhook itself is named
	// <group-or-slug>--<channel> in Mattermost.
	results := make([]scaffoldResult, 0, len(slugs))
	newSlugs := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		receiverName := receiverNameForChannel(slug, channel)
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
			// below shows the full picture.
			for _, slug := range newSlugs {
				results = append(results, scaffoldResult{receiverNameForChannel(slug, channel), "failed", hookErr.Error()})
			}
		} else {
			sharedHookID = hookID
			now := time.Now().UTC()
			for _, slug := range newSlugs {
				receiverName := receiverNameForChannel(slug, channel)
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
		// slices.Concat allocates a fresh backing array — guards against
		// the append-aliasing pitfall where reusing the source slice's
		// capacity would mutate p.getConfiguration().AlertConfigs in place.
		merged := slices.Concat(p.getConfiguration().AlertConfigs, newEntries)
		if err := p.saveConfigsLocked(merged); err != nil {
			_ = p.deleteIncomingWebhook(args.UserId, sharedHookID)
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

	// Group entries by base slug. Two-pass: collect, then emit ordered.
	grouped := make(map[string][]alertConfig)
	for _, ac := range entries {
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
	b.WriteString("routes:\n")
	for _, slug := range slugs {
		group := grouped[slug]
		for _, ac := range group {
			b.WriteString(fmt.Sprintf("  - matchers: [runbook=%q]\n", slug))
			b.WriteString(fmt.Sprintf("    receiver: %s\n", ac.Name))
			b.WriteString("    continue: true\n")
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
		rendered := renderReceiverYAML(entry.Name, p.webhookURLForReceiver(entry), entry.Channel, p.runbookDefaultURL(receiverBaseSlug(entry.Name)), p.siteURL()+webhookIconURL)
		y.WriteString(rendered)
		y.WriteString("\n")
	}
	return y.String()
}

// receiverNameForChannel constructs the channel-scoped receiver name
// from a runbook slug + channel slug. Pattern: <slug>--<channel>.
//
// The double-hyphen separator is deliberate: it's a visually obvious
// boundary that can't be confused with hyphens inside either component.
// `high-cpu-usage--alert-slo-channel` parses unambiguously as
// `(high-cpu-usage)--(alert-slo-channel)`.
func receiverNameForChannel(slug, channelSlug string) string {
	return slug + "--" + channelSlug
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
