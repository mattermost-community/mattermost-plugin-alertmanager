package main

import (
	"strings"
	"testing"
)

// TestScaffoldRunbooksRender guards the render path for every runbook the
// scaffold can offer. For each slug it asserts the generated slack_configs
// receiver:
//
//	(a) leaves no plugin placeholder ({{NAME}}, {{QUICK_DIAGNOSTICS}}, ...)
//	    unresolved,
//	(b) has its inline Quick Diagnostics block injected — which also proves
//	    the runbook .md exists and has a "## Quick diagnostics" section, and
//	(c) parses as valid Alertmanager config (via AM's own loader).
//
// Without this, a malformed fence or an odd <placeholder> in a new runbook
// only surfaces at `/alertmanager add` time, never in CI. This exercises the
// otherwise-untested renderReceiverYAML path across all shipped runbooks.
func TestScaffoldRunbooksRender(t *testing.T) {
	// Unique slugs across every category set. `all` is nil (resolved at
	// runtime from the embedded FS), so skip it — the category lists
	// enumerate the same slugs explicitly.
	seen := map[string]bool{}
	var slugs []string
	for set, list := range scaffoldSets {
		if set == "all" {
			continue
		}
		for _, s := range list {
			if !seen[s] {
				seen[s] = true
				slugs = append(slugs, s)
			}
		}
	}
	if len(slugs) == 0 {
		t.Fatal("scaffoldSets resolved to zero slugs — map misconfigured")
	}

	placeholders := []string{
		"{{NAME}}", "{{URL}}", "{{CHANNEL}}",
		"{{RUNBOOK_DEFAULT}}", "{{ICON_URL}}", "{{QUICK_DIAGNOSTICS}}",
	}

	for _, slug := range slugs {
		t.Run(slug, func(t *testing.T) {
			block := renderReceiverYAML(
				slug,
				"http://localhost:8065/hooks/aaaaaaaaaaaaaaaaaaaaaaaaaa",
				"alerts",
				"http://localhost:8065/plugins/com.mattermost.alertmanager/public/runbooks/"+slug+".html",
				"http://localhost:8065/plugins/com.mattermost.alertmanager/public/alertmanager-logo.png",
			)

			// (a) every plugin placeholder was substituted.
			for _, ph := range placeholders {
				if strings.Contains(block, ph) {
					t.Errorf("unresolved placeholder %s in rendered receiver", ph)
				}
			}

			// (b) inline diagnostics were injected.
			if !strings.Contains(block, "**Quick diagnostics:**") {
				t.Errorf("no Quick diagnostics rendered — missing runbook file or section?")
			}

			// (c) Alertmanager can parse it. Wrap the receiver (which
			// begins with a column-0 `- name:` sequence, valid directly
			// under `receivers:`) in a minimal config that references it.
			cfg := "route:\n  receiver: " + slug + "\nreceivers:\n" + block
			if err := validateMergedConfig(cfg); err != nil {
				t.Errorf("rendered receiver is not valid Alertmanager config: %v\n---\n%s", err, cfg)
			}
		})
	}
}
