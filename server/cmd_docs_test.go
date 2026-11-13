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
