package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type siteURLAPI struct{ plugin.API }

func (siteURLAPI) GetConfig() *model.Config {
	url := "https://mm.example.com/"
	cfg := &model.Config{}
	cfg.ServiceSettings.SiteURL = &url
	return cfg
}

type fixedSiteURLAPI struct {
	plugin.API
	url   string
	warns *int
}

func (a fixedSiteURLAPI) GetConfig() *model.Config {
	cfg := &model.Config{}
	cfg.ServiceSettings.SiteURL = &a.url
	return cfg
}

func (a fixedSiteURLAPI) LogWarn(string, ...any) {
	if a.warns != nil {
		*a.warns++
	}
}

func TestSiteURLRejectsUnsafeCharacters(t *testing.T) {
	const placeholder = siteURLPlaceholder
	cases := []struct{ in, want string }{
		{"https://mm.example.com/", "https://mm.example.com"},
		{"http://mm.internal:8065/sub/path", "http://mm.internal:8065/sub/path"},
		{"https://[::1]:8065", "https://[::1]:8065"},
		{"https://[2001:db8::1]/chat", "https://[2001:db8::1]/chat"},
		{"https://mm.example.com/a%20b", "https://mm.example.com/a%20b"},
		{"", placeholder},
		{"https://mm.example.com/a\u2028x", placeholder},
		{"https://mm.example.com/a\u0085x", placeholder},
		{"https://mm.example.com/a\u00a0x", placeholder},
		{"https://mm.example.com/a\x0cx", placeholder},
		{"mm.example.com", placeholder},
		{"https://mm.example.com/x)[evil](https://evil", placeholder},
		{"https://mm.example.com/{{ .Labels.secret }}", placeholder},
		{"https://mm.example.com/'x", placeholder},
		{"https://mm.example.com/\"x", placeholder},
		{"https://mm.example.com/`x`", placeholder},
		{"https://mm.example.com/\\x", placeholder},
		{"https://mm.example.com/<x>", placeholder},
		{"https://mm.example.com/a b", placeholder},
		{"https://mm.example.com/\nx", placeholder},
	}
	for _, tc := range cases {
		p := &Plugin{}
		p.API = fixedSiteURLAPI{url: tc.in}
		if got := p.siteURL(); got != tc.want {
			t.Errorf("siteURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnsafeSiteURLWarnsOnce(t *testing.T) {
	warns := 0
	p := &Plugin{}
	p.API = fixedSiteURLAPI{url: "https://mm.example.com/{x}", warns: &warns}
	for range 3 {
		p.siteURL()
	}
	if warns != 1 {
		t.Errorf("unsafe SiteURL logged %d warnings, want 1", warns)
	}

	p = &Plugin{}
	p.API = fixedSiteURLAPI{url: "", warns: &warns}
	warns = 0
	p.siteURL()
	if warns != 0 {
		t.Errorf("unset SiteURL should not warn, got %d", warns)
	}
}

func TestHostileSiteURLCannotBreakRunbookLink(t *testing.T) {
	p := &Plugin{}
	p.API = fixedSiteURLAPI{url: "https://mm.example.com/x){{ .Labels.secret }}"}
	out := renderReceiverYAML("high-cpu-usage--t-c", "https://mm.example/hooks/x", "alerts", p.runbookDefaultURL("high-cpu-usage"), p.siteURL()+webhookIconURL)
	if strings.Contains(out, ".Labels.secret") || !strings.Contains(out, "(https://CHANGE-ME-mattermost-site-url/plugins/") {
		t.Errorf("hostile SiteURL reached the receiver template:\n%s", out)
	}
}

func newDocsTestPlugin() *Plugin {
	p := &Plugin{}
	p.API = siteURLAPI{}
	return p
}

func TestHandleDocsLinksToSiteInsteadOfPrintingDocs(t *testing.T) {
	p := newDocsTestPlugin()

	for _, cmd := range []string{"/alertmanager docs", "/alertmanager docs kubernetes", "/alertmanager docs nosuchtopic"} {
		got, err := p.handleDocs(&model.CommandArgs{Command: cmd})
		if err != nil {
			t.Fatalf("%q: %v", cmd, err)
		}
		for _, want := range []string{
			"https://mm.example.com/plugins/" + Manifest.Id + "/public/help/home.html",
			"/public/runbooks/home.html",
			"/public/help/prometheus-rules.html",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%q: reply missing %s: %s", cmd, want, got)
			}
		}
		if len(got) > 1500 {
			t.Errorf("%q: reply is %d bytes, docs must not be printed in chat", cmd, len(got))
		}
	}
}

func TestHandleDocsDeprecatedTopicLinksToThatPage(t *testing.T) {
	p := newDocsTestPlugin()
	got, err := p.handleDocs(&model.CommandArgs{Command: "/alertmanager docs kubernetes"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "public/help/kubernetes.html") || !strings.Contains(got, "deprecated") {
		t.Errorf("topic reply should link the page and say it is deprecated: %s", got)
	}

	bare, err := p.handleDocs(&model.CommandArgs{Command: "/alertmanager docs"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bare, "deprecated") {
		t.Errorf("bare docs should not mention deprecation: %s", bare)
	}
}
