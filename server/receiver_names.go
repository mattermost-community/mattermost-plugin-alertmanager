package main

import (
	"strings"
)

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

// webhookDisplayNameFor builds the Mattermost incoming-webhook display name.
// It deliberately mirrors the Alertmanager receiver-name format
// (<base>--<team>-<channel>) so the System Console → Integrations → Incoming
// Webhooks list reads the same way as the receiver names, and the team segment
// disambiguates channels that repeat across teams (`town-square` is in every
// team — channel name alone is not unique). For an individual runbook add,
// `base` is the runbook slug, so the display name is identical to the receiver
// name; for a group add it's the category, since one webhook serves the whole
// category. No "Alertmanager:" prefix — the bot identity on the posts already
// makes ownership obvious, and dropping it buys headroom against the 64-char
// display-name cap (createIncomingWebhook truncates past that).
func webhookDisplayNameFor(base, teamSlug, channelSlug string) string {
	return base + "--" + teamSlug + "-" + channelSlug
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
