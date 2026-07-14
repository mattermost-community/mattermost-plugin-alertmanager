package main

import (
	"sort"
	"strings"
	"testing"
)

// TestReceiverNameForChannel pins the team-qualified suffixing pattern used to
// keep receiver names globally unique. Team is in the name because channel
// names are unique only per team — without it, same-named channels in
// different teams would collide. The double-hyphen after the slug is
// load-bearing: it stays unambiguous when parsed back out by receiverBaseSlug.
func TestReceiverNameForChannel(t *testing.T) {
	cases := []struct {
		slug, team, channel, want string
	}{
		{"high-cpu-usage", "ops", "alerts", "high-cpu-usage--ops-alerts"},
		{"high-cpu-usage", "sre", "alert-slo-channel", "high-cpu-usage--sre-alert-slo-channel"},
		{"pod-not-ready", "platform", "ops", "pod-not-ready--platform-ops"},
		// Same channel name, different teams → distinct receiver names.
		// This is the collision the team segment exists to prevent.
		{"pod-crashloopbackoff", "team-a", "town-square", "pod-crashloopbackoff--team-a-town-square"},
		{"pod-crashloopbackoff", "team-b", "town-square", "pod-crashloopbackoff--team-b-town-square"},
	}

	for _, tc := range cases {
		t.Run(tc.slug+"/"+tc.team+"/"+tc.channel, func(t *testing.T) {
			got := receiverNameForChannel(tc.slug, tc.team, tc.channel)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestReceiverBaseSlug covers the inverse: extracting the runbook slug
// portion from a channel-suffixed receiver name. Crucial for the
// runbook URL fallback — that lookup is keyed by slug, not by full
// receiver name, so getting this parse wrong silently routes users to
// the wrong docs.
func TestReceiverBaseSlug(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// Channel-suffixed: the slug portion before the first `--`.
		{"high-cpu-usage--alerts", "high-cpu-usage"},
		{"high-cpu-usage--alert-slo-channel", "high-cpu-usage"},
		{"pod-not-ready--ops", "pod-not-ready"},
		// Team-qualified form: slug is still everything before the first `--`,
		// the team-channel tail is ignored.
		{"pod-crashloopbackoff--team-a-town-square", "pod-crashloopbackoff"},

		// Unsuffixed names pass through unchanged. Treated as a legacy
		// shape — receivers created before channel-suffixing existed
		// keep working without rewrite.
		{"high-cpu-usage", "high-cpu-usage"},
		{"plain", "plain"},

		// Pathological: only-separator inputs. We return the part
		// before `--` since that's the documented contract. An empty
		// string here means the caller fed us garbage, but the
		// function shouldn't panic.
		{"--alerts", "--alerts"},
		{"slug--", "slug"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := receiverBaseSlug(tc.input)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestResolveAddTarget covers the classifier that decides whether an
// /alertmanager add [target] arg is a group set, an individual runbook
// slug, or invalid. The (group, slugs) shape it returns drives every
// downstream decision: webhook display name, GroupName field on each
// receiver, and which receivers get created.
func TestResolveAddTarget(t *testing.T) {
	t.Run("all returns full runbook list as `all` group", func(t *testing.T) {
		group, slugs, err := resolveAddTarget("all")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if group != "all" {
			t.Fatalf("expected group=all, got %q", group)
		}
		want := runbookSlugs()
		if len(slugs) != len(want) {
			t.Fatalf("expected %d slugs, got %d", len(want), len(slugs))
		}
	})

	t.Run("empty target resolves to all (via caller default)", func(t *testing.T) {
		// resolveAddTarget itself doesn't substitute "" → "all"; the
		// caller in handleAdd does. Verify the empty case errors so
		// callers don't accidentally rely on it.
		_, _, err := resolveAddTarget("")
		if err == nil {
			t.Fatal("expected error for empty target")
		}
	})

	t.Run("category set returns subset with category as group name", func(t *testing.T) {
		group, slugs, err := resolveAddTarget("compute")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if group != "compute" {
			t.Fatalf("expected group=compute, got %q", group)
		}
		want := scaffoldSets["compute"]
		if len(slugs) != len(want) {
			t.Fatalf("expected %d slugs, got %d", len(want), len(slugs))
		}
	})

	t.Run("individual runbook slug returns single-element slice with slug as group name", func(t *testing.T) {
		// Pick any real slug from the embedded set.
		all := runbookSlugs()
		if len(all) == 0 {
			t.Skip("no runbooks embedded — can't run individual-slug test")
		}
		slug := all[0]
		group, slugs, err := resolveAddTarget(slug)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if group != slug {
			t.Fatalf("expected group=%q, got %q", slug, group)
		}
		if len(slugs) != 1 || slugs[0] != slug {
			t.Fatalf("expected [%q], got %v", slug, slugs)
		}
	})

	t.Run("case-insensitive match on category", func(t *testing.T) {
		group, _, err := resolveAddTarget("COMPUTE")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if group != "compute" {
			t.Fatalf("expected group lowercased, got %q", group)
		}
	})

	t.Run("unknown target returns helpful error", func(t *testing.T) {
		_, _, err := resolveAddTarget("not-a-real-thing")
		if err == nil {
			t.Fatal("expected error for unknown target")
		}
		if !strings.Contains(err.Error(), "unknown target") {
			t.Fatalf("expected error mentioning target, got %q", err.Error())
		}
	})
}

// TestWebhookStillReferenced covers the refcount predicate used by the
// remove path to decide whether to delete a Mattermost webhook after
// pruning the receiver.
func TestWebhookStillReferenced(t *testing.T) {
	entries := []alertConfig{
		{Name: "a", WebhookID: "w1"},
		{Name: "b", WebhookID: "w1"},
		{Name: "c", WebhookID: "w2"},
	}

	if !webhookStillReferenced(entries, "w1") {
		t.Error("w1 should still be referenced (a and b)")
	}
	if !webhookStillReferenced(entries, "w2") {
		t.Error("w2 should still be referenced (c)")
	}
	if webhookStillReferenced(entries, "w3") {
		t.Error("w3 should not be referenced")
	}
	if webhookStillReferenced([]alertConfig{}, "w1") {
		t.Error("empty slice should never be referenced")
	}
}

// TestOrphanedWebhookIDs covers the post-remove cleanup helper. The
// function answers "which webhooks lost their last receiver?" by
// diffing two snapshots — load-bearing for not deleting webhooks
// that other channels' receivers still depend on.
func TestOrphanedWebhookIDs(t *testing.T) {
	t.Run("nothing removed produces no orphans", func(t *testing.T) {
		entries := []alertConfig{
			{Name: "a", WebhookID: "w1"},
			{Name: "b", WebhookID: "w1"},
		}
		got := orphanedWebhookIDs(entries, entries)
		if len(got) != 0 {
			t.Fatalf("expected no orphans, got %v", got)
		}
	})

	t.Run("removing last reference orphans the webhook", func(t *testing.T) {
		before := []alertConfig{
			{Name: "a", WebhookID: "w1"},
		}
		after := []alertConfig{}
		got := orphanedWebhookIDs(before, after)
		if len(got) != 1 || got[0] != "w1" {
			t.Fatalf("expected [w1], got %v", got)
		}
	})

	t.Run("removing partial group keeps webhook alive", func(t *testing.T) {
		before := []alertConfig{
			{Name: "a", WebhookID: "w1", GroupName: "compute"},
			{Name: "b", WebhookID: "w1", GroupName: "compute"},
			{Name: "c", WebhookID: "w1", GroupName: "compute"},
		}
		after := []alertConfig{
			{Name: "b", WebhookID: "w1", GroupName: "compute"},
			{Name: "c", WebhookID: "w1", GroupName: "compute"},
		}
		got := orphanedWebhookIDs(before, after)
		if len(got) != 0 {
			t.Fatalf("expected no orphans (w1 still has 2 refs), got %v", got)
		}
	})

	t.Run("removing entire group orphans the shared webhook", func(t *testing.T) {
		before := []alertConfig{
			{Name: "a", WebhookID: "w1", GroupName: "compute"},
			{Name: "b", WebhookID: "w1", GroupName: "compute"},
			{Name: "c", WebhookID: "w2", GroupName: "database"},
		}
		after := []alertConfig{
			{Name: "c", WebhookID: "w2", GroupName: "database"},
		}
		got := orphanedWebhookIDs(before, after)
		if len(got) != 1 || got[0] != "w1" {
			t.Fatalf("expected [w1], got %v", got)
		}
	})

	t.Run("removing one of multiple webhooks produces a single orphan", func(t *testing.T) {
		before := []alertConfig{
			{Name: "a", WebhookID: "w1"},
			{Name: "b", WebhookID: "w2"},
			{Name: "c", WebhookID: "w3"},
		}
		after := []alertConfig{
			{Name: "a", WebhookID: "w1"},
			{Name: "c", WebhookID: "w3"},
		}
		got := orphanedWebhookIDs(before, after)
		if len(got) != 1 || got[0] != "w2" {
			t.Fatalf("expected [w2], got %v", got)
		}
	})

	t.Run("removing all receivers orphans all webhooks (deterministic order)", func(t *testing.T) {
		before := []alertConfig{
			{Name: "a", WebhookID: "w2"},
			{Name: "b", WebhookID: "w1"},
			{Name: "c", WebhookID: "w3"},
		}
		got := orphanedWebhookIDs(before, []alertConfig{})
		// Should preserve order of first appearance.
		want := []string{"w2", "w1", "w3"}
		if len(got) != len(want) {
			t.Fatalf("expected %v, got %v", want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("at index %d expected %q, got %q (full: %v)", i, want[i], got[i], got)
			}
		}
		// Sanity: sorted comparison should match too (no missing/extra)
		sortedGot := make([]string, len(got))
		copy(sortedGot, got)
		sortedWant := make([]string, len(want))
		copy(sortedWant, want)
		sort.Strings(sortedGot)
		sort.Strings(sortedWant)
		for i := range sortedWant {
			if sortedGot[i] != sortedWant[i] {
				t.Fatalf("sorted comparison mismatch: got %v want %v", sortedGot, sortedWant)
			}
		}
	})
}
