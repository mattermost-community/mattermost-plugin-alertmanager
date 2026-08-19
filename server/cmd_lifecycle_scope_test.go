package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// fakeScopeAPI implements just the channel/team lookups configsForCurrentChannel
// and resolveReceiverName need. Everything else panics (embedded interface),
// which is fine for the cross-channel scoping tests below — they return before
// any save/webhook path runs.
type fakeScopeAPI struct {
	plugin.API
	channels map[string]*model.Channel
	teams    map[string]*model.Team
}

func (f *fakeScopeAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	if c, ok := f.channels[id]; ok {
		return c, nil
	}
	return nil, &model.AppError{StatusCode: 404}
}

func (f *fakeScopeAPI) GetTeam(id string) (*model.Team, *model.AppError) {
	if t, ok := f.teams[id]; ok {
		return t, nil
	}
	return nil, &model.AppError{StatusCode: 404}
}

// scopeTestPlugin wires a plugin whose only receivers are one bound to the
// caller's channel (ateam/alerts, channel id "chanA") and one bound to a
// different team+channel (bteam/secret). Used to prove remove/rotate can't reach
// the second from the first (CL-02).
func scopeTestPlugin() (*Plugin, *model.CommandArgs) {
	api := &fakeScopeAPI{
		channels: map[string]*model.Channel{
			"chanA": {Id: "chanA", Name: "alerts", TeamId: "teamA"},
		},
		teams: map[string]*model.Team{
			"teamA": {Id: "teamA", Name: "ateam"},
		},
	}
	p := &Plugin{}
	p.API = api
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: "high-cpu-usage--ateam-alerts", Team: "ateam", Channel: "alerts", WebhookID: "h1"},
		{Name: "high-cpu-usage--bteam-secret", Team: "bteam", Channel: "secret", WebhookID: "h2"},
	}, "", 0, "", "", 0))
	return p, &model.CommandArgs{ChannelId: "chanA", UserId: "u1"}
}

// TestRemoveOneRejectsCrossChannelName is the CL-02 regression for remove: a
// caller in ateam/alerts supplying another team's exact full receiver name must
// get "not found", and that receiver must remain in the config. Without the
// scope-membership guard the global exact-match filter would have deleted it.
func TestRemoveOneRejectsCrossChannelName(t *testing.T) {
	p, args := scopeTestPlugin()

	msg, err := p.handleRemoveOne(args, "high-cpu-usage--bteam-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "not found") {
		t.Fatalf("expected not-found for a cross-channel name, got: %q", msg)
	}
	if !receiverNameInScope(p.getConfiguration().AlertConfigs, "high-cpu-usage--bteam-secret") {
		t.Fatalf("cross-channel receiver was removed — scoping failed")
	}
}

// TestRemoveOneShortNameStaysInChannel confirms a short base slug resolves to
// THIS channel's receiver, not the same-slug receiver in another team.
func TestRemoveOneShortNameResolvesInChannelOnly(t *testing.T) {
	p, args := scopeTestPlugin()

	// Both receivers share the base slug "high-cpu-usage". A short-name resolve
	// must pick the in-channel one; the cross-channel one is invisible here.
	scoped := p.configsForCurrentChannel(args)
	resolved := resolveReceiverName(scoped, "high-cpu-usage", args.ChannelId, p)
	if resolved != "high-cpu-usage--ateam-alerts" {
		t.Fatalf("short name resolved to %q, want the in-channel receiver", resolved)
	}
	if receiverNameInScope(scoped, "high-cpu-usage--bteam-secret") {
		t.Fatalf("cross-channel receiver leaked into the scoped set")
	}
}

// TestGroupOverdueByWebhook pins the failure-accounting fix: when a group
// rotation fails, every overdue member sharing that webhook must be recoverable
// (so all are reported, not just the representative). Pairs with
// representativeOverdueNames — one dedups to the rep, this expands back to the group.
func TestGroupOverdueByWebhook(t *testing.T) {
	overdue := []string{"a", "b", "c", "d"}
	byName := map[string]string{"a": "hookX", "b": "hookX", "c": "hookY", "d": "hookX"}

	got := groupOverdueByWebhook(overdue, byName)

	// A representative failing on hookX must surface a, b, AND d (first-seen order).
	if want := []string{"a", "b", "d"}; !reflect.DeepEqual(got["hookX"], want) {
		t.Fatalf("hookX group = %#v, want %#v", got["hookX"], want)
	}
	if want := []string{"c"}; !reflect.DeepEqual(got["hookY"], want) {
		t.Fatalf("hookY group = %#v, want %#v", got["hookY"], want)
	}
	// The representative's own webhook resolves to its full group — the exact
	// lookup handleRotateOverdue does on a failure.
	rep := representativeOverdueNames(overdue, byName)[0] // "a" (hookX)
	if want := []string{"a", "b", "d"}; !reflect.DeepEqual(got[byName[rep]], want) {
		t.Fatalf("failed-rep %q group lookup = %#v, want %#v", rep, got[byName[rep]], want)
	}
}

