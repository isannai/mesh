package faucet

// ticket_ext.go: the model a ticket was earned with, under a second signature.
//
//	ISANN-TICKET-EXT:<chainId>:<faucetAddr>:<prober>:<node>:<owner>:<root>:<kind>:<params>:<model>
//
// Every node that passes a check gets a ticket. One that runs a model file the
// operators have designated also gets these fields, so the faucet can later pay
// it more than a plain ticket, and more for a larger model. That needs the
// ticket to say which file the node was checked on and how large it counts as,
// in a form the node cannot edit after the prober hands it over.
//
// # WHY A SECOND SIGNATURE, NOT A LONGER MESSAGE
//
// The rendezvous rebuilds Message() from the ticket it receives and checks Sig
// against it. Folding these fields into Message() would make every rendezvous
// that has not been rebuilt refuse every new ticket as bad_signature. A second
// signature over a separate string leaves Sig exactly as it was: a rendezvous
// that knows nothing of this pays the ticket as before, and the one that later
// prices by size checks SigExt as well.
//
// The extension string repeats the six ticket fields so it stands on its own.
// A SigExt moved onto another ticket would cover that other ticket's fields
// and fail.
//
// # THE FIELDS
//
//	kind    "text" or "image". One parameter count is priced differently for a
//	        picture model, and the count alone cannot tell the two apart.
//	params  the parameter count the faucet pays by, in billions with exactly
//	        three decimals ("14.768"). It comes from the designated list, never
//	        from the node: a mixture-of-experts model is listed at what it
//	        costs to run, not at its total size.
//	model   sha256 of the model file, 0x + 64 lowercase hex. The same file
//	        hash the node reports to the rendezvous directory as model_hash.
//
// They travel as strings so the signed bytes are the bytes on the wire.
// CheckTicketExt refuses every spelling but the canonical one, so two programs
// can never sign and verify different strings for the same value.

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// TicketExtPrefix tags the string the second signature covers.
const TicketExtPrefix = "ISANN-TICKET-EXT:"

// The kinds a prober writes. A node accepts any short lowercase word so a kind
// added later does not need every node upgraded first; what a kind is worth is
// the rendezvous's decision.
const (
	KindText  = "text"
	KindImage = "image"
)

// FormatParams renders a parameter count the way a ticket carries it: billions,
// exactly three decimals, the rest cut off.
//
//	14,768,307,200 → "14.768"
//	 7,090,000,000 → "7.090"
//
// Integer division, never floating point: a float can round the last digit
// differently on another machine, and a different digit is a different signed
// string. Trailing zeros stay for the same reason: "7.09" and "7.090" would
// sign differently.
func FormatParams(count uint64) string {
	m := count / 1_000_000
	return strconv.FormatUint(m/1000, 10) + "." + fmt.Sprintf("%03d", m%1000)
}

// ParseParams reads a ticket's params back as millions of parameters, so a
// price table can compare plain integers. Only FormatParams's own spelling is
// accepted.
func ParseParams(s string) (uint64, error) {
	whole, frac, ok := strings.Cut(s, ".")
	if !ok || len(frac) != 3 || whole == "" || len(whole) > 7 ||
		(len(whole) > 1 && whole[0] == '0') || !isDigits(whole) || !isDigits(frac) {
		return 0, fmt.Errorf("ticket: params %q is not <billions>.<3 digits>", s)
	}
	w, _ := strconv.ParseUint(whole, 10, 64)
	f, _ := strconv.ParseUint(frac, 10, 64)
	return w*1000 + f, nil
}

// ModelFromReport turns a model hash as a node reports it ("sha256:<hex>") into
// the ticket spelling ("0x<hex>"). A bare or 0x-prefixed hash is accepted too.
func ModelFromReport(s string) (string, error) {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(strings.ToLower(s), "sha256:"); ok {
		s = rest
	}
	h, err := ParseHash(s)
	if err != nil {
		return "", fmt.Errorf("ticket: model hash: %w", err)
	}
	return h.Hex(), nil
}

// HasExt reports whether the ticket carries any of the extension fields.
func (t Ticket) HasExt() bool {
	return t.Kind != "" || t.Params != "" || t.Model != "" || t.SigExt != ""
}

// ExtMessage is the string SigExt covers: the six ticket fields, then kind,
// params and model.
func (t Ticket) ExtMessage() string {
	return TicketExtPrefix + strings.TrimPrefix(t.Message(), TicketMessagePrefix) +
		":" + t.Kind + ":" + t.Params + ":" + t.Model
}

// checkExtFields validates kind, params and model in their canonical spelling.
func checkExtFields(t Ticket) error {
	if n := len(t.Kind); n == 0 || n > 16 || !isLowerWord(t.Kind) {
		return fmt.Errorf("ticket: kind %q is not a short lowercase word", t.Kind)
	}
	if _, err := ParseParams(t.Params); err != nil {
		return err
	}
	if len(t.Model) != 66 || !strings.HasPrefix(t.Model, "0x") || !isLowerHex(t.Model[2:]) {
		return fmt.Errorf("ticket: model %q is not 0x + 64 lowercase hex", t.Model)
	}
	return nil
}

// CheckTicketExt checks the extension's shape: none of it, or all of it in
// canonical form with a hex SigExt. A ticket without the extension passes,
// since it is an ordinary ticket. What it cannot check is the signature itself; that
// needs the prober's public key (VerifyTicketExt).
func CheckTicketExt(t Ticket) error {
	if !t.HasExt() {
		return nil
	}
	if err := checkExtFields(t); err != nil {
		return err
	}
	s := strings.TrimPrefix(t.SigExt, "0x")
	if s == "" || len(s)%2 != 0 || !isLowerHex(s) {
		return errors.New("ticket: sig_ext is not 0x-hex")
	}
	return nil
}

// SignTicketExt fills in SigExt. Kind, Params and Model must be set first; the
// signer is injected for the same reason SignTicket's is.
func SignTicketExt(t *Ticket, sign func(msg []byte) ([]byte, error)) error {
	if t == nil {
		return errors.New("ticket: nil")
	}
	if err := checkExtFields(*t); err != nil {
		return err
	}
	sig, err := sign([]byte(t.ExtMessage()))
	if err != nil {
		return fmt.Errorf("ticket: sign ext: %w", err)
	}
	if len(sig) == 0 {
		return errors.New("ticket: signer returned nothing")
	}
	t.SigExt = "0x" + hex.EncodeToString(sig)
	return nil
}

// VerifyTicketExt checks SigExt against the prober's public key. It says
// nothing about Sig; a caller that pays by the extension checks both.
func VerifyTicketExt(t Ticket, pubDER []byte, verify func(pub, msg, sig []byte) error) error {
	if !t.HasExt() {
		return errors.New("ticket: no model extension")
	}
	if err := CheckTicketExt(t); err != nil {
		return err
	}
	if len(pubDER) == 0 {
		return errors.New("ticket: no public key for this prober")
	}
	sig, err := (Ticket{Sig: t.SigExt}).SigBytes()
	if err != nil {
		return err
	}
	return verify(pubDER, []byte(t.ExtMessage()), sig)
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func isLowerWord(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
