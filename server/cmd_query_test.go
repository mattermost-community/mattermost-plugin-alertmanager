package main

import (
	"reflect"
	"testing"
)

// TestGroupByAMURL pins the dedup logic for the alerts/silences/status
// commands. Without this collapse step, a channel hosting 20 receivers
// against one Alertmanager renders 20 copies of the same output — the
// commands are unusable in practice. Each test case captures a
// different shape of receiver-to-AM binding the function has to handle.
func TestGroupByAMURL(t *testing.T) {
	t.Run("empty input returns empty output", func(t *testing.T) {
		got := groupByAMURL(nil)
		if len(got) != 0 {
			t.Fatalf("expected empty slice, got %v", got)
		}
	})

	t.Run("single AM with N receivers collapses to one group", func(t *testing.T) {
		input := []alertConfig{
			{Name: "high-cpu", AlertManagerURL: "http://am1"},
			{Name: "high-mem", AlertManagerURL: "http://am1"},
			{Name: "pod-not-ready", AlertManagerURL: "http://am1"},
		}
		got := groupByAMURL(input)
		if len(got) != 1 {
			t.Fatalf("expected 1 group, got %d", len(got))
		}
		if got[0].URL != "http://am1" {
			t.Fatalf("wrong URL: %s", got[0].URL)
		}
		if len(got[0].Receivers) != 3 {
			t.Fatalf("expected 3 receivers in group, got %d", len(got[0].Receivers))
		}
	})

	t.Run("N AMs with one receiver each → N groups", func(t *testing.T) {
		input := []alertConfig{
			{Name: "a", AlertManagerURL: "http://am1"},
			{Name: "b", AlertManagerURL: "http://am2"},
			{Name: "c", AlertManagerURL: "http://am3"},
		}
		got := groupByAMURL(input)
		if len(got) != 3 {
			t.Fatalf("expected 3 groups, got %d", len(got))
		}
	})

	t.Run("mixed: two AMs sharing N+M receivers respectively", func(t *testing.T) {
		input := []alertConfig{
			{Name: "a", AlertManagerURL: "http://am1"},
			{Name: "b", AlertManagerURL: "http://am2"},
			{Name: "c", AlertManagerURL: "http://am1"},
			{Name: "d", AlertManagerURL: "http://am2"},
			{Name: "e", AlertManagerURL: "http://am1"},
		}
		got := groupByAMURL(input)
		if len(got) != 2 {
			t.Fatalf("expected 2 groups, got %d", len(got))
		}
		// First group is am1 (first-appearance order). Two groups
		// with receiver counts 3 and 2.
		gotCounts := []int{len(got[0].Receivers), len(got[1].Receivers)}
		wantCounts := []int{3, 2}
		if !reflect.DeepEqual(gotCounts, wantCounts) {
			t.Fatalf("expected receiver counts %v, got %v", wantCounts, gotCounts)
		}
		if got[0].URL != "http://am1" || got[1].URL != "http://am2" {
			t.Fatalf("groups not in first-appearance order: %q then %q", got[0].URL, got[1].URL)
		}
	})

	t.Run("first receiver's credentials propagate to the group", func(t *testing.T) {
		input := []alertConfig{
			{Name: "a", AlertManagerURL: "http://am1", User: "alice", Password: "p1"},
			{Name: "b", AlertManagerURL: "http://am1", User: "bob", Password: "p2"},
		}
		got := groupByAMURL(input)
		if len(got) != 1 {
			t.Fatalf("expected 1 group, got %d", len(got))
		}
		if got[0].User != "alice" || got[0].Password != "p1" {
			t.Fatalf("expected first-wins credentials, got user=%s pass=%s", got[0].User, got[0].Password)
		}
	})

	t.Run("output order is stable across runs", func(t *testing.T) {
		input := []alertConfig{
			{Name: "a", AlertManagerURL: "http://am1"},
			{Name: "b", AlertManagerURL: "http://am2"},
		}
		first := groupByAMURL(input)
		second := groupByAMURL(input)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("expected stable output, got %v vs %v", first, second)
		}
	})
}

// TestReceiverNames pins the inline-code formatting used in section
// headers so the rendered output stays consistent across slash-command
// runs. Cosmetic test, but the output is what users actually read.
func TestReceiverNames(t *testing.T) {
	g := amGroup{
		Receivers: []alertConfig{
			{Name: "high-cpu-usage--alerts"},
			{Name: "high-memory-usage--alerts"},
		},
	}
	got := g.receiverNames()
	want := "`high-cpu-usage--alerts`, `high-memory-usage--alerts`"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
