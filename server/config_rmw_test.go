package main

import (
	"testing"
)

// noopTransform is a transform that returns the list unchanged — enough to
// exercise updateConfigsAtomic's lock guard without needing a KV backend.
func noopTransform(current []alertConfig) ([]alertConfig, error) { return current, nil }

// TestUpdateConfigsAtomicRequiresLock verifies the invariant that makes the
// read-modify-write atomic within a pod: updateConfigsAtomic must be called with
// configWriteMu already held. If a future caller forgets to lock (which would
// let two intra-pod writers needlessly lose CAS races to each other), the guard
// panics loudly instead of silently proceeding.
//
// The guard runs before any API access, so a bare &Plugin{} with a zero-value
// mutex and nil API is enough — the panic fires first.
func TestUpdateConfigsAtomicRequiresLock(t *testing.T) {
	p := &Plugin{}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected updateConfigsAtomic to panic when configWriteMu is not held, but it did not")
		}
	}()

	_, _, _ = p.updateConfigsAtomic(noopTransform)
	t.Fatal("unreachable: updateConfigsAtomic should have panicked")
}

// TestUpdateConfigsAtomicGuardAllowsHeldLock verifies the complement: when the
// lock IS held, the guard does not panic (it proceeds past the TryLock check).
// We only exercise the guard — the API is nil, so we recover the subsequent
// nil-API panic from KVGet and assert it is NOT the guard's "lock not held"
// panic. This proves a correctly-locked caller passes the guard.
func TestUpdateConfigsAtomicGuardAllowsHeldLock(t *testing.T) {
	p := &Plugin{}
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			if msg, ok := r.(string); ok && msg == "updateConfigsAtomic called without configWriteMu held — lock configWriteMu across the full read-modify-write" {
				t.Fatalf("guard wrongly panicked even though the lock was held: %v", r)
			}
			// Any other panic (e.g. nil API on the real read path) means we got
			// PAST the guard, which is what this test asserts.
		}
	}()

	_, _, _ = p.updateConfigsAtomic(noopTransform)
}
