package faucet

import (
	"encoding/json"
	"strings"
	"testing"
)

func sampleExtTicket() Ticket {
	t := sampleTicket()
	t.Kind = KindText
	t.Params = "14.768"
	t.Model = "0x" + strings.Repeat("ab", 32)
	return t
}

// The spelling is signed, so it is pinned: billions, three decimals, cut off.
func TestFormatParams(t *testing.T) {
	for count, want := range map[uint64]string{
		14_768_307_200:  "14.768",
		7_090_000_000:   "7.090", // the trailing zero stays
		1_543_714_304:   "1.543", // cut, never rounded up
		999_999:         "0.000",
		0:               "0.000",
		405_000_000_000: "405.000",
	} {
		if got := FormatParams(count); got != want {
			t.Errorf("FormatParams(%d) = %q, want %q", count, got, want)
		}
		if m, err := ParseParams(want); err != nil || m != count/1_000_000 {
			t.Errorf("ParseParams(%q) = %d, %v", want, m, err)
		}
	}
	for _, bad := range []string{"", "14", "14.77", "14.7700", "014.768", "14,768", "-1.000", "1e3.000", ".768", "14.76x"} {
		if _, err := ParseParams(bad); err == nil {
			t.Errorf("ParseParams(%q) accepted", bad)
		}
	}
}

func TestModelFromReport(t *testing.T) {
	hexs := strings.Repeat("e0", 32)
	for _, in := range []string{"sha256:" + hexs, "SHA256:" + strings.ToUpper(hexs), "0x" + hexs, hexs} {
		got, err := ModelFromReport(in)
		if err != nil || got != "0x"+hexs {
			t.Errorf("ModelFromReport(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "sha256:", "sha256:abcd", "md5:" + hexs} {
		if _, err := ModelFromReport(bad); err == nil {
			t.Errorf("ModelFromReport(%q) accepted", bad)
		}
	}
}

func TestTicketExtSignVerify(t *testing.T) {
	pub, sign := testSigner(t)
	tk := sampleExtTicket()
	if err := SignTicket(&tk, sign); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := SignTicketExt(&tk, sign); err != nil {
		t.Fatalf("sign ext: %v", err)
	}
	if err := CheckTicketExt(tk); err != nil {
		t.Fatalf("shape: %v", err)
	}
	if err := VerifyTicketExt(tk, pub, testVerify); err != nil {
		t.Fatalf("honest extension rejected: %v", err)
	}
	// 🔴 The first signature is untouched: a rendezvous that knows nothing of
	// the extension still pays this ticket.
	if err := VerifyTicket(tk, pub, testVerify); err != nil {
		t.Fatalf("extension broke the ticket signature: %v", err)
	}
}

// Every field of the extension string must be covered, the six ticket fields
// included. Otherwise a SigExt could be lifted onto another ticket.
func TestTicketExtCoversEveryField(t *testing.T) {
	pub, sign := testSigner(t)
	base := sampleExtTicket()
	if err := SignTicketExt(&base, sign); err != nil {
		t.Fatalf("sign ext: %v", err)
	}
	for _, c := range []struct {
		name   string
		mutate func(*Ticket)
	}{
		{"chain id", func(x *Ticket) { x.ChainID = 1 }},
		{"faucet address", func(x *Ticket) { x.FaucetAddr[0] ^= 0xff }},
		{"prober", func(x *Ticket) { x.Prober[0] ^= 0xff }},
		{"node", func(x *Ticket) { x.Node[0] ^= 0xff }},
		{"owner", func(x *Ticket) { x.Owner[0] ^= 0xff }},
		{"root", func(x *Ticket) { x.Root[0] ^= 0xff }},
		{"kind", func(x *Ticket) { x.Kind = KindImage }},
		{"params, the size it is paid by", func(x *Ticket) { x.Params = "70.000" }},
		{"model", func(x *Ticket) { x.Model = "0x" + strings.Repeat("cd", 32) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			tampered := base
			c.mutate(&tampered)
			if err := VerifyTicketExt(tampered, pub, testVerify); err == nil {
				t.Error("tampered extension verified")
			}
		})
	}
}

func TestCheckTicketExt(t *testing.T) {
	if err := CheckTicketExt(sampleTicket()); err != nil {
		t.Errorf("a ticket without the extension is refused: %v", err)
	}
	ok := sampleExtTicket()
	ok.SigExt = "0x0102"
	if err := CheckTicketExt(ok); err != nil {
		t.Fatalf("well-formed extension refused: %v", err)
	}
	for _, c := range []struct {
		name   string
		mutate func(*Ticket)
	}{
		{"no sig_ext", func(x *Ticket) { x.SigExt = "" }},
		{"odd sig_ext", func(x *Ticket) { x.SigExt = "0x123" }},
		{"no kind", func(x *Ticket) { x.Kind = "" }},
		{"kind with a colon", func(x *Ticket) { x.Kind = "text:70.000" }},
		{"uppercase kind", func(x *Ticket) { x.Kind = "Text" }},
		{"params without decimals", func(x *Ticket) { x.Params = "14" }},
		{"model without 0x", func(x *Ticket) { x.Model = strings.Repeat("ab", 32) }},
		{"uppercase model", func(x *Ticket) { x.Model = "0x" + strings.Repeat("AB", 32) }},
		{"model as reported", func(x *Ticket) { x.Model = "sha256:" + strings.Repeat("ab", 32) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			bad := ok
			c.mutate(&bad)
			if err := CheckTicketExt(bad); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestTicketExtMessageShape(t *testing.T) {
	tk := sampleExtTicket()
	msg := tk.ExtMessage()
	want := TicketExtPrefix + strings.TrimPrefix(tk.Message(), TicketMessagePrefix) +
		":text:14.768:0x" + strings.Repeat("ab", 32)
	if msg != want {
		t.Fatalf("ext message\n got %s\nwant %s", msg, want)
	}
	// prefix + nine fields.
	if n := strings.Count(msg, ":"); n != 9 {
		t.Errorf("message has %d separators, want 9: %q", n, msg)
	}
}

// A ticket from a prober with no designated list must stay byte-for-byte what
// it was, so the node stores and forwards it exactly as before.
func TestTicketJSONWithoutExt(t *testing.T) {
	b, err := json.Marshal(sampleTicket())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"kind", "params", "model", "sig_ext"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("%s written on a ticket without the extension: %s", k, b)
		}
	}
}
