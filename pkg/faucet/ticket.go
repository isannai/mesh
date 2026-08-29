package faucet

// ticket.go — the receipt a prober signs when a node passes a check.
//
//	ISANN-TICKET:<chainId>:<faucetAddr>:<prober>:<node>:<owner>:<root>
//
// Six fields, and each answers exactly one question:
//
//	chainId · faucetAddr   which deployment is this for
//	prober                 who says so
//	node                   what did the work
//	owner                  who gets paid
//	root                   when, and under whose assignment
//
// 🔴 prober is INSIDE the signature even though leaving it out would not be
// exploitable — a swapped signer field just makes the verifier reach for the
// wrong public key and fail. It is covered anyway because the de-duplication
// key is (prober, node, root), and a verifier should never have to argue about
// which unsigned fields are safe to trust. It also separates two probers whose
// tickets for the same node and slot would otherwise sign a byte-identical
// string.
//
// # WHY root CARRIES THE TIME
//
// An earlier shape signed an epoch, a timestamp, a per-day count and a nonce.
// All four collapse into the root:
//
//	epoch      folded into every leaf AND into the root (merkle.go) — a root
//	           names exactly one slot of one rendezvous
//	issued_at  the slot's start; a claim can only cover closed days (D18), so
//	           three-hour granularity loses nothing
//	count      one ticket per slot now, so the NUMBER of tickets is the count.
//	           It no longer has to sit inside the signature to resist editing
//	nonce      the key (prober, node, root) is already unique, so a random
//	           tag would only let one prober mint several tickets for one slot
//
// # WHAT THE SIGNATURE IS
//
// The prober signs with the key that lives inside its TPM — the same key and
// the same primitive the rendezvous challenge uses, so no new crypto enters
// here:
//
//	digest = SHA-256(message)
//	sig    = TPM2_Sign(challenge key, digest, RSASSA-PKCS1v15/SHA-256)
//
// 256 bytes for RSA-2048, carried as 0x-prefixed hex. NOT an Ethereum
// signature: there is no address to recover, so a verifier needs the public
// key. The rendezvous has it — it recorded the binding when the node answered
// its challenge — which is why a ticket carries no separate credential naming
// its signer.
//
// A wallet key is deliberately not used. It would mean a keystore file and a
// passphrase sitting in the prober's config, and a copy of that pair mints
// tickets in the node's name from anywhere. A TPM key cannot leave the chip.
//
// # WHY THE MERKLE BUNDLE IS NOT SIGNED
//
// It proves itself: the members and path either fold to this root or they do
// not. Signing self-verifying material again buys nothing and makes the message
// grow with the group.
//
// # WHAT A TICKET DOES AND DOES NOT PROVE
//
//	proven      this prober was assigned to this node in the slot that root
//	            names, and its chip signed the receipt
//	attested    the node answered correctly
//
// The second cannot be proven by anyone: grading happens on the prober, and
// even shipping the answer would not show the node generated it. That is why
// the operator chooses the probers, why several of them overlap on the same
// node, and why the rendezvous takes the MAXIMUM across probers, never a sum.

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// TicketMessagePrefix tags the string a prober signs. Same shape as
// ISANN-PROBE / ISANN-CREDENTIAL / ISANN-ACCESS so one convention covers every
// signed artifact on the wire.
const TicketMessagePrefix = "ISANN-TICKET:"

// Ticket is the ENVELOPE: the fields plus the signature over them. What gets
// signed is Message(), never this struct — the same split the other signed
// artifacts use, where "ISANN-XXX:…" is always the string and the envelope is
// something else (ianacc_ / ianprb_ tokens, or a JSON object as here).
//
// JSON rather than a single base64 token, unlike those two, because nobody
// copies a ticket by hand. It travels prober → node → rendezvous between
// programs only, and staying readable is worth more than being one blob.
//
// Sig sits alongside the fields rather than in a wrapper so a stored ticket and
// a transmitted one are the same object: the node saves what it received and
// later hands the same bytes on.
type Ticket struct {
	ChainID    uint64 `json:"chain_id"`
	FaucetAddr Addr   `json:"faucet_addr"`
	Prober     Addr   `json:"prober"`
	Node       Addr   `json:"node"`
	Owner      Addr   `json:"owner"`
	Root       Hash   `json:"root"`
	Sig        string `json:"sig"`
}