// TestOldWebhookStatusLine is the B-003 + F-002 regression: a successful delete
// says the old URL is dead; a failed delete must warn it may still be live and
// identify the webhook by DISPLAY NAME to remove by hand — never a false "no
// longer works", and never the raw hook ID (a live bearer token).
func TestOldWebhookStatusLine(t *testing.T) {
	const displayName = "high-cpu--ateam-alerts"
	const hookID = "abcdef0123456789abcdef0123456789"

	ok := oldWebhookStatusLine(true, displayName, hookID)
	if !strings.Contains(ok, "no longer works") || strings.Contains(ok, hookID) {
		t.Fatalf("deleted case should claim dead and not leak the ID: %q", ok)
	}
	warn := oldWebhookStatusLine(false, displayName, hookID)
	if strings.Contains(warn, "no longer works") {
		t.Fatalf("failed-delete case must NOT claim the old URL is dead: %q", warn)
	}
	if strings.Contains(warn, hookID) {
		t.Fatalf("failed-delete case must NOT leak the raw hook ID: %q", warn)
	}
	if !strings.Contains(warn, displayName) || !strings.Contains(warn, "may still be live") {
		t.Fatalf("failed-delete case should warn and name the webhook by display name: %q", warn)
	}
}

// TestRepresentativeOverdueNames is the group-webhook double-rotation guard: only
// one overdue receiver per shared webhook is rotated (the rest ride along when the
// group rotates), while distinct webhooks each get a representative and order is
// preserved.
func TestRepresentativeOverdueNames(t *testing.T) {
	cases := []struct {
		name    string
		overdue []string
		byHook  map[string]string
		want    []string
	}{
		{
			name:    "two share a webhook -> one representative",
			overdue: []string{"a", "b"},
			byHook:  map[string]string{"a": "hookX", "b": "hookX"},
			want:    []string{"a"},
		},
		{
			name:    "mixed groups -> one rep per distinct webhook, first-seen order",
			overdue: []string{"a", "b", "c"},
			byHook:  map[string]string{"a": "hookX", "b": "hookX", "c": "hookY"},
			want:    []string{"a", "c"},
		},
		{
			name:    "all distinct -> all kept",
			overdue: []string{"a", "b"},
			byHook:  map[string]string{"a": "h1", "b": "h2"},
			want:    []string{"a", "b"},
		},
		{
			name:    "unknown webhook is its own group, never dropped",
			overdue: []string{"a", "b"},
			byHook:  map[string]string{"a": "h1"}, // b missing from the map
			want:    []string{"a", "b"},
		},
		{
			name:    "duplicate name collapses to one (same webhook seen twice)",
			overdue: []string{"a", "a"},
			byHook:  map[string]string{"a": "hookX"},
			want:    []string{"a"},
		},
		{
			name:    "empty",
			overdue: nil,
			byHook:  map[string]string{},
			want:    []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := representativeOverdueNames(tc.overdue, tc.byHook)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("representativeOverdueNames = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestOverdueReceiverNamesRespectsOptIn is the F-007 regression: rotate all
// --overdue must only consider receivers opted INTO rotation reminders. An
// opted-out receiver past the threshold must NOT be selected — rotating it would
// invalidate a webhook the operator never enrolled in the rotation workflow.
func TestOverdueReceiverNamesRespectsOptIn(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour) // well past any threshold
	scoped := []alertConfig{
		{Name: "opted-in--t-c", LastRotatedAt: old, RotationRemindersEnabled: true},
		{Name: "opted-out--t-c", LastRotatedAt: old, RotationRemindersEnabled: false},
		{Name: "opted-in-but-fresh--t-c", LastRotatedAt: now, RotationRemindersEnabled: true},
		{Name: "opted-in-zero--t-c", RotationRemindersEnabled: true}, // zero LastRotatedAt = not due
	}
	got := overdueReceiverNames(scoped, now, 24*time.Hour)
	if len(got) != 1 || got[0] != "opted-in--t-c" {
		t.Fatalf("overdue set = %v, want only [opted-in--t-c] (opt-out and fresh excluded)", got)
	}
}

// TestShortNameDoesNotResolveToLegacyOutsideChannel is the F-006 regression:
// resolving a short name against the CHANNEL-SCOPED set must return the
// in-channel receiver, never a legacy unsuffixed receiver of the same base slug
// in another team (which a global resolve would exact-match first).
func TestShortNameDoesNotResolveToLegacyOutsideChannel(t *testing.T) {
	p, args := scopeTestPlugin()

	// Add a LEGACY unsuffixed receiver with the same base slug in another
	// team/channel (invisible to the current channel's scope).
	cfg := p.getConfiguration()
	entries := append([]alertConfig{}, cfg.AlertConfigs...)
	entries = append(entries, alertConfig{Name: "high-cpu-usage", Team: "bteam", Channel: "legacy", WebhookID: "hookL"})
	p.setConfiguration(newConfiguration(entries, cfg.WebhookHost, cfg.AssembledYAMLTTLHours, cfg.AlertManagerCABundle, cfg.MetricsToken, cfg.WebhookRotationDays))

	scoped := p.configsForCurrentChannel(args)
	if resolved := resolveReceiverName(scoped, "high-cpu-usage", args.ChannelId, p); resolved != "high-cpu-usage--ateam-alerts" {
		t.Fatalf("scoped resolve = %q, want the in-channel receiver (legacy in another team must not win)", resolved)
	}
}
