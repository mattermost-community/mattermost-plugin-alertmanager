package alertmanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
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
	// Disable HTTP(S) proxying (F-001): the cloned DefaultTransport inherits
	// Proxy: ProxyFromEnvironment, so with HTTP_PROXY/HTTPS_PROXY set the client
	// would DIAL THE PROXY — the SSRF dial guard below would then validate the
	// proxy's IP, not the real Alertmanager destination, and the proxy would
	// happily fetch a blocked internal/metadata URL on our behalf. The plugin
	// always talks to Alertmanager directly, so no proxy is ever needed.
	t.Proxy = nil
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

// RequestBudget bounds one Alertmanager call, retries included, so a slow or
// failing Alertmanager holds a slash command or probe goroutine for at most
// this long. A caller's context can shorten it but never extend it.
const RequestBudget = 15 * time.Second

// httpBackoff returns the backoff policy used for Alertmanager API calls. It
// never gives up on its own; the request context ends the retries.
func httpBackoff() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 200 * time.Millisecond
	b.MaxInterval = 5 * time.Second
	b.MaxElapsedTime = 0
	return b
}

// runWithRetry runs op with backoff until it succeeds, returns a permanent
// error, or ctx ends. When ctx ends during a backoff wait, the last attempt's
// error is returned rather than the bare context error.
func runWithRetry(ctx context.Context, op func() error) error {
	var last error
	err := backoff.Retry(func() error {
		last = op()
		return last
	}, backoff.WithContext(httpBackoff(), ctx))
	if err == nil {
		return nil
	}
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		var re *RequestError
		if errors.As(last, &re) {
			return re
		}
		re, _ = transportError(err)
		return re
	}
	return err
}

func newRequest(ctx context.Context, method, url string, payload []byte) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, invalidURL(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// readBody reads a response body within MaxResponseBytes. A declared length
// over the cap is refused before any of the body is read.
func readBody(resp *http.Response) ([]byte, error) {
	if resp.ContentLength > MaxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	return ReadAllLimited(resp.Body)
}

func retryableStatus(code int) bool {
	return code >= 500 || code == http.StatusRequestTimeout || code == http.StatusTooManyRequests
}

// attemptError turns a transport or read failure into the error an attempt
// returns, marking it permanent when a retry cannot succeed.
func attemptError(err error) error {
	re, permanent := transportError(err)
	if permanent {
		return backoff.Permanent(re)
	}
	return re
}

// httpRetry sends an idempotent request (GET or DELETE) to the Alertmanager
// API, retrying transport failures and 5xx, 408 and 429 within RequestBudget.
//
// GET returns the body of a 200 and fails on any other status. DELETE returns
// the status for the caller to judge and never reads the body, so an unused
// body cannot be reflected to the user (CL-08).
//
// The body is read here because the request context is cancelled when this
// function returns. Errors are *RequestError, safe to show channel members.
func httpRetry(ctx context.Context, method, url string) ([]byte, int, error) {
	if method != http.MethodGet && method != http.MethodDelete {
		return nil, 0, fmt.Errorf("httpRetry: %s is not idempotent", method)
	}
	ctx, cancel := context.WithTimeout(ctx, RequestBudget)
	defer cancel()
	key := breakerKey(url)
	if err := breakers.allow(key); err != nil {
		return nil, 0, err
	}

	var body []byte
	var statusCode int
	err := runWithRetry(ctx, func() error {
		req, err := newRequest(ctx, method, url, nil)
		if err != nil {
			return backoff.Permanent(err)
		}
		resp, err := GetClient().Do(req)
		if err != nil {
			return attemptError(err)
		}
		defer func() { _ = resp.Body.Close() }()

		statusCode = resp.StatusCode
		if method == http.MethodDelete {
			return nil
		}
		if statusCode != http.StatusOK {
			if retryableStatus(statusCode) {
				return statusError(statusCode)
			}
			return backoff.Permanent(statusError(statusCode))
		}
		b, err := readBody(resp)
		if err != nil {
			return attemptError(err)
		}
		body = b
		return nil
	})
	breakers.record(key, err)
	if err != nil {
		return nil, 0, err
	}
	return body, statusCode, nil
}

// httpPostJSON sends one JSON POST. Creating a silence is not idempotent:
// re-sending after a lost reply creates a second silence that the plugin never
// records and no channel command can expire. So a request is only re-sent when
// the connection failed before the request was fully written. When it was
// written but no usable reply came back, or the reply was a 5xx that a proxy
// may have sent after Alertmanager acted, the error wraps ErrOutcomeUnknown.
//
// Only a 200 or 201 body is read. Error bodies are discarded unread so they
// cannot be reflected to the user (CL-08) or cost memory.
func httpPostJSON(ctx context.Context, url string, payload []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestBudget)
	defer cancel()
	key := breakerKey(url)
	if err := breakers.allow(key); err != nil {
		return nil, 0, err
	}

	var body []byte
	var statusCode int
	err := runWithRetry(ctx, func() error {
		var written atomic.Bool
		traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			WroteRequest: func(info httptrace.WroteRequestInfo) {
				if info.Err == nil {
					written.Store(true)
				}
			},
		})
		req, err := newRequest(traced, http.MethodPost, url, payload)
		if err != nil {
			return backoff.Permanent(err)
		}
		resp, err := GetClient().Do(req)
		if err != nil {
			if written.Load() {
				re, _ := transportError(err)
				return backoff.Permanent(outcomeUnknown(re))
			}
			return attemptError(err)
		}
		defer func() { _ = resp.Body.Close() }()

		statusCode = resp.StatusCode
		switch {
		case statusCode == http.StatusOK || statusCode == http.StatusCreated:
			b, err := readBody(resp)
			if err != nil {
				re, _ := transportError(err)
				return backoff.Permanent(outcomeUnknown(re))
			}
			body = b
			return nil
		case statusCode >= 500:
			return backoff.Permanent(outcomeUnknown(statusError(statusCode)))
		default:
			return backoff.Permanent(statusError(statusCode))
		}
	})
	breakers.record(key, err)
	if err != nil {
		return nil, statusCode, err
	}
	return body, statusCode, nil
}
