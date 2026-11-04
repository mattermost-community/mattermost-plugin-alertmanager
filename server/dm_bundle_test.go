package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

type failingUploadAPI struct {
	*ruleDMAPI
	fail string
}

func (f *failingUploadAPI) UploadFile(data []byte, channelID, filename string) (*model.FileInfo, *model.AppError) {
	if filename == f.fail {
		return nil, model.NewAppError("UploadFile", "test", nil, "", 500)
	}
	return f.ruleDMAPI.UploadFile(data, channelID, filename)
}

func bundleMessage(t *testing.T, api *ruleDMAPI, routesYAML, amURL string, withRules bool, fail string) string {
	t.Helper()
	p := &Plugin{}
	p.API = &failingUploadAPI{ruleDMAPI: api, fail: fail}
	var rules *rulesFile
	if withRules {
		rules, _ = p.rulesFileForChannel(testRulesContext(formatStandard), scopedCompute())
		if rules == nil {
			t.Fatal("test setup: no rules file for the compute receivers")
		}
	}
	if err := p.dmYAMLBundle("u1", "receivers", routesYAML, 1, amURL, rules); err != nil {
		t.Fatal(err)
	}
	if len(api.posts) != 1 {
		t.Fatalf("want one DM post, got %d", len(api.posts))
	}
	return api.posts[0].Message
}

const pasteRoutes = "Paste `alertmanager-routes.yml`"

func TestDMBundleAsksForRoutesOnlyWhenTheyAreAttached(t *testing.T) {
	t.Run("routes and rules attached", func(t *testing.T) {
		msg := bundleMessage(t, &ruleDMAPI{uploads: map[string]string{}}, "routes", "http://am:9093", true, "")
		if !strings.Contains(msg, pasteRoutes) {
			t.Fatalf("routes are attached but the message does not say where to paste them:\n%s", msg)
		}
	})
	t.Run("no routes, rules attached", func(t *testing.T) {
		msg := bundleMessage(t, &ruleDMAPI{uploads: map[string]string{}}, "", "http://am:9093", true, "")
		if strings.Contains(msg, pasteRoutes) || strings.Contains(msg, "could not be attached") {
			t.Fatalf("no routes file exists, yet the message mentions one:\n%s", msg)
		}
	})
	t.Run("routes upload failed, rules attached", func(t *testing.T) {
		api := &ruleDMAPI{uploads: map[string]string{}}
		msg := bundleMessage(t, api, "routes", "http://am:9093", true, "alertmanager-routes.yml")
		if strings.Contains(msg, pasteRoutes) {
			t.Fatalf("the routes upload failed, yet the message asks the user to paste it:\n%s", msg)
		}
		if !strings.Contains(msg, "could not be attached") {
			t.Fatalf("the message should say the routes file is missing:\n%s", msg)
		}
		if got := len(api.posts[0].FileIds); got != 2 {
			t.Fatalf("want receivers + rules attached, got %v", api.posts[0].FileIds)
		}
	})
}

func TestDMBundleReloadCommandIsNeverBroken(t *testing.T) {
	msg := bundleMessage(t, &ruleDMAPI{uploads: map[string]string{}}, "routes", "", false, "")
	if strings.Contains(msg, "curl -X POST /-/reload") {
		t.Fatalf("an empty Alertmanager URL produced a reload command that cannot work:\n%s", msg)
	}
	if !strings.Contains(msg, "curl -X POST <am-url>/-/reload") {
		t.Fatalf("want a placeholder URL in the reload command:\n%s", msg)
	}
}

func TestDMBundleDoesNotClaimTheReceiversAreNew(t *testing.T) {
	msg := bundleMessage(t, &ruleDMAPI{uploads: map[string]string{}}, "routes", "", false, "")
	if strings.Contains(msg, "new receiver") || strings.Contains(msg, "config add") {
		t.Fatalf("export, rotate and remove also send this DM, so it must not say the receivers were just added:\n%s", msg)
	}
}
