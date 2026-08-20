package alertmanager

import (
	"encoding/json"
	"fmt"
	"io"
)

// MaxResponseBytes caps how much of an Alertmanager response the plugin will
// buffer or decode. CL-09: there is no size bound on the wire, the decoders
// allocate proportionally, and Go's transport transparently gunzips — so a
// hostile or misconfigured endpoint can stream an unbounded (or gzip-amplified)
// body and OOM the plugin/Mattermost process. A time bound alone does not help;
// the memory is committed before the deadline fires.
const MaxResponseBytes = 8 << 20 // 8 MiB — far above any real AM response.

// errResponseTooLarge is returned when a response exceeds MaxResponseBytes.
func errResponseTooLarge() error {
	return fmt.Errorf("alertmanager response exceeds %d bytes; refusing to decode", MaxResponseBytes)
}

// DecodeJSONLimited decodes a single JSON value from r into v, reading AT MOST
// MaxResponseBytes+1 bytes. Overflow is rejected explicitly — a plain
// io.LimitReader would silently turn overflow into EOF and hide the attack.
func DecodeJSONLimited(r io.Reader, v any) error {
	limited := &io.LimitedReader{R: r, N: MaxResponseBytes + 1}
	err := json.NewDecoder(limited).Decode(v)
	if limited.N <= 0 {
		// We consumed the whole budget: the body was at least MaxResponseBytes+1.
		return errResponseTooLarge()
	}
	return err
}

// ReadAllLimited reads at most MaxResponseBytes from r, rejecting overflow. The
// bounded counterpart to io.ReadAll for any non-JSON Alertmanager body a caller
// needs to slurp — pairs with DecodeJSONLimited. (ExpireSilence stopped reading
// the error body for CL-08, so this currently has no in-tree caller, but it
// stays as the size-limit primitive for the next non-JSON reader.)
func ReadAllLimited(r io.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: r, N: MaxResponseBytes + 1}
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > MaxResponseBytes {
		return nil, errResponseTooLarge()
	}
	return b, nil
}
