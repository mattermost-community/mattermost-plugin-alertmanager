package main

import "testing"

func TestResolvedKindReadsEntriesSavedBeforeKindExisted(t *testing.T) {
	cases := []struct {
		name string
		ac   alertConfig
		want receiverKind
	}{
		{"plain legacy entry", alertConfig{Name: "high-cpu-usage--t-c"}, kindRunbook},
		{"legacy custom", alertConfig{Name: "slo--t-c", Custom: true}, kindCustom},
		{"legacy pack set both fields", alertConfig{Name: "mattermost-server--t-c", Custom: true, Pack: packReceiverSlug}, kindPack},
		{"explicit custom", alertConfig{Name: "slo--t-c", Kind: kindCustom}, kindCustom},
		{"explicit pack", alertConfig{Name: "mattermost-server--t-c", Kind: kindPack, Pack: packReceiverSlug}, kindPack},
		{"explicit kind wins over the legacy flag", alertConfig{Name: "x--t-c", Kind: kindRunbook, Custom: true}, kindRunbook},
	}
	for _, c := range cases {
		if got := c.ac.resolvedKind(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsValidChecksReceiverKind(t *testing.T) {
	base := alertConfig{Name: "slo--t-c", Team: "t", Channel: "c", WebhookID: "h"}

	bad := base
	bad.Kind = "bogus"
	if bad.IsValid() == nil {
		t.Error("an unknown kind must be rejected")
	}
	noPack := base
	noPack.Kind = kindPack
	if noPack.IsValid() == nil {
		t.Error("a pack receiver without a pack name must be rejected")
	}
	for _, kind := range []receiverKind{"", kindRunbook, kindCustom} {
		ok := base
		ok.Kind = kind
		if err := ok.IsValid(); err != nil {
			t.Errorf("kind %q: %v", kind, err)
		}
	}
}
