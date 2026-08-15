package probe

// appointment.go — the prober's licence to operate.
//
// The appointment is NOT kept in this program's own config. It is read from
// isannd, which is where `isann cred add` puts it and where `isann cred list`
// shows it. One place, one answer to "which appointment is in effect".
//
// That indirection is the point. Today an operator installs the appointment by
// hand; the intended next step is for the RV to deliver it on the register ack
// (see docs/confirm/faucet/autonomous-probe.md in the node repo). Because the
// prober asks isannd rather than holding its own copy, that change lands with
// no edit here — the appointment simply starts appearing.
//
// 🔴 NO SESSION TOKEN. The route used here is the one cred read isannd leaves
// ungated (loopback-guarded only), precisely because a prober is a background
// process with no wallet. Reading the operator-gated pool instead would tie a
// long-running mesh to `isann auth unlock`, and sessions EXPIRE — the prober
// would go quietly idle the day one did.
//
// No appointment means IDLE: no directory fetch, no firing, nothing but a log
// line. A prober with no licence must not look like one with a licence and
// nothing to do.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/auth"
	"github.com/isannai/mesh/pkg/tunnel"
)

// appointmentPath is isannd's ungated prober-appointment read.
const appointmentPath = "/internal/api/cred/prober"

// Appointment is the active prober appointment as isannd holds it.
type Appointment struct {
	Alias    string
	Token    string // the ianprb_… blob, attached to every ticket this prober signs
	Issuer   string // recovered here from the signature, lowercase
	Prober   string // the address allowed to sign tickets
	IssuedMs int64
	ExpireMs int64
}

// Active reports whether now falls inside the appointment window.
func (a Appointment) Active(now time.Time) bool {
	ms := now.UnixMilli()
	return ms >= a.IssuedMs && ms <= a.ExpireMs
}

// Expires returns the window end.
func (a Appointment) Expires() time.Time { return time.UnixMilli(a.ExpireMs) }

// appointmentReply mirrors GET /internal/api/cred/prober.
//
// Only Token is really consumed: everything else is re-derived from the signed
// message below, so a wrong or stale field here cannot mislead the prober.
type appointmentReply struct {
	Alias string `json:"alias"`
	Token string `json:"token"`
}

// FetchAppointment asks isannd for the active prober appointment.
//
// ok=false with a nil error means "none installed" — the normal state for a
// node that has the probe mesh but has not been appointed. It is not a failure
// and must not be logged as one.
func FetchAppointment(isanndURL string, c *http.Client) (Appointment, bool, error) {
	endpoint := strings.TrimRight(isanndURL, "/") + appointmentPath
	resp, err := c.Get(endpoint)
	if err != nil {
		return Appointment{}, false, fmt.Errorf("read appointment: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Appointment{}, false, fmt.Errorf("read appointment: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Appointment{}, false, fmt.Errorf("read appointment: %s", resp.Status)
	}

	var reply appointmentReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return Appointment{}, false, fmt.Errorf("decode appointment: %w", err)
	}
	if strings.TrimSpace(reply.Token) == "" {
		return Appointment{}, false, nil // nobody has appointed this node
	}
	return parseAppointment(reply.Alias, reply.Token)
}

// parseAppointment derives every field from the signed token.
//
// isannd reports the issuer and window too, and it runs on the same host, so
// trusting the reply would be defensible. Recovering instead costs one call and
// makes the prober correct even if the reply is wrong — the token carries its
// own proof, so there is no reason to depend on anything else.
func parseAppointment(alias, token string) (Appointment, bool, error) {
	msg, sig, err := tunnel.DecodeProberToken(token)
	if err != nil {
		return Appointment{}, false, fmt.Errorf("appointment %q: %w", alias, err)
	}
	// ParseProberMessage also rejects an empty prober, so a bearer appointment
	// can never be adopted here. A bearer one would make every leaked copy a
	// network-wide ticket mint.
	prober, issuedMs, expireMs, ok := tunnel.ParseProberMessage(msg)
	if !ok {
		return Appointment{}, false, fmt.Errorf("appointment %q is malformed (or binds no prober)", alias)
	}
	issuer, err := auth.RecoverAddress(msg, sig)
	if err != nil {
		return Appointment{}, false, fmt.Errorf("appointment %q: bad signature: %w", alias, err)
	}
	return Appointment{
		Alias:    alias,
		Token:    token,
		Issuer:   strings.ToLower(issuer),
		Prober:   prober,
		IssuedMs: issuedMs,
		ExpireMs: expireMs,
	}, true, nil
}
