package alertmanager

import (
	"net/http"
	"testing"
)

// TestClientConcurrentSwap proves the Alertmanager client swap is race-free:
// SetClient (config-change goroutine) runs concurrently with GetClient
// (slash-command / probe goroutines). Under `go test -race` a plain package var
// would flag here; the atomic.Pointer does not. Also guards that a swap never
// leaves the client nil.
func TestClientConcurrentSwap(t *testing.T) {
	const iters = 2000
	done := make(chan struct{})
	go func() {
		for i := 0; i < iters; i++ {
			SetClient(&http.Client{Transport: NewTransport(), CheckRedirect: RefuseRedirect})
		}
		close(done)
	}()
	for i := 0; i < iters; i++ {
		if GetClient() == nil {
			t.Fatal("GetClient returned nil during concurrent swap")
		}
	}
	<-done
	if GetClient() == nil {
		t.Fatal("client is nil after swaps")
	}
}
