package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// handleAddCustom creates ONE generic (non-runbook) receiver named
// <name>--<team>-<channel> with its own Mattermost webhook. Unlike handleAdd it
// does NOT emit a `runbook=` route — the receiver is marked Custom and the user
// wires the matcher manually (export renders a commented stub). Mirrors the
// handleAdd persistence discipline: configWriteMu held across the read-modify-write,
// webhook rolled back if the save fails.
//
//	/alertmanager config add-custom <am-url> <name> [webhook-host=<url>]
//
// Like handleAdd, the destination is the channel the command runs in.
func (p *Plugin) handleAddCustom(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	rest := fields[2:]

	if msg := removedAddSyntaxMessage(rest); msg != "" {
		return msg, nil
	}

	webhookHostOverride, rest := extractFlagValue(rest, "webhook-host=")
	if webhookHostOverride != "" {
		if err := validateWebhookHost(webhookHostOverride); err != nil {
			return fmt.Sprintf("Invalid `webhook-host` value: %s", p.amErrorText(args.UserId, err)), nil
		}
	}

	namespace, rest := extractFlagValue(rest, "namespace=")
	if namespace == "" {
		namespace = defaultCRDNamespace
	} else if err := validateCRDNamespace(namespace); err != nil {
		return fmt.Sprintf(":warning: Invalid `namespace`: %v", err), nil
	}

	format, rotationOptIn, positional := splitAddWords(rest)
	if len(positional) != 2 {
		return addCustomUsageMessage(), nil
	}
	amURL, rawName := strings.TrimRight(positional[0], "/"), positional[1]
	// Reject a dangerous AM URL up front (CL-03/CL-21) — see handleAdd.
	if err := validateAlertManagerURL(amURL); err != nil {
		return fmt.Sprintf("Invalid Alertmanager URL: %s", p.amErrorText(args.UserId, err)), nil
	}

	dest, msg := p.currentChannelDestination(args)
	if msg != "" {
		return msg, nil
	}
	channelID, team, channel := dest.channelID, dest.team, dest.channel

	receiverName, err := validateCustomReceiverName(rawName, team, channel)
	if err != nil {
		return ":warning: " + err.Error(), nil
	}

	// Atomic read-modify-write — same discipline as handleAdd.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	for _, c := range p.getConfiguration().AlertConfigs {
		if c.Team == team && c.Channel == channel && c.Name == receiverName {
			return fmt.Sprintf(":warning: Receiver `%s` already exists in this channel.", receiverName), nil
		}
	}

	// Mirror the receiver-name format; for an individual add this is exactly
	// the receiver name (<slug>--<team>-<channel>).
	webhookDisplayName := webhookDisplayNameFor(receiverBaseSlug(receiverName), team, channel)
	hookID, hookErr := p.createIncomingWebhook(args.UserId, channelID, webhookDisplayName)
	if hookErr != nil {
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

		RotationRemindersEnabled: rotationOptIn,
	}

	_, _, err = p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		if capErr := enforceReceiverCap(len(current), 1); capErr != nil {
			return nil, capErr
		}
		return slices.Concat(current, []alertConfig{entry}), nil
	})
	if err != nil {
		warn := ""
		if delErr := p.deleteIncomingWebhook(args.UserId, hookID); delErr != nil {
			p.API.LogWarn("add-custom rollback: could not delete webhook (orphan may remain)", "webhook", redactHookID(hookID), "err", delErr.Error())
			warn = webhookRollbackWarning(webhookDisplayNameFor(receiverBaseSlug(receiverName), team, channel))
		}
		return fmt.Sprintf("Failed to persist custom receiver (rolled back webhook): %v%s", err, warn), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, ":white_check_mark: Created custom receiver `%s`\n", receiverName)
	fmt.Fprintf(&b, "Webhook created and bound to `%s` (team `%s`). No runbook is attached — alerts render as raw content.\n\n", channel, team)
	b.WriteString(":warning: **No route exists yet — you must wire it manually.** Add a matcher selecting the alerts you want here, e.g.:\n\n")
	b.WriteString("```yaml\n")
	b.WriteString("  - matchers: [ alertname=\"MyCustomAlert\" ]   # <-- set to labels YOUR rules emit\n")
	fmt.Fprintf(&b, "    receiver: %s\n", receiverName)
	b.WriteString("    continue: true\n")
	b.WriteString("```\n\n")

	if format == formatCRD {
		manifest, crCount := p.assembleCRDManifest([]alertConfig{entry}, namespace)
		if dmErr := p.dmCRDBundle(args.UserId, manifest, crCount, namespace, nil); dmErr != nil {
			p.API.LogWarn("add-custom: couldn't DM CRD manifest; falling back to inline", "err", dmErr.Error())
			fmt.Fprintf(&b, ":warning: Couldn't DM the manifest (%s). Inline copy below — review and `kubectl apply -n %s`:\n\n```yaml\n%s```\n", dmErr.Error(), namespace, manifest)
		} else {
			fmt.Fprintf(&b, ":page_facing_up: **Sent `alertmanager-config.yaml` (%d AlertmanagerConfig, v1alpha1) to your DM with `@%s`.** Open it, review, wire the route stub inside it, then apply:\n\n```\nkubectl apply -n %s -f alertmanager-config.yaml\n```\n:book: [Kubernetes guide](%s)", crCount, webhookUsername, namespace, p.helpPageURL("KUBERNETES.md"))
		}
		return b.String(), nil
	}

	// Runbook-free receiver block plus the commented route stub, DM'd to the caller.
	receiverYAML := "# Custom (non-runbook) receiver created by /alertmanager config add-custom.\n" +
		"# Paste under `receivers:` in your alertmanager.yml.\n\n" +
		renderReceiverYAMLForKind(entry.Name, p.webhookURLForReceiver(entry), channel, "", p.siteURL()+webhookIconURL, true)
	routesYAML := assembleRoutesYAML([]alertConfig{entry})

	if dmErr := p.dmYAMLBundle(args.UserId, receiverYAML, routesYAML, 1, amURL, nil); dmErr != nil {
		// DM failed — inline the receiver block so the caller isn't left unable
		// to retrieve it (export is channel-scoped and may be unreachable).
		p.API.LogWarn("add-custom: couldn't DM receiver YAML; falling back to inline", "err", dmErr.Error())
		b.WriteString(":warning: Couldn't DM the receiver block (")
		b.WriteString(dmErr.Error())
		b.WriteString("). Inline copy below — paste under `receivers:` in your `alertmanager.yml`:\n\n```yaml\n")
		b.WriteString(receiverYAML)
		b.WriteString("```\n")
	} else {
		fmt.Fprintf(&b, ":page_facing_up: **Sent the receiver block + route stub to your DM with `@%s`.** See the [Configuration guide](%s) for the walkthrough.", webhookUsername, p.helpPageURL("CONFIGURATION.md"))
	}
	return b.String(), nil
}

