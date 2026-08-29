package faucet

// The signer here is a SOFTWARE RSA key, not a TPM. That is deliberate: this
// package must build and test in three repositories, only one of which ever
// touches a chip. What is under test is the format and the plumbing — that the
// same string is signed and verified, and that every field is covered.
//
// The real TPM path has its own round-trip test in pkg/setup.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"strings"
	"testing"
)

func testSigner(t *testing.T) (pubDER []byte, sign func([]byte) ([]byte, error)) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubDER, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return pubDER, func(msg []byte) ([]byte, error) {
		digest := sha256.Sum256(msg)
		return rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	}
}

func testVerify(pub, msg, sig []byte) error {
	parsed, err := x509.ParsePKIXPublicKey(pub)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(msg)
	return rsa.VerifyPKCS1v15(parsed.(*rsa.PublicKey), crypto.SHA256, digest[:], sig)
}

func sampleTicket() Ticket {
	var faucetAddr, prober, node, owner Addr
	var root Hash
	faucetAddr[19] = 0xfa
	prober[19] = 0x0b
	node[19] = 0x0d
	owner[19] = 0x00
	root[31] = 0xff
	return Ticket{ChainID: 1337, FaucetAddr: faucetAddr, Prober: prober, Node: node, Owner: owner, Root: root}
}

func TestTicketSignVerify(t *testing.T) {
	pub, sign := testSigner(t)
	tk := sampleTicket()

	if err := SignTicket(&tk, sign); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.HasPrefix(tk.Sig, "0x") {
		t.Errorf("signature is not 0x-hex: %q", tk.Sig)
	}
	if err := VerifyTicket(tk, pub, testVerify); err != nil {
		t.Fatalf("honest ticket rejected: %v", err)
	}
}

// 🔴 Every field must be covered by the signature. A field left out is a field
// an attacker can rewrite in flight — the payee most of all.
func TestTicketSignatureCoversEveryField(t *testing.T) {
	pub, sign := testSigner(t)
	base := sampleTicket()
	if err := SignTicket(&base, sign); err != nil {
		t.Fatalf("sign: %v", err)
	}

	for _, c := range []struct {
		name   string
		mutate func(*Ticket)
	}{
		{"chain id", func(x *Ticket) { x.ChainID = 1 }},
		{"faucet address", func(x *Ticket) { x.FaucetAddr[0] ^= 0xff }},
		{"prober", func(x *Ticket) { x.Prober[0] ^= 0xff }},
		{"node", func(x *Ticket) { x.Node[0] ^= 0xff }},
		{"owner — the payee", func(x *Ticket) { x.Owner[0] ^= 0xff }},
		{"root", func(x *Ticket) { x.Root[0] ^= 0xff }},
	} {
		t.Run(c.name, func(t *testing.T) {
			tampered := base
			c.mutate(&tampered)
			if err := VerifyTicket(tampered, pub, testVerify); err == nil {
				t.Error("tampered ticket verified")
			}
		})
	}
}

// The key comes from the verifier's own records, so presenting a ticket signed
// by some other chip must fail rather than fall back to anything.
func TestTicketRejectsForeignKey(t *testing.T) {
	_, sign := testSigner(t)
	otherPub, _ := testSigner(t)

	tk := sampleTicket()
	if err := SignTicket(&tk, sign); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := VerifyTicket(tk, otherPub, testVerify); err == nil {
		t.Error("verified against the wrong public key")
	}
}

func TestTicketRejectsMissingParts(t *testing.T) {
	pub, sign := testSigner(t)
	tk := sampleTicket()
	if err := SignTicket(&tk, sign); err != nil {
		t.Fatalf("sign: %v", err)
	}

	unsigned := sampleTicket()
	if err := VerifyTicket(unsigned, pub, testVerify); err == nil {
		t.Error("unsigned ticket accepted")
	}
	if err := VerifyTicket(tk, nil, testVerify); err == nil {
		t.Error("accepted with no public key")
	}

	bad := tk
	bad.Sig = "0xzz"
	if err := VerifyTicket(bad, pub, testVerify); err == nil {
		t.Error("non-hex signature accepted")
	}
}

// One prober, one node, one slot — one ticket. The key is what makes a second
// one a duplicate by definition, so it must change with each part and with
// nothing else.
func TestTicketKey(t *testing.T) {
	a := sampleTicket()
	if TicketKey(a) != TicketKey(a) {
		t.Fatal("key is not stable")
	}

	owner := a
	owner.Owner[0] ^= 0xff
	if TicketKey(owner) != TicketKey(a) {
		t.Error("payee changed the identity of the ticket")
	}

	for _, c := range []struct {
		name   string
		mutate func(*Ticket)
	}{
		{"prober", func(x *Ticket) { x.Prober[0] ^= 0xff }},
		{"node", func(x *Ticket) { x.Node[0] ^= 0xff }},
		{"root", func(x *Ticket) { x.Root[0] ^= 0xff }},
	} {
		t.Run(c.name, func(t *testing.T) {
			other := a
			c.mutate(&other)
			if TicketKey(other) == TicketKey(a) {
				t.Error("key did not change")
			}
		})
	}
}

func TestTicketMessageShape(t *testing.T) {
	tk := sampleTicket()
	msg := tk.Message()
	if !strings.HasPrefix(msg, TicketMessagePrefix) {
		t.Fatalf("missing prefix: %q", msg)
	}
	// prefix + six fields, and the prefix itself ends in a colon.
	if n := strings.Count(msg, ":"); n != 6 {
		t.Errorf("message has %d separators, want 6: %q", n, msg)
	}
	// The prefix is uppercase by convention; everything after it is lowercase
	// hex. A mixed-case address would sign a different string than the copy in
	// the next repository.
	body := strings.TrimPrefix(msg, TicketMessagePrefix)
	if strings.ToLower(body) != body {
		t.Errorf("fields must be lowercase hex: %q", body)
	}
}
