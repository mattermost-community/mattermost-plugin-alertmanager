package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// handleRepair is the sysadmin-only recovery path for a corrupt receiver-list KV
// value (F-005). Normal mutating commands parse the current KV blob before
// transforming it, so if that blob is invalid (bad JSON, duplicate names, …) they
// all fail — and the plugin can't fix itself through its own command surface. The
// load path already keeps the plugin UP on last-known in-memory state; this
// command lets an admin write that known-good in-memory snapshot back over the
// corrupt KV value (with an explicit --force, since it's destructive).
//
//	/alertmanager repair          → diagnose (is KV valid? how many in memory?)
//	/alertmanager repair --force  → overwrite KV from the in-memory snapshot
func (p *Plugin) handleRepair(args *model.CommandArgs) (string, error) {
	if err := p.requireSystemAdmin(args.UserId); err != nil {
		return err.Error(), nil
	}
	fields := strings.Fields(args.Command)
	force := len(fields) >= 3 && fields[2] == "--force"
	return p.repairAlertConfigsKV(args.UserId, args.ChannelId, force), nil
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
				"```\n/alertmanager repair --force\n```\n"+
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
		return ":warning: The receiver list in KV changed while repairing (another node may have already fixed it). Re-run `/alertmanager repair` to re-check before forcing."
	}
	go p.broadcastConfigReload() // peers reload the now-valid KV
	p.auditLog("config.repair", userID, "", channelID, "success")
	return fmt.Sprintf(":white_check_mark: Repaired the receiver-list KV value from the in-memory snapshot (**%d** receiver(s)). Peers notified to reload.", len(inMemory))
}
