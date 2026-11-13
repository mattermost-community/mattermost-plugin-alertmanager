package alertmanager

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
)

// RequestError is a failed Alertmanager call. Error is safe to post in a
// channel. Transport errors name the resolved IP and port of the Alertmanager,
// and a URL parse error echoes the URL with any credentials in it, so the raw
// text is only available through Detail and Unwrap.
type RequestError struct {
	msg string
	err error
	// unavailable marks a failure that says the Alertmanager is down or
	// overloaded, as opposed to a request it refused. Only these trip the
	// circuit breaker.
	unavailable bool
}

func (e *RequestError) Error() string { return e.msg }

func (e *RequestError) Unwrap() error { return e.err }

// Detail is the underlying error text, for the plugin log and system admins.
func (e *RequestError) Detail() string { return e.err.Error() }

// ErrOutcomeUnknown marks a POST that may have reached Alertmanager but whose
// result the plugin never saw.
var ErrOutcomeUnknown = errors.New("alertmanager outcome unknown")

func statusError(code int) *RequestError {
	err := fmt.Errorf("alertmanager returned status %d", code)
	return &RequestError{msg: err.Error(), err: err, unavailable: retryableStatus(code)}
}

func invalidURL(err error) *RequestError {
	return &RequestError{msg: "the Alertmanager URL is not valid", err: err}
}

func outcomeUnknown(cause *RequestError) *RequestError {
	return &RequestError{msg: cause.msg, err: fmt.Errorf("%w: %w", ErrOutcomeUnknown, cause), unavailable: cause.unavailable}
}

// AsRequestError sanitizes an error from a direct GetClient().Do call the same
// way the API helpers do, so callers outside this package never surface the
// raw transport text.
func AsRequestError(err error) *RequestError {
	var re *RequestError
	if errors.As(err, &re) {
		return re
	}
	re, _ = transportError(err)
	return re
}

// transportError classifies a failed round trip or body read. permanent is true
// when another attempt cannot succeed: the destination is blocked, the
// certificate or protocol does not match, or the body is over the size cap.
func transportError(err error) (re *RequestError, permanent bool) {
	var blocked *BlockedDestinationError
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &blocked):
		return &RequestError{msg: blocked.Error(), err: err}, true
	case errors.Is(err, ErrResponseTooLarge):
		return &RequestError{msg: ErrResponseTooLarge.Error(), err: err}, true
	case errors.As(err, &certErr):
		return &RequestError{msg: "could not verify the Alertmanager TLS certificate. Check AlertManagerCABundle", err: err}, true
	case errors.As(err, &recordErr):
		return &RequestError{msg: "the TLS handshake with Alertmanager failed. Check whether the URL should be http:// or https://", err: err}, true
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		// Deliberately retried, not permanent: an in-cluster Service name
		// reports not-found while CoreDNS restarts or the Service is being
		// recreated, and failing on the first lookup would page nobody but
		// break every command during a routine rollout. A mistyped host
		// costs one RequestBudget per call until breakerThreshold calls
		// have failed, then the circuit breaker fails fast.
		return &RequestError{msg: "the Alertmanager host name does not resolve", err: err, unavailable: true}, false
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return &RequestError{msg: "Alertmanager did not respond in time", err: err, unavailable: true}, false
	default:
		return &RequestError{msg: "could not connect to Alertmanager", err: err, unavailable: true}, false
	}
}
