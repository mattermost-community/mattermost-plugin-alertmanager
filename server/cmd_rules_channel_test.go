package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type ruleDMAPI struct {
	plugin.API
	uploads map[string]string
	posts   []*model.Post
}

func (f *ruleDMAPI) LoadPluginConfiguration(any) error { return nil }

func (f *ruleDMAPI) GetDirectChannel(string, string) (*model.Channel, *model.AppError) {
	return &model.Channel{Id: "dm"}, nil
}

func (f *ruleDMAPI) UploadFile(data []byte, _, filename string) (*model.FileInfo, *model.AppError) {
	f.uploads[filename] = string(data)
	return &model.FileInfo{Id: filename}, nil
}

func (f *ruleDMAPI) CreatePost(post *model.Post) (*model.Post, *model.AppError) {
	f.posts = append(f.posts, post)
	return &model.Post{Id: "p1"}, nil
}

func TestDNSLabel(t *testing.T) {
	cases := map[string]string{
		"ops":          "ops",
		"Ops_Team":     "ops-team",
		"alerts.prod":  "alerts-prod",
		"-weird_name-": "weird-name",
	}
	for in, want := range cases {
		if got := dnsLabel(in); got != want {
			t.Errorf("dnsLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRulesFileForChannelCRD(t *testing.T) {
	p := &Plugin{}
	p.API = &ruleDMAPI{uploads: map[string]string{}}
	slug := scaffoldSets["compute"][0]
	rules, reason := p.rulesFileForChannel("monitoring", []alertConfig{
		{Name: slug + "--ops-alerts", Team: "ops", Channel: "alerts"},
		{Name: "slo-burn--ops-alerts", Team: "ops", Channel: "alerts", Custom: true},
	}, formatCRD)
	if rules == nil || reason != "" {
		t.Fatalf("rules=%v reason=%q", rules, reason)
	}
	if rules.name != "mattermost-alertmanager-rules-ops-alerts.yaml" {
		t.Fatalf("name = %q", rules.name)
	}
	body := string(rules.data)
	if !strings.Contains(body, "kind: PrometheusRule") || !strings.Contains(body, "runbook: "+slug) {
		t.Fatalf("rule file should cover %s:\n%s", slug, body)
	}
	if strings.Contains(body, "slo-burn") {
		t.Fatal("custom receivers have no sample rules")
	}
	if !strings.Contains(rules.section(), "kubectl apply -n monitoring") {
		t.Fatalf("section should say how to apply: %s", rules.section())
	}
}

func TestRulesFileForChannelStandardIsPlain(t *testing.T) {
	p := &Plugin{}
	p.API = &ruleDMAPI{uploads: map[string]string{}}
	rules, _ := p.rulesFileForChannel("monitoring", []alertConfig{
		{Name: scaffoldSets["compute"][0] + "--ops-alerts", Team: "ops", Channel: "alerts"},
	}, formatStandard)
	body := string(rules.data)
	if !strings.HasPrefix(body, "groups:") || strings.Contains(body, "kind: PrometheusRule") {
		t.Fatalf("standard format should be a bare rules file:\n%s", body)
	}
	if !strings.Contains(rules.section(), "rule_files") || strings.Contains(rules.section(), "kubectl") {
		t.Fatalf("section should point at rule_files: %s", rules.section())
	}
}

func TestRulesFileForChannelSkipsCustomOnly(t *testing.T) {
	p := &Plugin{}
	p.API = &ruleDMAPI{uploads: map[string]string{}}
	rules, reason := p.rulesFileForChannel("monitoring", []alertConfig{
		{Name: "slo-burn--ops-alerts", Team: "ops", Channel: "alerts", Custom: true},
	}, formatCRD)
	if rules != nil || !strings.Contains(reason, "No rules file") {
		t.Fatalf("rules=%v reason=%q", rules, reason)
	}
}

func (f *ruleDMAPI) KVSet(string, []byte) *model.AppError { return nil }
func (f *ruleDMAPI) LogWarn(string, ...any)               {}
func (f *ruleDMAPI) GetConfig() *model.Config {
	url := "http://mm:8065"
	return &model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &url}}
}

func scopedCompute() []alertConfig {
	return []alertConfig{{Name: scaffoldSets["compute"][0] + "--ops-alerts", Team: "ops", Channel: "alerts", AlertManagerURL: "http://am:9093", WebhookID: "hook1"}}
}

// TestFormatExportCRDIsOneMessageWithConfigAndRules covers the whole command
// path: one DM post carries both files.
func TestFormatExportCRDIsOneMessageWithConfigAndRules(t *testing.T) {
	api := &ruleDMAPI{uploads: map[string]string{}}
	p := &Plugin{}
	p.API = api

	msg, err := p.handleExportCRD(&model.CommandArgs{UserId: "u1"}, scopedCompute(), "monitoring")
	if err != nil {
		t.Fatal(err)
	}
	if len(api.posts) != 1 {
		t.Fatalf("want one DM post, got %d", len(api.posts))
	}
	files := api.posts[0].FileIds
	if len(files) != 2 || files[0] != "alertmanager-config.yaml" || files[1] != "mattermost-alertmanager-rules-ops-alerts.yaml" {
		t.Fatalf("files = %v", files)
	}
	if !strings.Contains(api.posts[0].Message, "Prometheus rules") {
		t.Fatalf("post should describe the rules file: %s", api.posts[0].Message)
	}
	if !strings.Contains(msg, "attached to the same DM") {
		t.Fatalf("reply should point at the same DM: %s", msg)
	}
}

func TestFormatExportStandardIsOneMessageWithConfigRoutesAndRules(t *testing.T) {
	api := &ruleDMAPI{uploads: map[string]string{}}
	p := &Plugin{}
	p.API = api
	scoped := scopedCompute()
	rules, _ := p.rulesFileForChannel("monitoring", scoped, formatStandard)
	if err := p.dmYAMLBundle("u1", "receivers", "routes", 1, "", rules); err != nil {
		t.Fatal(err)
	}
	if len(api.posts) != 1 {
		t.Fatalf("want one DM post, got %d", len(api.posts))
	}
	if got := len(api.posts[0].FileIds); got != 3 {
		t.Fatalf("want receivers + routes + rules, got %v", api.posts[0].FileIds)
	}
}
