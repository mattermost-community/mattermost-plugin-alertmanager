package alertmanager

import (
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"
)

const (
	// breakerThreshold failed requests in a row open the breaker. Each one has
	// already spent up to RequestBudget retrying, so only a backend that stays
	// down trips it.
	breakerThreshold = 3
	breakerCooldown  = 30 * time.Second
	// maxTrackedHosts caps the map, since team admins choose the URLs. Only
	// failing hosts are kept; a success deletes the entry.
	maxTrackedHosts = 256
)

type breakerState struct {
	failures  int
	openUntil time.Time
	probing   bool
	lastMsg   string
}

// breakerSet is a per-host circuit breaker. While a host is open, calls fail at
// once instead of each holding a goroutine for RequestBudget. After the cooldown
// one request goes through as a probe and its result closes or reopens the
// breaker. The state is per plugin process, so each HA node trips on its own.
type breakerSet struct {
	mu    sync.Mutex
	now   func() time.Time
	hosts map[string]*breakerState
}

var breakers = newBreakerSet(time.Now)

func newBreakerSet(now func() time.Time) *breakerSet {
	return &breakerSet{now: now, hosts: map[string]*breakerState{}}
}

func breakerKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Scheme + "://" + u.Host
}

// allow returns an error when calls to key should fail fast.
func (b *breakerSet) allow(key string) *RequestError {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.hosts[key]
	if s == nil || s.failures < breakerThreshold {
		return nil
	}
	if b.now().Before(s.openUntil) || s.probing {
		err := fmt.Errorf("circuit open after %d failed requests: %s", s.failures, s.lastMsg)
		return &RequestError{
			msg: fmt.Sprintf("%s. The plugin stopped calling this Alertmanager for up to %s after %d failed requests in a row", s.lastMsg, breakerCooldown, s.failures),
			err: err,
		}
	}
	s.probing = true
	return nil
}

// record updates key with the outcome of a request that allow let through.
func (b *breakerSet) record(key string, err error) {
	var re *RequestError
	unavailable := errors.As(err, &re) && re.unavailable
	b.mu.Lock()
	defer b.mu.Unlock()
	if !unavailable {
		delete(b.hosts, key)
		return
	}
	s := b.hosts[key]
	if s == nil {
		if len(b.hosts) >= maxTrackedHosts {
			return
		}
		s = &breakerState{}
		b.hosts[key] = s
	}
	s.probing = false
	s.failures++
	s.lastMsg = re.msg
	if s.failures >= breakerThreshold {
		s.openUntil = b.now().Add(breakerCooldown)
	}
}

func (b *breakerSet) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.hosts)
}