// addCustomUsageMessage is shown when /alertmanager config add-custom is called with
// the wrong number of positional args.
func addCustomUsageMessage() string {
	return "Usage: `/alertmanager config add-custom <am-url> <name> [standard|crd] [on] [namespace=<ns>] [webhook-host=<url>]`\n\n" +
		"Run it in the channel that should receive the alerts. Creates ONE generic (non-runbook) receiver named `<name>--<team>-<channel>` with its own Mattermost webhook. " +
		"Unlike `/alertmanager config add`, it does NOT generate a `runbook=` route — you wire the matcher yourself " +
		"(see the Configuration page from `/alertmanager docs`). `<name>`: lowercase `[a-z0-9_-]`, no `--`, and not a runbook slug, category set, or one of `standard`, `crd`, `on`. " +
		"`standard` (default) DMs plain alertmanager.yml receivers; `crd` DMs Prometheus Operator objects. `on` opts into rotation reminders. " +
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
	if name == packReceiverSlug || name == legacyPackReceiverSlug {
		return "", fmt.Errorf("name %q is reserved for the Mattermost alerts — use `/alertmanager config add <am-url> %s mm-namespace=<ns>` instead", name, mattermostGroup)
	}
	if slices.Contains(runbookSlugs(), name) {
		return "", fmt.Errorf("name %q is a runbook slug — use `/alertmanager config add <am-url> %s` for the runbook flow, or pick a different custom name", name, name)
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
