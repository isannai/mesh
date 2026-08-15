package tunnel

// prober.go — faucet prober appointment: the issuer-signed statement that one
// address may hand out faucet tickets for a while. Third member of the same
// family as the RV admission credential (rendezvous.go) and the inference
// access grant (access.go) — SAME three fields, only the prefix and the
// verifying party differ.
//
// Why an appointment instead of a prober allowlist on every node: a node has to
// be able to check that a ticket came from a legitimate prober, but shipping a
// list to every node makes revocation impractical (you would have to reach them
// all). Instead the prober carries proof, the node checks it against the ONE
// issuer address its RV vouches for, and revocation is the appointment expiring.
// Short lifetimes are the point, not a limitation.
//
// Two layers, kept apart (same split as access.go):
//   - ProberMessage = the canonical string the issuer SIGNS
//     "ISANN-PROBER:<prober>:<issued>:<expire>"
//   - Prober token  = "ianprb_<base64url(json)>", one copy-paste blob bundling
//     message + signature, so an operator drops a single value into the probe
//     node's config instead of three separate fields.
//
// Verification (node side): recover the issuer from the signature, require it to
// be one the RV vouches for, require now within [issued, expire], and require
// the TICKET's signer to equal <prober>. That last check is what makes a leaked
// appointment useless — see below.
//
// No new crypto: reuses auth.SignMessage / auth.RecoverAddress.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// proberPrefix marks a prober appointment. Distinct from "ISANN-CREDENTIAL:"
// and "ISANN-ACCESS:" so an appointment can never be replayed as an RV
// admission credential or an inference grant, and vice versa.
const proberPrefix = "ISANN-PROBER:"

// ProberTokenPrefix marks the single-blob copy-paste token form.
const ProberTokenPrefix = "ianprb_"

// ProberMessage is the canonical string an issuer signs to appoint a prober.
//
//	ISANN-PROBER:<prober>:<issued>:<expire>
//
//   - prober = the address that will SIGN faucet tickets. REQUIRED — unlike
//     CredentialMessage and AccessMessage, there is no bearer form. An
//     appointment is copied to every node a prober touches, so it is effectively
//     public; binding it to an address is what stops a copy from being usable by
//     anyone else. A bearer appointment would let one leaked blob mint tickets
//     network-wide, which is the whole risk this design exists to bound.
//   - issued/expire = the appointment window. Expiry IS the revocation
//     mechanism, so it is meant to be short (hours to days, not months).
//
// No target-node field: an appointment says WHO may issue tickets, not WHOM they
// may check, so the same one is handed to every node — exactly as RV's
// CredentialMessage works on whichever RV trusts the issuer.
//
//	Issuer side: sig = auth.SignMessage(ProberMessage(prober, issued, expire), issuerPK)
//	Node side:   issuer = auth.RecoverAddress(ProberMessage(prober, issued, expire), sig)
func ProberMessage(proberAddr string, issuedMs, expireMs int64) string {
	return proberPrefix + strings.ToLower(strings.TrimSpace(proberAddr)) + ":" +
		strconv.FormatInt(issuedMs, 10) + ":" + strconv.FormatInt(expireMs, 10)
}

// IsProberMessage reports whether msg carries the prober-appointment prefix.
func IsProberMessage(msg string) bool {
	return strings.HasPrefix(msg, proberPrefix)
}

// ParseProberMessage extracts (prober, issuedMs, expireMs) from an appointment.
// ok=false when the prefix or shape is wrong, OR when prober is empty — callers
// MUST treat that as "deny".
//
// The empty-prober rejection lives here rather than at each call site so no
// verifier can accidentally accept a bearer appointment: getting that wrong once
// would make every leaked blob a network-wide ticket mint.
func ParseProberMessage(msg string) (prober string, issuedMs, expireMs int64, ok bool) {
	if !strings.HasPrefix(msg, proberPrefix) {
		return "", 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(msg, proberPrefix), ":")
	if len(parts) != 3 {
		return "", 0, 0, false
	}
	prober = strings.ToLower(strings.TrimSpace(parts[0]))
	if prober == "" {
		return "", 0, 0, false // no bearer appointments
	}
	issuedMs, err1 := strconv.ParseInt(parts[1], 10, 64)
	expireMs, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		return "", 0, 0, false
	}
	return prober, issuedMs, expireMs, true
}

// proberTokenPayload is the JSON bundled inside a prober token.
type proberTokenPayload struct {
	Msg string `json:"m"` // the signed ProberMessage
	Sig string `json:"s"` // its signature (hex)
}

// EncodeProberToken bundles a signed appointment + signature into the single
// copy-paste token "ianprb_<base64url(json)>".
func EncodeProberToken(message, sig string) string {
	b, _ := json.Marshal(proberTokenPayload{Msg: message, Sig: sig})
	return ProberTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// DecodeProberToken reverses EncodeProberToken, returning the signed
// appointment and its signature.
func DecodeProberToken(token string) (message, sig string, err error) {
	s := strings.TrimSpace(token)
	if !strings.HasPrefix(s, ProberTokenPrefix) {
		return "", "", fmt.Errorf("not a prober token (missing %q prefix)", ProberTokenPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, ProberTokenPrefix))
	if err != nil {
		return "", "", fmt.Errorf("decode prober token: %w", err)
	}
	var p proberTokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", fmt.Errorf("parse prober token: %w", err)
	}
	if p.Msg == "" || p.Sig == "" {
		return "", "", fmt.Errorf("prober token missing message or signature")
	}
	return p.Msg, p.Sig, nil
}
