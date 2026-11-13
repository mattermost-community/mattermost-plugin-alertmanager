package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

const repairUsage = "Usage: `/alertmanager admin repair [prune] [force]`"

// handleRepair is the sysadmin-only health command. It covers the two ways the
// plugin's receiver list can drift from reality:
//
//   - A corrupt receiver-list KV value (F-005). Normal mutating commands parse
//     the current KV blob before transforming it, so an invalid blob (bad JSON,
//     duplicate names, …) makes them all fail. The plugin keeps running on its
//     last-known in-memory list, and force writes that snapshot back.
//   - Orphans: receivers whose Mattermost webhook was deleted out of band. A
//     background job prunes these every 5 minutes. prune runs it now.
//
// The KV check always runs first. Pruning rewrites the same KV value, so it
// cannot succeed on a corrupt blob.
//
//	admin repair                  → read-only: KV check plus a preview of orphans
//	admin repair prune          → also remove the orphaned receivers
//	admin repair force          → overwrite a corrupt KV value from memory
func (p *Plugin) handleRepair(args *model.CommandArgs) (string, error) {
	if err := p.requireSystemAdmin(args.UserId); err != nil {
		return err.Error(), nil
	}
	force, prune, ok := parseRepairFlags(strings.Fields(args.Command)[2:])
	if !ok {
		return repairUsage, nil
	}
	kv := p.repairAlertConfigsKV(args.UserId, args.ChannelId, force)
	return kv + "\n\n" + p.orphanSection(args.UserId, prune), nil
}

// parseRepairFlags rejects anything but force and prune. A typo must not
// fall through to a bare run, which would look like it did the repair.
func parseRepairFlags(args []string) (force, prune, ok bool) {
	for _, a := range args {
		switch a {
		case "force":
			force = true
		case "prune":
			prune = true
		default:
			return false, false, false
		}
	}
	return force, prune, true
}

// orphanSection reports receivers whose webhook no longer exists. With prune it
// removes them, otherwise it only lists what a prune would remove.
func (p *Plugin) orphanSection(userID string, prune bool) string {
	if !prune {
		orphans, err := p.findOrphanedReceivers(userID)
		if err != nil {
			return fmt.Sprintf(":warning: Could not check for orphaned receivers: %v", err)
		}
		if len(orphans) == 0 {
			return ":white_check_mark: No orphaned receivers. Every registered webhook still exists in Mattermost."
		}
		return fmt.Sprintf(
			":mag: %d receiver(s) point at a deleted webhook:\n\n- `%s`\n\nRemove them now with `/alertmanager admin repair prune`. The background job also does this every 5 minutes.",
			len(orphans), strings.Join(orphans, "`\n- `"))
	}

	pruned, err := p.reconcileOrphans(userID)
	if err != nil {
		return fmt.Sprintf(":warning: Prune failed: %v\n\nIf the receiver-list KV value is unreadable, fix it first with `/alertmanager admin repair force`.", err)
	}
	if len(pruned) == 0 {
		return ":white_check_mark: Nothing to prune. Every registered webhook still exists in Mattermost."
	}
	return fmt.Sprintf(
		":wastebasket: Pruned %d orphaned receiver(s) whose webhooks were deleted out of band:\n\n- `%s`\n\n"+
			"_Delete the matching `slack_configs` blocks from your `alertmanager.yml` if you have not already, then reload Alertmanager._",
		len(pruned), strings.Join(pruned, "`\n- `"))
}

// repairAlertConfigsKV is the KV-recovery core of handleRepair, split out (no
// auth) so the CAS/validation behavior is unit-testable.
func (p *Plugin) repairAlertConfigsKV(userID, channelID string, force bool) string {
	// Hold configWriteMu across read → parse → snapshot → write so a same-node
	// writer can't interleave; a cross-node race is caught by the compare-and-set
	// below (F-002).
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	raw, appErr := p.API.KVGet(kvKeyAlertConfigs)
	if appErr != nil {
		return fmt.Sprintf(":warning: Could not read the receiver-list KV key: %v", appErr)
	}
	inMemory := p.getConfiguration().AlertConfigs

	// If KV parses cleanly there's nothing to repair — say so rather than let an
	// admin destructively overwrite a healthy list.
	if parsed, perr := parseAlertConfigs(string(raw)); perr == nil {
		return fmt.Sprintf(":white_check_mark: The receiver list in KV is valid (%d receiver(s)); no repair needed. (In-memory: %d.)", len(parsed), len(inMemory))
	} else if !force {
		return fmt.Sprintf(
			":warning: The receiver list in KV is UNREADABLE: %v\n\n"+
				"The plugin is running on its last-known in-memory list (**%d** receiver(s)). "+
				"To overwrite the corrupt KV value with that in-memory list, re-run:\n\n"+
				"```\n/alertmanager admin repair force\n```\n"+
				":warning: **Destructive** — anything in the corrupt blob not present in the in-memory list is lost. "+
				"Copy the raw KV value out first if you need to inspect it.",
			perr, len(inMemory))
	}

	// Force path: serialize the known-good in-memory snapshot and validate it
	// BEFORE persisting — never replace one unreadable blob with another (F-003).
	snapshotBytes, merr := json.MarshalIndent(inMemory, "", "  ")
	if merr != nil {
		return fmt.Sprintf(":warning: Failed to serialize the in-memory receiver list: %v", merr)
	}
	parsed, verr := parseAlertConfigs(string(snapshotBytes))
	if verr != nil {
		return fmt.Sprintf(":warning: The in-memory snapshot is itself invalid (%v) — refusing to write it. The KV value needs manual repair.", verr)
	}
	// Persist the SANITIZED parsed result, not the raw snapshot: parseAlertConfigs
	// neuters e.g. a stale ?/# or embedded creds in a pre-hardening stored URL, so
	// repair writes a clean value rather than round-tripping the un-sanitized one.
	newBytes, merr := json.MarshalIndent(parsed, "", "  ")
	if merr != nil {
		return fmt.Sprintf(":warning: Failed to serialize the sanitized receiver list: %v", merr)
	}
	// F-002: compare-and-set on the EXACT corrupt bytes we read. If another node
	// already changed KV (e.g. repaired it) since our read, abort rather than
	// clobber the newer valid state with our stale snapshot.
	set, appErr := p.API.KVCompareAndSet(kvKeyAlertConfigs, raw, newBytes)
	if appErr != nil {
		return fmt.Sprintf(":warning: Failed to write the repaired receiver list to KV: %v", appErr)
	}
	if !set {
		return ":warning: The receiver list in KV changed while repairing (another node may have already fixed it). Re-run `/alertmanager admin repair` to re-check before forcing."
	}
	// Apply the sanitized parsed list to THIS node's memory now — the CAS only
	// fixed KV. Without this, the writing node keeps serving its stale (possibly
	// un-sanitized, pre-hardening) in-memory snapshot until the next periodic
	// reload; peers converge via the broadcast below (finding: repair leaves local
	// memory unsanitized until reload).
	p.applyAlertConfigsToMemory(parsed)
	go p.broadcastConfigReload() // peers reload the now-valid KV
	p.auditLog("config.repair", userID, "", channelID, "success")
	return fmt.Sprintf(":white_check_mark: Repaired the receiver-list KV value from the in-memory snapshot (**%d** receiver(s)). Peers notified to reload.", len(inMemory))
}
