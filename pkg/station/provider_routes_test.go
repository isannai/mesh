package station

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isannai/mesh/pkg/glog"
	"github.com/isannai/mesh/pkg/tunnel"
)

// SEC-16: the station's management API is gone. These were served behind a
// gate that took any owner signature, however old and whatever it was made
// for, and isannd hands every non-/svc path on its public door to the station.
// Now they are 404 whether signed or not.
func TestManagementRoutesAreGone(t *testing.T) {
	p := &Provider{Base: &tunnel.Base{Log: glog.New(glog.Config{})}}
	p.Auth.Owner = "0x1111111111111111111111111111111111111111"

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/provider/auth"},
		{http.MethodPost, "/provider/auth"},
		{http.MethodPost, "/provider/auth-verify"},
		{http.MethodGet, "/provider/profiles"},
		{http.MethodPost, "/provider/active-profile"},
		{http.MethodPost, "/provider/profile"},
		{http.MethodDelete, "/provider/profile"},
		{http.MethodGet, "/provider/partials"},
		{http.MethodPost, "/provider/scan-local"},
		{http.MethodPost, "/provider/save-package"},
		{http.MethodGet, "/provider/logs"},
		{http.MethodPost, "/provider/about"},
		{http.MethodPost, "/provider/emblem"},
		{http.MethodDelete, "/provider/emblem"},
		{http.MethodPost, "/provider/register"},
		{http.MethodGet, "/provider/config"},
	} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "ISANN 0xdeadbeef")
		r.Header.Set("X-ISANN-Message", "anything")
		w := httptest.NewRecorder()
		p.HandleProviderHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d %s, want 404", c.method, c.path, w.Code, w.Body)
		}
	}
}
