package station

import "testing"

// requestRegister wakes the register loop without blocking: a second request
// while one is pending is dropped, and a Provider with no channel is a no-op.
func TestRequestRegisterDoesNotBlock(t *testing.T) {
	p := &Provider{resyncCh: make(chan struct{}, 1)}
	p.requestRegister()
	p.requestRegister() // already pending: must return, not block
	if len(p.resyncCh) != 1 {
		t.Errorf("pending requests = %d, want 1", len(p.resyncCh))
	}
	<-p.resyncCh
	p.requestRegister()
	if len(p.resyncCh) != 1 {
		t.Errorf("after a drain, a new request must be queued: %d", len(p.resyncCh))
	}

	var nilProvider *Provider
	nilProvider.requestRegister()
	(&Provider{}).requestRegister()
}
