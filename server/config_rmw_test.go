package main

import (
	"testing"
)

// TestSaveConfigsLockedRequiresLock verifies the invariant that makes the
// read-modify-write atomic: saveConfigsLocked must be called with
// configWriteMu already held. If a future caller forgets to lock (which
// would reopen the lost-update race), the guard panics loudly instead of
// silently persisting from a possibly-stale snapshot.
//
// The guard runs before any client/API access, so a bare &Plugin{} with a
// zero-value mutex and nil client is enough — the panic fires first.
func TestSaveConfigsLockedRequiresLock(t *testing.T) {
	p := &Plugin{}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected saveConfigsLocked to panic when configWriteMu is not held, but it did not")
		}
	}()

	// Lock NOT held → must panic before touching the config store.
	_ = p.saveConfigsLocked(nil)
	t.Fatal("unreachable: saveConfigsLocked should have panicked")
}

// TestSaveConfigsLockedGuardAllowsHeldLock verifies the complement: when the
// lock IS held, the guard does not panic (it proceeds past the TryLock check).
// We only exercise the guard, not the full save — the client is nil, so we
// recover the subsequent nil-client panic and assert it is NOT the guard's
// "lock not held" panic. This proves a correctly-locked caller passes the
// guard.
func TestSaveConfigsLockedGuardAllowsHeldLock(t *testing.T) {
	p := &Plugin{}
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			if msg, ok := r.(string); ok && msg == "saveConfigsLocked called without configWriteMu held — lock configWriteMu across the full read-modify-write" {
				t.Fatalf("guard wrongly panicked even though the lock was held: %v", r)
			}
			// Any other panic (e.g. nil client on the real save path) means
			// we got PAST the guard, which is what this test asserts.
		}
	}()

	_ = p.saveConfigsLocked([]alertConfig{})
}
