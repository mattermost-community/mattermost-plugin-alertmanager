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

	raw, appErr := p.API.KVGet(kvKeyAlertConfigs)
	if appErr != nil {
		return fmt.Sprintf(":warning: Could not read the receiver-list KV key: %v", appErr), nil
	}
	inMemory := p.getConfiguration().AlertConfigs

	// If KV parses cleanly there's nothing to repair — say so rather than let an
	// admin destructively overwrite a healthy list.
	if parsed, perr := parseAlertConfigs(string(raw)); perr == nil {
		return fmt.Sprintf(":white_check_mark: The receiver list in KV is valid (%d receiver(s)); no repair needed. (In-memory: %d.)", len(parsed), len(inMemory)), nil
	} else if !force {
		return fmt.Sprintf(
			":warning: The receiver list in KV is UNREADABLE: %v\n\n"+
				"The plugin is running on its last-known in-memory list (**%d** receiver(s)). "+
				"To overwrite the corrupt KV value with that in-memory list, re-run:\n\n"+
				"```\n/alertmanager repair --force\n```\n"+
				":warning: **Destructive** — anything in the corrupt blob not present in the in-memory list is lost. "+
				"Copy the raw KV value out first if you need to inspect it.",
			perr, len(inMemory)), nil
	}

	// Force path: serialize the known-good in-memory snapshot and write it over the
	// corrupt value. Unconditional KVSet (not CAS) — CAS would key off the corrupt
	// bytes we're trying to replace. Held under configWriteMu so it serializes with
	// normal writers.
	newBytes, merr := json.MarshalIndent(inMemory, "", "  ")
	if merr != nil {
		return fmt.Sprintf(":warning: Failed to serialize the in-memory receiver list: %v", merr), nil
	}
	p.configWriteMu.Lock()
	setErr := p.API.KVSet(kvKeyAlertConfigs, newBytes)
	p.configWriteMu.Unlock()
	if setErr != nil {
		return fmt.Sprintf(":warning: Failed to write the repaired receiver list to KV: %v", setErr), nil
	}
	go p.broadcastConfigReload() // peers reload the now-valid KV
	p.auditLog("config.repair", args.UserId, "", args.ChannelId, "success")
	return fmt.Sprintf(":white_check_mark: Repaired the receiver-list KV value from the in-memory snapshot (**%d** receiver(s)). Peers notified to reload.", len(inMemory)), nil
}