// TicketMessage builds the string a prober signs.
//
// chainId is decimal; the three addresses and the root are 0x-prefixed
// lowercase hex. Signer and verifier must produce byte-identical strings or
// the recovered address is a different one, so both sides call THIS function
// instead of formatting their own — the same rule the leaf encoding follows.
func TicketMessage(chainID uint64, faucetAddr, prober, node, owner Addr, root Hash) string {
	return TicketMessagePrefix +
		strconv.FormatUint(chainID, 10) + ":" +
		faucetAddr.Hex() + ":" +
		prober.Hex() + ":" +
		node.Hex() + ":" +
		owner.Hex() + ":" +
		root.Hex()
}

// Message is the string this ticket's signature covers.
func (t Ticket) Message() string {
	return TicketMessage(t.ChainID, t.FaucetAddr, t.Prober, t.Node, t.Owner, t.Root)
}

// TicketKey identifies a ticket for storage and de-duplication.
//
//	(prober, node, root)
//
// 🔴 This tuple is why the ticket needs no nonce and no per-day counter. One
// prober checking one node in one slot yields exactly one ticket, so a second
// one with the same key is a duplicate by definition — there is no honest
// reason to produce it. A random tag inside the ticket would break that: the
// same prober could mint several tickets for one slot and each would look
// distinct.
func TicketKey(t Ticket) string {
	return t.Prober.Hex() + "|" + t.Node.Hex() + "|" + t.Root.Hex()
}

// SignTicket fills in the signature, using a signer supplied by the caller.
//
// 🔴 The signer is injected rather than imported. This package is copied into
// three repositories and must stay free of TPM code — only the prober actually
// signs, while the node and the rendezvous merely verify. Passing the function
// in keeps the format definition portable and leaves the hardware dependency
// where it belongs.
//
// In the prober that argument is setup.AnswerTPMChallenge, which is the same
// key and the same primitive the rendezvous challenge uses.
func SignTicket(t *Ticket, sign func(msg []byte) ([]byte, error)) error {
	if t == nil {
		return errors.New("ticket: nil")
	}
	sig, err := sign([]byte(t.Message()))
	if err != nil {
		return fmt.Errorf("ticket: sign: %w", err)
	}
	if len(sig) == 0 {
		return errors.New("ticket: signer returned nothing")
	}
	t.Sig = "0x" + hex.EncodeToString(sig)
	return nil
}

// SigBytes decodes the signature.
func (t Ticket) SigBytes() ([]byte, error) {
	s := strings.TrimPrefix(t.Sig, "0x")
	if s == "" {
		return nil, errors.New("ticket: no signature")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("ticket: signature is not hex: %w", err)
	}
	return b, nil
}

// VerifyTicket checks the signature against the prober's public key.
//
// verify is injected for the same reason SignTicket's signer is; the node and
// the rendezvous both pass setup.VerifyTPMChallenge.
//
// The public key is NOT carried in the ticket. It comes from whoever is
// checking: the rendezvous looks up the binding it recorded when that prober
// answered its challenge. A key travelling with the thing it authenticates
// would prove nothing.
func VerifyTicket(t Ticket, pubDER []byte, verify func(pub, msg, sig []byte) error) error {
	sig, err := t.SigBytes()
	if err != nil {
		return err
	}
	if len(pubDER) == 0 {
		return errors.New("ticket: no public key for this prober")
	}
	return verify(pubDER, []byte(t.Message()), sig)
}
