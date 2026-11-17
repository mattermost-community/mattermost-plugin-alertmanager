package main

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type reconcilerAPI struct {
	plugin.API
	users      []*model.User
	byID       map[string]*model.User
	listCalls  int
	getCalls   int
	listenAddr string

	mu    sync.Mutex
	warns []string
}

func (a *reconcilerAPI) GetUsers(*model.UserGetOptions) ([]*model.User, *model.AppError) {
	a.listCalls++
	return a.users, nil
}

func (a *reconcilerAPI) GetUser(id string) (*model.User, *model.AppError) {
	a.getCalls++
	if u, ok := a.byID[id]; ok {
		return u, nil
	}
	return nil, model.NewAppError("GetUser", "not_found", nil, "", http.StatusNotFound)
}

func (a *reconcilerAPI) LogWarn(msg string, _ ...any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.warns = append(a.warns, msg)
}

func (a *reconcilerAPI) warnCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.warns)
}

func (a *reconcilerAPI) GetConfig() *model.Config {
	cfg := &model.Config{}
	cfg.ServiceSettings.ListenAddress = model.NewPointer(a.listenAddr)
	return cfg
}

func (a *reconcilerAPI) CreateUserAccessToken(t *model.UserAccessToken) (*model.UserAccessToken, *model.AppError) {
	return &model.UserAccessToken{Id: "tok", Token: "secret", UserId: t.UserId}, nil
}

func (a *reconcilerAPI) RevokeUserAccessToken(string) *model.AppError { return nil }

func testAdmin(id string) *model.User {
	return &model.User{Id: id, Roles: model.SystemUserRoleId + " " + model.SystemAdminRoleId}
}

