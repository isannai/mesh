package setup

// The verify half is tested with a SOFTWARE key. That is deliberate: the
// verifier is a plain server with no TPM, so a software key exercises exactly
// the code path it runs. The TPM half is covered by the round-trip test below,
// which skips where there is no chip.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"testing"
)

func softwareSigner(t *testing.T) (pubDER []byte, sign func([]byte) []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubDER, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return pubDER, func(nonce []byte) []byte {
		digest := sha256.Sum256(nonce)
		sig, serr := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if serr != nil {
			t.Fatalf("sign: %v", serr)
		}
		return sig
	}
}

func TestVerifyTPMChallenge(t *testing.T) {
	pubDER, sign := softwareSigner(t)
	nonce := []byte("a nonce the verifier picked")
	sig := sign(nonce)

	if err := VerifyTPMChallenge(pubDER, nonce, sig); err != nil {
		t.Fatalf("honest answer rejected: %v", err)
	}

	// 🔴 The nonce is the whole point. Without this case a replayed answer from
	// an earlier round would pass and the challenge would prove nothing.
	if err := VerifyTPMChallenge(pubDER, []byte("a different nonce"), sig); err == nil {
		t.Error("answer to a different nonce accepted")
	}

	// A signature from someone else's key must not pass, or the uniqueness rule
	// the verifier builds on top of this ("one key, one node") is decorative.
	otherPub, _ := softwareSigner(t)
	if err := VerifyTPMChallenge(otherPub, nonce, sig); err == nil {
		t.Error("answer verified against the wrong public key")
	}

	if err := VerifyTPMChallenge(pubDER, nonce, append([]byte{0}, sig...)); err == nil {
		t.Error("corrupted signature accepted")
	}
}

// Empty inputs are their own case: a verifier that treats "nothing supplied" as
// success would mark every node verified, including the ones with no TPM.
func TestVerifyTPMChallengeRejectsEmpty(t *testing.T) {
	pubDER, sign := softwareSigner(t)
	nonce := []byte("nonce")
	sig := sign(nonce)

	for _, c := range []struct {
		name            string
		pub, nonce, sig []byte
	}{
		{"no public key", nil, nonce, sig},
		{"no nonce", pubDER, nil, sig},
		{"no signature", pubDER, nonce, nil},
		{"nothing at all", nil, nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := VerifyTPMChallenge(c.pub, c.nonce, c.sig); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestVerifyTPMChallengeRejectsGarbagePublicKey(t *testing.T) {
	_, sign := softwareSigner(t)
	nonce := []byte("nonce")
	if err := VerifyTPMChallenge([]byte("not a DER key"), nonce, sign(nonce)); err == nil {
		t.Error("garbage public key accepted")
	}
}

// The real chip, when there is one. Skips elsewhere rather than failing: most
// machines running these tests have no TPM, and the verify half above is what
// carries the logic.
func TestTPMChallengeRoundTrip(t *testing.T) {
	pubDER, err := TPMChallengePublicKey()
	if err != nil {
		t.Skipf("no usable TPM here: %v", err)
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	sig, err := SignWithTPMKey(nonce)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := VerifyTPMChallenge(pubDER, nonce, sig); err != nil {
		t.Fatalf("round trip failed: %v", err)
	}

	// 🔴 The key must survive a second derivation, because the challenge is
	// issued in one exchange and answered in a later one. A key that drifted
	// would verify here and fail in production.
	again, err := TPMChallengePublicKey()
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if string(again) != string(pubDER) {
		t.Error("challenge key changed between calls")
	}
}

func TestSignWithTPMKeyRejectsEmptyNonce(t *testing.T) {
	if _, err := SignWithTPMKey(nil); err == nil {
		t.Error("signed an empty nonce")
	}
}

// tpmChallengeVector is the fixture that keeps the two repositories honest.
//
// 🔴 THE SAME BYTES LIVE IN BOTH. The signer is in the node repo (GLink) and
// the verifier in the rendezvous repo, and nothing at runtime reports that they
// have stopped agreeing — a drifted verifier simply marks honest nodes
// unverified, months later, with no error anywhere. So the contract is pinned
// here instead: RSASSA-PKCS1v15 over SHA-256 of the raw nonce, public key as
// PKIX DER.
//
// If this test goes red, the algorithm changed. Change it in BOTH places and
// regenerate the vector deliberately, never edit one side to make it pass.
var tpmChallengeVector = struct{ pubB64, nonce, sigB64 string }{
	pubB64: "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArXgtDBd0VJQIIbB6ErtvhpZGbbN76g2s/7/Z5et6+YUj4bWqtIIloC+OpVqK0H0WZt/znOq/NMrD4aB5vSWEH3N/qSdVLMn+wNMK1iRqUaWs+nYrbuUxCHeMZebXdDlCgNQG6veJh4cgDkn6ZALjrMLnmX5skNXFZYZIMLCatE0MTwxKz/RbyDWcEMFnjMhOyFiMG2pjXZkBUQ4olOpKHAnEUZ6CJqVe4DjoIhXDTJ0KiptRFtOHGoiqpSc35iPotGs8E9uuFRSHw9zcw/3WIwSIwHMKgB3PKIIiDAxC/k1TlK0a3C8uaiXe6RTi2RRjkz11dnIs9aCzWhDxm5wMSQIDAQAB",
	nonce:  "isann tpm challenge golden vector v1",
	sigB64: "o0r5aUERxhoUw9E4sPQtCaVnUVLX9z+XB/CL81z3Jl4ViSnKpfu13lGA6a3kzTElSW+wxdD9hmA9qEJ+tDRONuF3eUVd7h7zTA+orSRXvH693KORivJ9IJXfoljdYXTwOMZ8ajeAZ+ty9Qi/IM7MMXD15jLH+VMLIIrLiEKTASO23MGpMwPZDBDeQeKz5AB5XMiEZaOdBSOH24wHTZiJvJnAzQ0cJSn3vZlKW8EOncUEnRyDpkOOkFnO2Qk1cEVS/NhWDwPZS+eKw0G7m6hv1uOZ9eG9khs2nAySIX7vCMurK9HzeQIZjTNuGnDz1pJOcD2mRDt/ZZM74VHFzDEG9g==",
}

func TestVerifyTPMChallengeGoldenVector(t *testing.T) {
	pub, err := base64.StdEncoding.DecodeString(tpmChallengeVector.pubB64)
	if err != nil {
		t.Fatalf("vector public key: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(tpmChallengeVector.sigB64)
	if err != nil {
		t.Fatalf("vector signature: %v", err)
	}
	if err := VerifyTPMChallenge(pub, []byte(tpmChallengeVector.nonce), sig); err != nil {
		t.Fatalf("golden vector rejected — the challenge contract drifted: %v", err)
	}
}
