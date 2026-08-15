package tunnel

import (
	"strings"
	"testing"
)

func TestProberMessageRoundTrip(t *testing.T) {
	const addr = "0xAbCdEf0123456789012345678901234567890123"
	msg := ProberMessage(addr, 1000, 2000)

	if !IsProberMessage(msg) {
		t.Fatalf("own output must be recognized: %q", msg)
	}
	if !strings.HasPrefix(msg, "ISANN-PROBER:") {
		t.Fatalf("want the prober prefix, got %q", msg)
	}

	prober, issued, expire, ok := ParseProberMessage(msg)
	if !ok {
		t.Fatalf("parse failed for %q", msg)
	}
	if prober != strings.ToLower(addr) {
		t.Errorf("address must round-trip lowercased, got %q", prober)
	}
	if issued != 1000 || expire != 2000 {
		t.Errorf("timestamps mangled: issued=%d expire=%d", issued, expire)
	}
}

// The three credential families share a shape, so the prefix is the only thing
// stopping one from being replayed as another. Verifiers parse by prefix, so a
// cross-accept here would let an RV admission credential act as a licence to
// mint faucet tickets.
func TestProberMessageRejectsOtherCredentialKinds(t *testing.T) {
	cred := CredentialMessage("0xaaa", 1, 2)
	access := AccessMessage("0xaaa", 1, 2)

	for _, m := range []string{cred, access} {
		if IsProberMessage(m) {
			t.Errorf("%q must not be seen as a prober appointment", m)
		}
		if _, _, _, ok := ParseProberMessage(m); ok {
			t.Errorf("%q must not parse as a prober appointment", m)
		}
	}
	// ...and the reverse: a prober appointment must not pass as an access grant.
	if IsAccessMessage(ProberMessage("0xaaa", 1, 2)) {
		t.Error("a prober appointment must not be seen as an access grant")
	}
}

// No bearer appointments. An appointment is handed to every node the prober
// touches, so it is effectively public — if an empty address parsed, one leaked
// blob would let anyone mint faucet tickets network-wide.
func TestProberMessageRefusesBearer(t *testing.T) {
	for _, bad := range []string{
		ProberMessage("", 1, 2),
		ProberMessage("   ", 1, 2),
		"ISANN-PROBER::1:2",
	} {
		if _, _, _, ok := ParseProberMessage(bad); ok {
			t.Errorf("bearer appointment must be refused: %q", bad)
		}
	}
}

func TestParseProberMessageRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"ISANN-PROBER:",
		"ISANN-PROBER:0xaaa",           // missing timestamps
		"ISANN-PROBER:0xaaa:1",         // missing expire
		"ISANN-PROBER:0xaaa:1:2:3",     // too many fields
		"ISANN-PROBER:0xaaa:notanum:2", // issued not a number
		"ISANN-PROBER:0xaaa:1:notanum", // expire not a number
		"ISANN-CREDENTIAL:0xaaa:1:2",   // wrong prefix
	}
	for _, m := range bad {
		if _, _, _, ok := ParseProberMessage(m); ok {
			t.Errorf("must not parse: %q", m)
		}
	}
}

func TestProberTokenRoundTrip(t *testing.T) {
	msg := ProberMessage("0xaaa", 1, 2)
	const sig = "deadbeef"

	tok := EncodeProberToken(msg, sig)
	if !strings.HasPrefix(tok, ProberTokenPrefix) {
		t.Fatalf("want the %q prefix, got %q", ProberTokenPrefix, tok)
	}

	gotMsg, gotSig, err := DecodeProberToken(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotMsg != msg || gotSig != sig {
		t.Errorf("round-trip mismatch: msg=%q sig=%q", gotMsg, gotSig)
	}
}

func TestDecodeProberTokenRejectsBadInput(t *testing.T) {
	// An access token must not decode as a prober appointment — the token
	// prefixes are the outer guard, matching the message-prefix guard above.
	accessTok := EncodeAccessToken(AccessMessage("0xaaa", 1, 2), "beef")

	for name, tok := range map[string]string{
		"empty":        "",
		"no_prefix":    "just-some-string",
		"access_token": accessTok,
		"bad_base64":   ProberTokenPrefix + "!!!not-base64!!!",
		"not_json":     ProberTokenPrefix + "aGVsbG8", // base64("hello")
	} {
		if _, _, err := DecodeProberToken(tok); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}
