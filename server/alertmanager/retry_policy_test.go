package alertmanager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func shortBudget(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func testMatchers() []SilenceMatcher {
	return []SilenceMatcher{{Name: "alertname", Value: "HighCPUUsage"}}
}

// fakeAM serves GET /api/v2/silences from whatever the POST handler stored, so
// a test can decide what Alertmanager "really" did with a POST.
type fakeAM struct {
	posts  atomic.Int32
	stored atomic.Value
	onPost func(w http.ResponseWriter, raw []byte)
}

func (f *fakeAM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		f.posts.Add(1)
		raw, _ := io.ReadAll(r.Body)
		f.onPost(w, raw)
	case http.MethodGet:
		list, _ := f.stored.Load().([]map[string]any)
		if list == nil {
			list = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(list)
	}
}

// store records the posted silence as Alertmanager would list it.
func (f *fakeAM) store(raw []byte, mutate func(map[string]any)) {
	var s map[string]any
	_ = json.Unmarshal(raw, &s)
	s["id"] = testSilenceUUID
	s["status"] = map[string]any{"state": "active"}
	s["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	if mutate != nil {
		mutate(s)
	}
	f.stored.Store([]map[string]any{s})
}

func dropConnection(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func TestCreateSilenceAdoptsTheSilenceWhenTheReplyIsLost(t *testing.T) {
	allowTestLoopback(t)
	am := &fakeAM{}
	am.onPost = func(w http.ResponseWriter, raw []byte) {
		am.store(raw, nil)
		dropConnection(w)
	}
	srv := httptest.NewServer(am)
	defer srv.Close()

	id, err := CreateSilence(context.Background(), srv.URL, "", "", "ada", "from mattermost", time.Now().Add(time.Hour), testMatchers())
	if err != nil {
		t.Fatalf("want the stored silence adopted, got %v", err)
	}
	if id != testSilenceUUID {
		t.Fatalf("id %q", id)
	}
	if n := am.posts.Load(); n != 1 {
		t.Fatalf("POST sent %d times; a written request must never be re-sent", n)
	}
}

func TestCreateSilenceAdoptsTheSilenceAfterATruncatedSuccess(t *testing.T) {
	allowTestLoopback(t)
	am := &fakeAM{}
	am.onPost = func(w http.ResponseWriter, raw []byte) {
		am.store(raw, nil)
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"silenceID":"`))
	}
	srv := httptest.NewServer(am)
	defer srv.Close()

	id, err := CreateSilence(context.Background(), srv.URL, "", "", "ada", "from mattermost", time.Now().Add(time.Hour), testMatchers())
	if err != nil || id != testSilenceUUID {
		t.Fatalf("id %q err %v", id, err)
	}
	if n := am.posts.Load(); n != 1 {
		t.Fatalf("POST sent %d times after a 200", n)
	}
}

func TestCreateSilenceDoesNotResendAfterAServerError(t *testing.T) {
	allowTestLoopback(t)
	am := &fakeAM{}
	am.onPost = func(w http.ResponseWriter, _ []byte) { w.WriteHeader(http.StatusBadGateway) }
	srv := httptest.NewServer(am)
	defer srv.Close()

	_, err := CreateSilence(shortBudget(t, time.Second), srv.URL, "", "", "ada", "from mattermost", time.Now().Add(time.Hour), testMatchers())
	if err == nil || !strings.Contains(err.Error(), "did not confirm") {
		t.Fatalf("want an unconfirmed-outcome error, got %v", err)
	}
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("error should wrap ErrOutcomeUnknown: %v", err)
	}
	if n := am.posts.Load(); n != 1 {
		t.Fatalf("POST sent %d times after a 502", n)
	}
}

func TestCreateSilenceDoesNotAdoptALookalike(t *testing.T) {
	allowTestLoopback(t)
	am := &fakeAM{}
	am.onPost = func(w http.ResponseWriter, raw []byte) {
		am.store(raw, func(s map[string]any) { s["comment"] = "someone else" })
		dropConnection(w)
	}
	srv := httptest.NewServer(am)
	defer srv.Close()

	if _, err := CreateSilence(shortBudget(t, time.Second), srv.URL, "", "", "ada", "from mattermost", time.Now().Add(time.Hour), testMatchers()); err == nil {
		t.Fatal("a silence with a different comment must not be adopted")
	}
}

type failFirstTransport struct {
	next  http.RoundTripper
	calls atomic.Int32
}

func (f *failFirstTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.calls.Add(1) == 1 {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	return f.next.RoundTrip(r)
}

func TestCreateSilenceResendsWhenNothingWasWritten(t *testing.T) {
	allowTestLoopback(t)
	prev := GetClient()
	t.Cleanup(func() { SetClient(prev) })
	SetClient(&http.Client{Transport: &failFirstTransport{next: NewTransport()}, CheckRedirect: RefuseRedirect})

	am := &fakeAM{}
	am.onPost = func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(`{"silenceID":"` + testSilenceUUID + `"}`))
	}
	srv := httptest.NewServer(am)
	defer srv.Close()

	id, err := CreateSilence(context.Background(), srv.URL, "", "", "ada", "c", time.Now().Add(time.Hour), testMatchers())
	if err != nil || id != testSilenceUUID {
		t.Fatalf("id %q err %v", id, err)
	}
	if n := am.posts.Load(); n != 1 {
		t.Fatalf("server saw %d POSTs, want 1", n)
	}
}

