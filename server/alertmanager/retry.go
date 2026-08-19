package alertmanager

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// NewTransport returns an http.Transport for Alertmanager calls: a clone of the
// default transport with transparent response compression DISABLED (CL-09), so a
// hostile endpoint cannot gzip-amplify a small wire body into a huge decoded one.
// Callers set TLSClientConfig on the returned transport when a CA bundle applies.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	// Install the SSRF dial guard (F-001): validate the resolved IP after DNS but
	// before connect, on every Alertmanager call, so a hostile URL can't reach
	// cloud-metadata / loopback / non-allowlisted internal targets. Timeouts match
	// http.DefaultTransport's dialer.
	t.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   safeDialControl,
	}).DialContext
	return t
}

// RefuseRedirect is the CheckRedirect policy for the Alertmanager client. The
// AM API never legitimately redirects the calls the plugin makes; a redirect is
// the signature of a path-normalizing front door (AM's ServeMux 301s a traversal
// path), and http.Client would otherwise FOLLOW it and re-send the Authorization
// header to the same host — turning a crafted request path into a credentialed
// GET against an arbitrary endpoint. Returning ErrUseLastResponse stops the
// follow and hands the redirect response back, which surfaces as a non-200 to
// the caller (CL-08). Applied to every construction of Client below.
func RefuseRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// client is the HTTP client used for all outbound Alertmanager API calls. It
// trusts system root CAs, disables response compression (defense in depth for
// the size-limited decode), and refuses redirects (see RefuseRedirect). The
// plugin swaps it for a CA-bundle-aware client when the AlertManagerCABundle
// setting changes (updateAlertmanagerHTTPClient in the main package).
//
// Held in an atomic.Pointer because that swap happens on the config-change
// goroutine while slash-command / probe goroutines read it concurrently — a
// plain package var would be a data race on the pointer word. Callers go through
// GetClient()/SetClient() and must not cache the returned client across calls.
var client atomic.Pointer[http.Client]

func init() {
	client.Store(&http.Client{Transport: NewTransport(), CheckRedirect: RefuseRedirect})
}

// GetClient returns the current Alertmanager HTTP client. Do not cache it — a
// CA-bundle config change may swap it via SetClient.
func GetClient() *http.Client { return client.Load() }

// SetClient atomically replaces the Alertmanager HTTP client. Called from
// OnConfigurationChange when the CA bundle setting changes.
func SetClient(c *http.Client) { client.Store(c) }

// httpBackoff returns the backoff policy used for Alertmanager API calls.
// Total elapsed time is capped at 30s so a slow/flaky Alertmanager doesn't
// stretch a slash-command response into the user-noticeable range.
func httpBackoff() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 200 * time.Millisecond
	b.MaxInterval = 15 * time.Second
	b.MaxElapsedTime = 30 * time.Second
	return b
}

// httpRetry issues an HTTP request to the Alertmanager API with exponential
// backoff retry. If user is non-empty, HTTP Basic Auth is added — paired with
// password, which must also be non-empty in that case. This is the path that
// makes Alertmanager instances behind an authenticating reverse proxy usable
// from the plugin's slash commands.
//
// Resolves the long-standing issue #7 (commands fail behind basic auth) and
// pulls in the work from open PR #604 (author: lipaysamart).
func httpRetry(method, url, user, password string) (*http.Response, error) {
	var resp *http.Response
	var err error

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	fn := func() error {
		req, errReq := http.NewRequestWithContext(ctx, method, url, nil)
		if errReq != nil {
			return errReq
		}
		if user != "" {
			req.SetBasicAuth(user, password)
		}

		resp, err = GetClient().Do(req) // nolint: bodyclose
		if err != nil {
			return err
		}

		// On a retryable status, close THIS attempt's body before returning the
		// error — backoff will re-issue and overwrite resp, so an unclosed body
		// here leaks the connection until GC.
		switch method {
		case http.MethodGet:
			if resp.StatusCode != http.StatusOK {
				_ = resp.Body.Close()
				return fmt.Errorf("status code is %d not 200", resp.StatusCode)
			}
		case http.MethodPost:
			if resp.StatusCode == http.StatusBadRequest {
				_ = resp.Body.Close()
				return fmt.Errorf("status code is %d not 3xx", resp.StatusCode)
			}
		}

		return nil
	}

	if errRetry := backoff.Retry(fn, httpBackoff()); errRetry != nil {
		return nil, errRetry
	}

	return resp, err
}
