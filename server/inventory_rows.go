package main

import (
	"fmt"
	"sort"
	"time"
)

// inventoryGroup represents one collapsible section on the inventory
// page. Group key (channel / team / AM URL) varies by ?group= param.
type inventoryGroup struct {
	// Label is what appears in the group header.
	Label string
	// SubLabel is a smaller secondary line (e.g., team name for a channel group).
	SubLabel string
	// ChannelURLPath, when non-empty, makes the group label a clickable
	// link to the channel in Mattermost (only set for channel-grouped view).
	ChannelURLPath string
	// AMReachable / AMStatus are populated for AM-URL-grouped view OR
	// for the per-receiver row in other views. Empty AMStatus = not probed.
	AMReachable bool
	AMStatus    string
	Receivers   []inventoryRow
}

// inventoryRow is one row in a group's table.
type inventoryRow struct {
	Name            string
	Team            string
	Channel         string
	ChannelURLPath  string // clickable link target ("/team/channels/channelname"), empty if unresolvable
	AlertManagerURL string
	AMReachable     bool
	AMStatus        string
	LoadedInAM      bool // receiver name found in AM's loaded YAML
	// LoadedViaOperator is true when the receiver is present only under the
	// Prometheus Operator's "<ns>/<config>/" prefix — i.e. loaded from an
	// AlertmanagerConfig CRD rather than flat alertmanager.yml.
	LoadedViaOperator bool

	// HealthLabel + HealthClass are pre-computed for the template
	// so the row can render an actionable status badge instead of
	// only an ambiguous colored dot. See computeHealth().
	HealthLabel string // "OK", "Not in AM YAML", "AM unreachable: timeout", etc.
	HealthClass string // "ok" | "warn" | "bad" — drives CSS color
}

// computeHealth fills HealthLabel + HealthClass based on the row's
// AM reachability + LoadedInAM state. Lets the template render the
// actual reason instead of just a colored dot.
//
// Precedence (worst first):
//  1. AM unreachable → "AM unreachable: <reason>"  (bad / red)
//  2. AM reachable but receiver not in YAML → "Not in AM YAML"  (warn / orange)
//  3. Healthy → "OK"  (ok / green)
//
// The "Not in AM YAML" case is the silent-failure mode: plugin thinks
// the receiver exists, but the user forgot to paste the assembled YAML
// into alertmanager.yml (or reloaded AM without picking it up).
func (r *inventoryRow) computeHealth() {
	if r.AlertManagerURL == "" {
		r.HealthLabel = "No AM URL configured"
		r.HealthClass = "warn"
		return
	}
	if !r.AMReachable {
		reason := r.AMStatus
		if reason == "" {
			reason = "unknown"
		}
		r.HealthLabel = "AM unreachable: " + reason
		r.HealthClass = "bad"
		return
	}
	if !r.LoadedInAM {
		r.HealthLabel = "Not in AM YAML"
		r.HealthClass = "warn"
		return
	}
	// Loaded, but via the Prometheus Operator's AlertmanagerConfig prefixing.
	// Still healthy — surfaced distinctly so an admin can see the receiver is
	// CRD-managed rather than present in a flat alertmanager.yml.
	if r.LoadedViaOperator {
		r.HealthLabel = "OK · via operator"
		r.HealthClass = "ok"
		return
	}
	r.HealthLabel = "OK"
	r.HealthClass = "ok"
}

// groupInventory bucketizes rows by the chosen mode. Result is sorted
// for deterministic output.
func groupInventory(rows []inventoryRow, mode string) []inventoryGroup {
	buckets := make(map[string][]inventoryRow)
	for _, r := range rows {
		key := groupKey(r, mode)
		buckets[key] = append(buckets[key], r)
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	groups := make([]inventoryGroup, 0, len(buckets))
	for _, key := range keys {
		entries := buckets[key]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		g := inventoryGroup{
			Label:     key,
			Receivers: entries,
		}
		switch mode {
		case "channel":
			// Channels are unique per team in MM, but receivers in the
			// same channel always share a team — pull from first entry.
			g.SubLabel = "Team: " + entries[0].Team
			g.ChannelURLPath = entries[0].ChannelURLPath
		case "team":
			g.SubLabel = fmt.Sprintf("%d receivers", len(entries))
		case "am":
			// AM URL grouping: surface reachability on the group header itself.
			if len(entries) > 0 {
				g.AMReachable = entries[0].AMReachable
				g.AMStatus = entries[0].AMStatus
			}
			g.SubLabel = fmt.Sprintf("%d receivers", len(entries))
		}
		groups = append(groups, g)
	}
	return groups
}

func groupKey(r inventoryRow, mode string) string {
	switch mode {
	case "team":
		return r.Team
	case "am":
		if r.AlertManagerURL == "" {
			return "(no AM URL)"
		}
		return r.AlertManagerURL
	default:
		return r.Channel
	}
}

func countDistinctChannels(configs []alertConfig) int {
	seen := make(map[string]bool)
	for _, c := range configs {
		seen[c.Channel] = true
	}
	return len(seen)
}

// humanDuration formats a duration as a short human-readable string
// for the reconciler health banner.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
