package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reconcilerAPI struct {
	plugin.API
	users     []*model.User
	byID      map[string]*model.User
	listCalls int
	getCalls  int
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

func (a *reconcilerAPI) LogWarn(string, ...any) {}

func testAdmin(id string) *model.User {
	return &model.User{Id: id, Roles: model.SystemUserRoleId + " " + model.SystemAdminRoleId}
}

func TestOrphanProbeIsThrottled(t *testing.T) {
	p := &Plugin{}
	now := time.Now()
	assert.True(t, p.orphanProbeDue(now), "first tick checks")

	p.markOrphanProbe(now)
	assert.False(t, p.orphanProbeDue(now.Add(reconcilerInterval)))
	assert.True(t, p.orphanProbeDue(now.Add(reconcilerOrphanProbeInterval)))
}

func TestYAMLSweepIsThrottled(t *testing.T) {
	p := &Plugin{}
	now := time.Now()
	assert.True(t, p.claimYAMLSweep(now))
	assert.False(t, p.claimYAMLSweep(now.Add(time.Minute)), "claiming restarts the interval")
	assert.True(t, p.claimYAMLSweep(now.Add(yamlSweepInterval)))
}

func TestRecordReconcileRunKeepsPrunedCountBetweenProbes(t *testing.T) {
	p := &Plugin{}
	p.recordReconcileRun(3, true)
	p.recordReconcileRun(0, false)

	_, pruned := p.reconcileStatus()
	assert.Equal(t, 3, pruned)
}

func TestActiveSysadminIDReusesTheLastAdmin(t *testing.T) {
	a := &reconcilerAPI{users: []*model.User{testAdmin("a1")}, byID: map[string]*model.User{"a1": testAdmin("a1")}}
	p := &Plugin{}
	p.API = a

	for range 3 {
		id, err := p.activeSysadminID()
		require.NoError(t, err)
		assert.Equal(t, "a1", id)
	}
	assert.Equal(t, 1, a.listCalls, "one user search, then cached lookups")
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
	require.NoError(t, err)
	assert.Equal(t, "a2", id)
}

func TestProbeWebhooksClassifiesEachHookOnce(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		hits[id]++
		mu.Unlock()
		switch id {
		case "alive":
			_, _ = w.Write([]byte(`{"id":"alive"}`))
		case "dead":
			http.Error(w, `{"id":"x","message":"gone","status_code":404}`, http.StatusNotFound)
		default:
			http.Error(w, `{"id":"x","message":"boom","status_code":500}`, http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := model.NewAPIv4Client(srv.URL)
	p := &Plugin{}
	p.API = &reconcilerAPI{}

	status := p.probeWebhooks(c, []alertConfig{
		{Name: "r1", WebhookID: "alive"},
		{Name: "r2", WebhookID: "alive"},
		{Name: "r3", WebhookID: "dead"},
		{Name: "r4", WebhookID: "flaky"},
	}, orphanProbeBudget)

	assert.Equal(t, map[string]bool{"alive": true, "dead": false}, status, "a transient error leaves the hook out")
	assert.Equal(t, 1, hits["alive"], "a shared hook is probed once")
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
		for id := range p.probeWebhooks(c, configs, 100*time.Millisecond) {
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
	p.setProbeCursor("c")
	assert.Equal(t, []string{"c", "d", "a", "b"}, p.rotateToProbeCursor([]string{"a", "b", "c", "d"}))
	p.setProbeCursor("removed")
	assert.Equal(t, []string{"a", "b", "d"}, p.rotateToProbeCursor([]string{"a", "b", "d"}))
}