// webhookServer answers GetIncomingWebhook: "alive" exists, "dead" is a 404 and
// anything else is a 500.
func webhookServer(t *testing.T, hits map[string]int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if hits != nil {
			mu.Lock()
			hits[id]++
			mu.Unlock()
		}
		switch id {
		case "alive":
			_, _ = w.Write([]byte(`{"id":"alive"}`))
		case "dead":
			http.Error(w, `{"id":"x","message":"gone","status_code":404}`, http.StatusNotFound)
		default:
			http.Error(w, `{"id":"x","message":"boom","status_code":500}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOrphanProbeIsThrottled(t *testing.T) {
	p := &Plugin{}
	now := time.Now()
	if !p.orphanProbeDue(now) {
		t.Fatal("the first tick should check")
	}

	p.markOrphanProbe(now)
	if p.orphanProbeDue(now.Add(reconcilerInterval)) {
		t.Error("the next tick should not check again")
	}
	if !p.orphanProbeDue(now.Add(reconcilerOrphanProbeInterval)) {
		t.Error("a check is due once the probe interval has passed")
	}
}

func TestYAMLSweepIsThrottled(t *testing.T) {
	p := &Plugin{}
	now := time.Now()
	if !p.claimYAMLSweep(now) {
		t.Fatal("the first sweep should be claimed")
	}
	if p.claimYAMLSweep(now.Add(time.Minute)) {
		t.Error("claiming restarts the interval")
	}
	if !p.claimYAMLSweep(now.Add(yamlSweepInterval)) {
		t.Error("a sweep is due once the interval has passed")
	}
}

func TestRecordReconcileRunKeepsPrunedCountBetweenProbes(t *testing.T) {
	p := &Plugin{}
	p.recordReconcileRun(3, true)
	p.recordReconcileRun(0, false)

	if got := p.reconcileStatus().pruned; got != 3 {
		t.Fatalf("pruned = %d, want 3", got)
	}
}

func TestReconcilerBannerDatesThePrunedCountByItsOrphanCheck(t *testing.T) {
	p := &Plugin{}
	p.markOrphanProbe(time.Now().Add(-25 * time.Minute))
	p.recordReconcileRun(3, true)
	p.recordReconcileRun(0, false)

	status, healthy := p.reconcilerBanner()
	if !healthy {
		t.Errorf("a tick that just ran is healthy: %q", status)
	}
	if !strings.Contains(status, "last orphan check 25m ago, pruned 3") {
		t.Errorf("the pruned count should carry the orphan check's own time: %q", status)
	}
}

func TestActiveSysadminIDReusesTheLastAdmin(t *testing.T) {
	a := &reconcilerAPI{users: []*model.User{testAdmin("a1")}, byID: map[string]*model.User{"a1": testAdmin("a1")}}
	p := &Plugin{}
	p.API = a

	for range 3 {
		id, err := p.activeSysadminID()
		if err != nil {
			t.Fatal(err)
		}
		if id != "a1" {
			t.Fatalf("id = %q, want a1", id)
		}
	}
	if a.listCalls != 1 {
		t.Errorf("want one user search, then cached lookups; got %d searches", a.listCalls)
	}
}

func TestActiveSysadminIDFindsANewAdminWhenTheCachedOneIsGone(t *testing.T) {
	gone := testAdmin("a1")
	gone.DeleteAt = 1
	a := &reconcilerAPI{
		users: []*model.User{testAdmin("a2")},
		byID:  map[string]*model.User{"a1": gone, "a2": testAdmin("a2")},
	}
	p := &Plugin{cachedSysadminID: "a1"}
	p.API = a

	id, err := p.activeSysadminID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "a2" {
		t.Fatalf("id = %q, want a2", id)
	}
}

func TestProbeWebhooksClassifiesEachHookOnce(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := webhookServer(t, hits, &mu)

	c := model.NewAPIv4Client(srv.URL)
	api := &reconcilerAPI{}
	p := &Plugin{}
	p.API = api

	status, cutOff, failed := p.probeWebhooks(c, []alertConfig{
		{Name: "r1", WebhookID: "alive"},
		{Name: "r2", WebhookID: "alive"},
		{Name: "r3", WebhookID: "dead"},
		{Name: "r4", WebhookID: "flaky"},
	}, orphanProbeBudget, backgroundProbe)

	if want := map[string]bool{"alive": true, "dead": false}; !maps.Equal(status, want) {
		t.Errorf("status = %v, want %v (a transient error leaves the hook out)", status, want)
	}
	if cutOff != 0 || failed != 1 {
		t.Errorf("cutOff = %d, failed = %d; want 0 and 1 for the hook that errored", cutOff, failed)
	}
	if hits["alive"] != 1 {
		t.Errorf("a shared hook is probed once, got %d calls", hits["alive"])
	}
	if api.warnCount() != 1 {
		t.Errorf("the errored hook should log one warning, got %v", api.warns)
	}
}

func TestProbeWebhooksResumesWhereTheBudgetRanOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
	}))
	defer srv.Close()

	c := model.NewAPIv4Client(srv.URL)
	p := &Plugin{}
	p.API = &reconcilerAPI{}
	var configs []alertConfig
	for i := range 40 {
		configs = append(configs, alertConfig{Name: fmt.Sprintf("r%d", i), WebhookID: fmt.Sprintf("hook%02d", i)})
	}

	checked := map[string]bool{}
	for range 40 {
		status, _, _ := p.probeWebhooks(c, configs, 100*time.Millisecond, backgroundProbe)
		for id := range status {
			checked[id] = true
		}
		if len(checked) == len(configs) {
			return
		}
	}
	t.Fatalf("after 40 checks that each ran out of budget, %d of %d hooks were never checked", len(configs)-len(checked), len(configs))
}

func TestProbeCursorStartsOverWhenItsHookIsGone(t *testing.T) {
	p := &Plugin{}
	p.setProbeCursor(backgroundProbe, "c")
	if got, want := p.rotateToProbeCursor(backgroundProbe, []string{"a", "b", "c", "d"}), []string{"c", "d", "a", "b"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	p.setProbeCursor(backgroundProbe, "removed")
	if got, want := p.rotateToProbeCursor(backgroundProbe, []string{"a", "b", "d"}), []string{"a", "b", "d"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// deadlineServer answers each lookup after 60ms, or drops it when the caller
// gives up first.
func deadlineServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(60 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// eightHooks is twice the worker count, so with deadlineServer and a 100ms
// budget the second four are still in flight when the budget ends.
func eightHooks() []alertConfig {
	var configs []alertConfig
	for i := range 8 {
		configs = append(configs, alertConfig{Name: fmt.Sprintf("r%d", i), WebhookID: fmt.Sprintf("hook%d", i)})
	}
	return configs
}

func TestProbeWebhooksRetriesChecksTheDeadlineCutOff(t *testing.T) {
	c := model.NewAPIv4Client(deadlineServer(t).URL)
	p := &Plugin{}
	p.API = &reconcilerAPI{}
	configs := eightHooks()

	checked := map[string]bool{}
	for range 20 {
		status, _, _ := p.probeWebhooks(c, configs, 100*time.Millisecond, backgroundProbe)
		for id := range status {
			checked[id] = true
		}
		if len(checked) == len(configs) {
			return
		}
	}
	t.Fatalf("%d of %d hooks were never checked: %v", len(configs)-len(checked), len(configs), checked)
}

func TestProbeWebhooksDoesNotWarnAboutChecksTheBudgetCutOff(t *testing.T) {
	c := model.NewAPIv4Client(deadlineServer(t).URL)
	api := &reconcilerAPI{}
	p := &Plugin{}
	p.API = api

	_, cutOff, failed := p.probeWebhooks(c, eightHooks(), 100*time.Millisecond, backgroundProbe)
	if failed != 0 {
		t.Errorf("checks cut off by the budget are not lookup errors, got %d failed", failed)
	}
	if cutOff == 0 {
		t.Fatal("the budget should have cut some checks off")
	}
	if n := api.warnCount(); n != 0 {
		t.Errorf("a check cut off by the budget is not a webhook error, got %d warnings: %v", n, api.warns)
	}
}

func TestManualProbeResumesWithoutMovingTheBackgroundCursor(t *testing.T) {
	c := model.NewAPIv4Client(deadlineServer(t).URL)
	p := &Plugin{}
	p.API = &reconcilerAPI{}
	p.setProbeCursor(backgroundProbe, "hook6")
	configs := eightHooks()

	checked := map[string]bool{}
	for range 10 {
		status, _, _ := p.probeWebhooks(c, configs, 100*time.Millisecond, repairPreviewProbe)
		for id := range status {
			checked[id] = true
		}
		if got := p.orphanProbeNext; got != "hook6" {
			t.Fatalf("a manual check moved the background cursor to %q", got)
		}
		if len(checked) == len(configs) {
			break
		}
	}
	if len(checked) != len(configs) {
		t.Errorf("rerunning admin repair should reach every webhook, %d of %d never checked", len(configs)-len(checked), len(configs))
	}
}

func TestRepairPruneChecksWhatTheCutShortPreviewReported(t *testing.T) {
	c := model.NewAPIv4Client(deadlineServer(t).URL)
	p := &Plugin{}
	p.API = &reconcilerAPI{}
	configs := eightHooks()

	preview, cutOff, _ := p.probeWebhooks(c, configs, 100*time.Millisecond, repairPreviewProbe)
	if cutOff == 0 {
		t.Fatal("the budget should have cut the preview short")
	}
	prune, _, _ := p.probeWebhooks(c, configs, 100*time.Millisecond, repairPruneProbe)
	for id := range preview {
		if _, ok := prune[id]; !ok {
			t.Errorf("the preview checked %s, but the prune it suggests skipped it (preview %v, prune %v)", id, slices.Sorted(maps.Keys(preview)), slices.Sorted(maps.Keys(prune)))
		}
	}
}

func TestUncheckedWebhooksNoteSaysWhyEachWasNotChecked(t *testing.T) {
	note := uncheckedWebhooksNote(orphanCheck{cutOff: 3, failed: 1}, "admin repair")
	for _, want := range []string{"3 webhook(s) were not reached before the time limit", "1 webhook(s) could not be checked because Mattermost returned an error", "`/alertmanager admin repair` again"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if note := uncheckedWebhooksNote(orphanCheck{failed: 1}, "admin repair"); strings.Contains(note, "time limit") {
		t.Errorf("an error-only check should not blame the time limit:\n%s", note)
	}
}

func TestRepairPreviewSaysWhenSomeWebhooksWereNotChecked(t *testing.T) {
	srv := webhookServer(t, nil, nil)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plugin{}
	p.API = &reconcilerAPI{listenAddr: ":" + u.Port()}
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: "ok", WebhookID: "alive"},
		{Name: "unknown", WebhookID: "flaky"},
	}, "", 0, "", "", 0))

	out := p.orphanSection("admin", false)
	if strings.Contains(out, "Every registered webhook still exists") {
		t.Errorf("a webhook that was not checked must not be reported as existing:\n%s", out)
	}
	if !strings.Contains(out, "1 webhook(s) could not be checked") {
		t.Errorf("the reply should say how many webhooks were not checked:\n%s", out)
	}
}

func TestFirstUnfinishedPassesOverAHookThatHangsEveryTime(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	for _, tc := range []struct {
		name     string
		finished map[string]bool
		want     string
	}{
		{"all finished", map[string]bool{"a": true, "b": true, "c": true, "d": true}, ""},
		{"stopped at c", map[string]bool{"a": true, "b": true}, "c"},
		{"a hung start hook does not pin the cursor", map[string]bool{"b": true}, "c"},
		{"the start hook is retried once it is the only one left", map[string]bool{"b": true, "c": true, "d": true}, "a"},
	} {
		if got := firstUnfinished(ids, tc.finished); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
