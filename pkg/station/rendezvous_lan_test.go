package station

import (
	"net"
	"strings"
	"testing"
)

// SEC-05: station listens on loopback by default (isannd is its only caller),
// but the LAN address it advertises to RV must still be the host's real LAN IP.
// Never 127.0.0.1: to a peer on the same LAN that is the peer itself.
func TestGetLANAddrLoopbackListen(t *testing.T) {
	all := getLANAddr(":8090")
	if all != "" && !strings.HasSuffix(all, ":8090") {
		t.Errorf(`getLANAddr(":8090") = %q, want the listen port`, all)
	}
	for _, listen := range []string{"127.0.0.1:8090", "localhost:8090", "[::1]:8090", "0.0.0.0:8090"} {
		got := getLANAddr(listen)
		if got != all {
			t.Errorf("getLANAddr(%q) = %q, want %q as for an unspecified host", listen, got, all)
		}
		if host, _, err := net.SplitHostPort(got); err == nil {
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
				t.Errorf("getLANAddr(%q) advertises loopback %q", listen, got)
			}
		}
	}
	// A listen address naming a LAN interface is advertised as it is.
	if got := getLANAddr("10.1.2.3:8090"); got != "10.1.2.3:8090" {
		t.Errorf("getLANAddr(10.1.2.3:8090) = %q", got)
	}
}
