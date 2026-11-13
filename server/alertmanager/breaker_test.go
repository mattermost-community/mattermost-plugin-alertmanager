package alertmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBreakerOpensThenLetsOneProbeThrough(t *testing.T) {
	now := time.Unix(0, 0)
	b := newBreakerSet(func() time.Time { return now })
	down := &RequestError{msg: "could not connect to Alertmanager", unavailable: true}

	for range breakerThreshold {
		if err := b.allow("h"); err != nil {
			t.Fatalf("open before the threshold: %v", err)
		}
		b.record("h", down)
	}
	err := b.allow("h")
	if err == nil || !strings.Contains(err.Error(), "could not connect") {
		t.Fatalf("want a fast failure that keeps the reason, got %v", err)
	}

	now = now.Add(breakerCooldown)
	if err := b.allow("h"); err != nil {
		t.Fatalf("probe refused after the cooldown: %v", err)
	}
	if err := b.allow("h"); err == nil {
		t.Fatal("a second request went through while the probe was in flight")
	}
	b.record("h", nil)
	if err := b.allow("h"); err != nil {
		t.Fatalf("a successful probe should close the breaker: %v", err)
	}
}

func TestBreakerIgnoresRequestsAlertmanagerRefused(t *testing.T) {
	b := newBreakerSet(time.Now)
	for range breakerThreshold + 1 {
		b.record("h", statusError(http.StatusUnauthorized))
	}
	if err := b.allow("h"); err != nil {
		t.Fatalf("a 401 says Alertmanager is up; breaker opened: %v", err)
	}
}

func TestBreakerStopsTrackingNewHostsAtTheCap(t *testing.T) {
	b := newBreakerSet(time.Now)
	down := &RequestError{unavailable: true}
	for i := range maxTrackedHosts + 10 {
		b.record("h"+strings.Repeat("x", i), down)
	}
	if n := len(b.hosts); n != maxTrackedHosts {
		t.Fatalf("tracked %d hosts", n)
	}
}

func TestUnreachableAlertmanagerFailsFastOnceTheBreakerOpens(t *testing.T) {
	allowTestLoopback(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	for range breakerThreshold {
		_, _ = ListAlerts(shortBudget(t, 100*time.Millisecond), srv.URL)
	}
	before := hits.Load()
	start := time.Now()
	_, err := ListSilences(context.Background(), srv.URL)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("open breaker still waited %s", elapsed)
	}
	if hits.Load() != before {
		t.Fatal("open breaker still sent a request")
	}
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "stopped calling") {
		t.Fatalf("want the last reason and the breaker note, got %v", err)
	}
}
