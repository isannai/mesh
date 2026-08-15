package probe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/isannai/mesh/pkg/auth"
	"github.com/isannai/mesh/pkg/tunnel"
)

const testProber = "0xc8ff97a1b2c3d4e5f60718293a4b5c6d7e8f9011"

// mintToken signs an appointment the way `ivm account issue --kind prober` does,
// and returns the issuer that should be recovered from it.
func mintToken(t *testing.T, prober string, issued, expire time.Time) (token, issuer string) {
	t.Helper()
	pk, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	msg := tunnel.ProberMessage(prober, issued.UnixMilli(), expire.UnixMilli())
	sig, err := auth.SignMessage(msg, pk)
	if err != nil {
		t.Fatal(err)
	}
	return tunnel.EncodeProberToken(msg, sig),
		strings.ToLower(crypto.PubkeyToAddress(pk.PublicKey).Hex())
}

// apptServer fakes isannd's GET /internal/api/cred/prober.
func apptServer(t *testing.T, body map[string]any, gotSession *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotSession != nil {
			*gotSession = r.Header.Get("X-ISANN-Session")
		}
		if r.URL.Path != appointmentPath {
			t.Errorf("path = %q, want %q", r.URL.Path, appointmentPath)
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestFetchAppointment(t *testing.T) {
	now := time.Now()
	issued, expire := now.Add(-time.Hour), now.Add(24*time.Hour)
	tok, issuer := mintToken(t, testProber, issued, expire)

	var gotSession string
	srv := apptServer(t, map[string]any{"alias": "faucet", "token": tok}, &gotSession)
	defer srv.Close()

	a, ok, err := FetchAppointment(srv.URL, srv.Client())
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if a.Alias != "faucet" || a.Token != tok {
		t.Errorf("got %+v", a)
	}
	// 🔴 No session header. The prober is a background process with no wallet;
	// depending on one would mean going idle whenever a session expired.
	if gotSession != "" {
		t.Errorf("a session header was sent (%q)", gotSession)
	}
	// Issuer is RECOVERED here, not taken from the reply — the token carries
	// its own proof, so nothing else has to be trusted.
	if a.Issuer != issuer {
		t.Errorf("issuer = %q, want %q", a.Issuer, issuer)
	}
	if a.Prober != testProber {
		t.Errorf("prober = %q, want %q", a.Prober, testProber)
	}
	if !a.Active(now) {
		t.Error("appointment should be active")
	}
}

// The reply's own fields must not override the signed ones: only the message
// is covered by the signature.
func TestFetchAppointmentIgnoresUnsignedFields(t *testing.T) {
	now := time.Now()
	issued, expire := now.Add(-time.Hour), now.Add(time.Hour)
	tok, issuer := mintToken(t, testProber, issued, expire)

	srv := apptServer(t, map[string]any{
		"alias":  "faucet",
		"token":  tok,
		"issuer": "0xdeadbeef00000000000000000000000000000000", // lie
		"expire": now.Add(365 * 24 * time.Hour).UnixMilli(),    // lie
	}, nil)
	defer srv.Close()

	a, ok, err := FetchAppointment(srv.URL, srv.Client())
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if a.Issuer != issuer {
		t.Errorf("issuer = %q, want the RECOVERED %q", a.Issuer, issuer)
	}
	if a.ExpireMs != expire.UnixMilli() {
		t.Errorf("expire = %d, want the SIGNED %d", a.ExpireMs, expire.UnixMilli())
	}
	if a.Active(now.Add(48 * time.Hour)) {
		t.Error("the reply's inflated window was honoured")
	}
}

// "Nobody has appointed this node" is the NORMAL state for a node that has the
// mesh installed. It must not surface as an error.
func TestFetchAppointmentNoneIsNotAnError(t *testing.T) {
	srv := apptServer(t, map[string]any{}, nil)
	defer srv.Close()

	a, ok, err := FetchAppointment(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("an empty reply produced an appointment: %+v", a)
	}
}

// A bearer appointment would let one leaked copy mint tickets network-wide.
func TestFetchAppointmentRejectsBearer(t *testing.T) {
	now := time.Now()
	tok, _ := mintToken(t, "", now, now.Add(time.Hour))

	srv := apptServer(t, map[string]any{"alias": "faucet", "token": tok}, nil)
	defer srv.Close()

	if _, ok, err := FetchAppointment(srv.URL, srv.Client()); ok || err == nil {
		t.Fatalf("bearer appointment adopted: ok=%v err=%v", ok, err)
	}
}

func TestFetchAppointmentRejectsGarbage(t *testing.T) {
	srv := apptServer(t, map[string]any{"alias": "faucet", "token": "ianprb_@@@"}, nil)
	defer srv.Close()

	if _, ok, err := FetchAppointment(srv.URL, srv.Client()); ok || err == nil {
		t.Fatalf("garbage token adopted: ok=%v err=%v", ok, err)
	}
}

func TestAppointmentActive(t *testing.T) {
	now := time.Now()
	a := Appointment{IssuedMs: now.Add(-time.Hour).UnixMilli(), ExpireMs: now.Add(time.Hour).UnixMilli()}
	if !a.Active(now) {
		t.Error("inside the window should be active")
	}
	if a.Active(now.Add(2 * time.Hour)) {
		t.Error("after expiry should be inactive")
	}
	if a.Active(now.Add(-2 * time.Hour)) {
		t.Error("before issuance should be inactive")
	}
}