func TestPostErrorBodyIsNotReflected(t *testing.T) {
	allowTestLoopback(t)
	const secret = "SECRET-INTERNAL-CONFIG-abc123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(secret))
	}))
	defer srv.Close()

	_, err := CreateSilence(context.Background(), srv.URL, "", "", "ada", "c", time.Now().Add(time.Hour), testMatchers())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error reflected the response body or was nil: %v", err)
	}
}

func TestGetDoesNotRetryClientErrors(t *testing.T) {
	allowTestLoopback(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := ListAlerts(context.Background(), srv.URL, "u", "wrong"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want a 401 error, got %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("401 retried: %d requests", n)
	}
}

func TestGetRetriesServerErrors(t *testing.T) {
	allowTestLoopback(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	if _, err := ListSilences(context.Background(), srv.URL, "", ""); err != nil {
		t.Fatalf("want success after one 503, got %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("hits %d", n)
	}
}

func TestOversizedBodyIsNotFetchedAgain(t *testing.T) {
	allowTestLoopback(t)
	cases := map[string]http.HandlerFunc{
		"declared length": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(MaxResponseBytes+1))
			w.WriteHeader(http.StatusOK)
		},
		"streamed body": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			chunk := []byte(strings.Repeat("x", 1<<20))
			for range MaxResponseBytes/len(chunk) + 1 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				h(w, r)
			}))
			defer srv.Close()

			_, err := ListAlerts(context.Background(), srv.URL, "", "")
			if !errors.Is(err, ErrResponseTooLarge) {
				t.Fatalf("want ErrResponseTooLarge, got %v", err)
			}
			if n := hits.Load(); n != 1 {
				t.Fatalf("oversized body fetched %d times", n)
			}
		})
	}
}

func TestBlockedDestinationFailsFastWithoutTheAddress(t *testing.T) {
	SetAllowedNets(nil)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()

	start := time.Now()
	_, err := ListAlerts(context.Background(), srv.URL, "", "")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("blocked destination was retried for %s", elapsed)
	}
	var blocked *BlockedDestinationError
	if !errors.As(err, &blocked) {
		t.Fatalf("want a BlockedDestinationError, got %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error text names the resolved address: %s", err)
	}
	if hits.Load() != 0 {
		t.Fatal("blocked destination was reached")
	}
}

func TestTransportErrorTextHidesTheAddress(t *testing.T) {
	allowTestLoopback(t)
	ctx := shortBudget(t, 300*time.Millisecond)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	_, err = ListAlerts(ctx, "http://"+addr, "", "")
	var re *RequestError
	if !errors.As(err, &re) {
		t.Fatalf("want a RequestError, got %T %v", err, err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), addr) {
		t.Fatalf("error text names the address: %s", err)
	}
	if !strings.Contains(re.Detail(), "127.0.0.1") {
		t.Fatalf("Detail should keep the address for the log: %s", re.Detail())
	}
}

func TestRetriesStopAtTheRequestBudget(t *testing.T) {
	allowTestLoopback(t)
	ctx := shortBudget(t, 400*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	start := time.Now()
	_, err := ListAlerts(ctx, srv.URL, "", "")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("retries ran %s past a 400ms budget", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want the last status in the error, got %v", err)
	}
}

func TestHTTPRetryRefusesNonIdempotentMethods(t *testing.T) {
	if _, _, err := httpRetry(context.Background(), http.MethodPost, "http://203.0.113.7", "", ""); err == nil {
		t.Fatal("httpRetry must refuse POST")
	}
}

func TestCreateSilenceFindsASilenceStoredAfterTheReply(t *testing.T) {
	allowTestLoopback(t)
	am := &fakeAM{}
	am.onPost = func(w http.ResponseWriter, raw []byte) {
		time.AfterFunc(600*time.Millisecond, func() { am.store(raw, nil) })
		w.WriteHeader(http.StatusGatewayTimeout)
	}
	srv := httptest.NewServer(am)
	defer srv.Close()

	id, err := CreateSilence(context.Background(), srv.URL, "", "", "ada", "c", time.Now().Add(time.Hour), testMatchers())
	if err != nil || id != testSilenceUUID {
		t.Fatalf("want the late silence adopted, got id %q err %v", id, err)
	}
}

func TestCreateSilencePostAndLookupShareOneDeadline(t *testing.T) {
	allowTestLoopback(t)
	release := make(chan struct{})
	am := &fakeAM{}
	am.onPost = func(w http.ResponseWriter, raw []byte) {
		am.store(raw, nil)
		w.(http.Flusher).Flush()
		<-release
	}
	srv := httptest.NewServer(am)
	defer srv.Close()
	defer close(release)

	const budget = 2400 * time.Millisecond
	start := time.Now()
	id, err := CreateSilence(shortBudget(t, budget), srv.URL, "", "", "ada", "c", time.Now().Add(time.Hour), testMatchers())
	if elapsed := time.Since(start); elapsed > budget+500*time.Millisecond {
		t.Fatalf("create took %s against a %s deadline", elapsed, budget)
	}
	if err != nil || id != testSilenceUUID {
		t.Fatalf("want the silence found in the time left after the POST, got id %q err %v", id, err)
	}
}
