package alertmanager

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func allowTestLoopback(t *testing.T) {
	t.Helper()
	SetAllowedNets([]*net.IPNet{mustCIDR("127.0.0.0/8"), mustCIDR("::1/128")})
	t.Cleanup(func() { SetAllowedNets(nil) })
}

func TestCreateSilencePostsMatchersAndReturnsUUID(t *testing.T) {
	allowTestLoopback(t)
	var body string
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/v2/silences" || r.Method != http.MethodPost {
			t.Errorf("path %s %s", r.Method, r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "pass" {
			t.Errorf("basic auth = %q %q ok=%v", user, pass, ok)
		}
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"silenceID":"` + testSilenceUUID + `"}`))
	}))
	defer srv.Close()

	id, err := CreateSilence(srv.URL, "user", "pass", "ada", "from mattermost", time.Now().Add(time.Hour), []SilenceMatcher{
		{Name: "alertname", Value: "HighCPUUsage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != testSilenceUUID {
		t.Fatalf("id %q", id)
	}
	if hits != 1 {
		t.Fatalf("hits %d", hits)
	}
	if !strings.Contains(body, `"name":"alertname"`) || !strings.Contains(body, `"value":"HighCPUUsage"`) {
		t.Fatalf("body %s", body)
	}
}

func TestCreateSilenceDoesNotRetryRejectedRequest(t *testing.T) {
	allowTestLoopback(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := CreateSilence(srv.URL, "", "", "ada", "from mattermost", time.Now().Add(time.Minute), []SilenceMatcher{
		{Name: "alertname", Value: "HighCPUUsage"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if hits != 1 {
		t.Fatalf("rejected silence was retried %d times", hits)
	}
}

func TestCreateSilenceRejectsNonUUIDResponse(t *testing.T) {
	allowTestLoopback(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"silenceID":"not a uuid"}`))
	}))
	defer srv.Close()

	if _, err := CreateSilence(srv.URL, "", "", "ada", "c", time.Now().Add(time.Minute), []SilenceMatcher{
		{Name: "alertname", Value: "HighCPUUsage"},
	}); err == nil {
		t.Fatal("expected a non-uuid id to be rejected")
	}
}
