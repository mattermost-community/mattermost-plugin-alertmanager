package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// kvKeyAcknowledgedReceivers holds canonical Alertmanager receiver names that
// an admin has marked as intentional. The inventory still loads them from
// Alertmanager. It stops painting them orange. This is not a silence: the
// alerts those receivers send keep firing.
const kvKeyAcknowledgedReceivers = "acknowledged-receivers:v1"

const maxAcknowledgedReceivers = 500

func (p *Plugin) handleAcknowledge(args *model.CommandArgs) (string, error) {
	if err := p.requireSystemAdmin(args.UserId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	action := ""
	name := ""
	if len(fields) >= 3 {
		action = strings.ToLower(fields[2])
	}
	if len(fields) >= 4 {
		name = fields[3]
	}
	if len(fields) > 4 {
		return "A receiver name cannot contain spaces. Usage: `/alertmanager admin acknowledge add <name>`", nil
	}

	current, err := p.loadAcknowledgedReceivers()
	if err != nil {
		return fmt.Sprintf(":warning: Could not read acknowledged receivers: %v", err), nil
	}

	switch action {
	case "", "list":
		return formatAcknowledged(current), nil
	case "add":
		canonical, nerr := normalizeAcknowledgedName(name)
		if nerr != nil {
			return ":warning: " + nerr.Error(), nil
		}
		if slices.Contains(current, canonical) {
			return fmt.Sprintf("`%s` is already acknowledged.", canonical), nil
		}
		if len(current) >= maxAcknowledgedReceivers {
			return fmt.Sprintf(":warning: The acknowledged list is at its cap of %d. Remove one first.", maxAcknowledgedReceivers), nil
		}
		next := append(append([]string{}, current...), canonical)
		slices.Sort(next)
		if serr := p.saveAcknowledgedReceivers(next); serr != nil {
			return fmt.Sprintf(":warning: Could not save the acknowledged list: %v", serr), nil
		}
		p.auditLog("acknowledge.add", args.UserId, canonical, args.ChannelId, "success")
		return fmt.Sprintf("Acknowledged `%s`.\n\nThe inventory will stop listing it as AM-only. Alerts it sends still fire. This is not `/alertmanager silence`.", canonical), nil
	case "remove":
		canonical, nerr := normalizeAcknowledgedName(name)
		if nerr != nil {
			return ":warning: " + nerr.Error(), nil
		}
		next := make([]string, 0, len(current))
		found := false
		for _, item := range current {
			if item == canonical {
				found = true
				continue
			}
			next = append(next, item)
		}
		if !found {
			return fmt.Sprintf("`%s` is not in the acknowledged list.", canonical), nil
		}
		if serr := p.saveAcknowledgedReceivers(next); serr != nil {
			return fmt.Sprintf(":warning: Could not save the acknowledged list: %v", serr), nil
		}
		p.auditLog("acknowledge.remove", args.UserId, canonical, args.ChannelId, "success")
		return fmt.Sprintf("Removed `%s`. The inventory will show it as AM-only again if Alertmanager still has it and the plugin did not create it.", canonical), nil
	default:
		return "Usage: `/alertmanager admin acknowledge`, `/alertmanager admin acknowledge add <name>`, or `/alertmanager admin acknowledge remove <name>`", nil
	}
}

func formatAcknowledged(names []string) string {
	if len(names) == 0 {
		return "No acknowledged receivers.\n\n`/alertmanager admin acknowledge add <name>` hides a receiver from the orange AM-only list. Use this when another system created the receiver on purpose. It does not mute alerts."
	}
	var b strings.Builder
	b.WriteString("**Acknowledged receivers** (hidden from the AM-only warning):\n\n")
	for _, name := range names {
		fmt.Fprintf(&b, "- `%s`\n", name)
	}
	b.WriteString("\nThese receivers still send alerts. `/alertmanager admin acknowledge remove <name>` puts the warning back.")
	return b.String()
}

func (p *Plugin) loadAcknowledgedReceivers() ([]string, error) {
	data, appErr := p.API.KVGet(kvKeyAcknowledgedReceivers)
	if appErr != nil {
		return nil, fmt.Errorf("read acknowledged receivers: %w", appErr)
	}
	return parseAcknowledgedReceivers(data)
}

func (p *Plugin) saveAcknowledgedReceivers(names []string) error {
	raw, err := json.Marshal(names)
	if err != nil {
		return err
	}
	if appErr := p.API.KVSet(kvKeyAcknowledgedReceivers, raw); appErr != nil {
		return fmt.Errorf("write acknowledged receivers: %w", appErr)
	}
	return nil
}

func parseAcknowledgedReceivers(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, fmt.Errorf("acknowledged receiver list is not valid JSON: %w", err)
	}
	return names, nil
}

// normalizeAcknowledgedName stores the last path segment. Alertmanager's
// operator shows receivers as namespace/config/name, and the inventory
// compares that short name.
func normalizeAcknowledgedName(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("a receiver name is required")
	}
	if strings.ContainsAny(s, " \t\r\n`\"'") {
		return "", fmt.Errorf("receiver name %q contains characters the inventory cannot match", raw)
	}
	if len(s) > 253 {
		return "", fmt.Errorf("receiver name is longer than 253 characters")
	}
	canonical, _ := canonicalReceiverName(s)
	if canonical == "null" || canonical == "blackhole" {
		return "", fmt.Errorf("`%s` is already ignored. Alertmanager always has it", canonical)
	}
	return canonical, nil
}

func acknowledgedSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}
