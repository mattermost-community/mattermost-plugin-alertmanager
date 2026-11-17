package main

import "testing"

func TestNormalizeAcknowledgedName(t *testing.T) {
	got, err := normalizeAcknowledgedName("monitoring/mattermost-alertmanager-abc/kube-bridge")
	if err != nil {
		t.Fatal(err)
	}
	if got != "kube-bridge" {
		t.Fatalf("canonical = %q", got)
	}
	if _, err := normalizeAcknowledgedName("null"); err == nil {
		t.Fatal("null is already ignored")
	}
	if _, err := normalizeAcknowledgedName("has space"); err == nil {
		t.Fatal("expected a spaced name to be rejected")
	}
}

func TestParseAcknowledgedReceiversEmpty(t *testing.T) {
	got, err := parseAcknowledgedReceivers(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %#v err=%v", got, err)
	}
	got, err = parseAcknowledgedReceivers([]byte(`["kube-bridge"]`))
	if err != nil || len(got) != 1 || got[0] != "kube-bridge" {
		t.Fatalf("got %#v err=%v", got, err)
	}
}
