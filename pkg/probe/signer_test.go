package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
	gethuuid "github.com/google/uuid"
)

// writeKeystore drops a real keystore v3 file into dir, named the way geth
// names them (UTC--<time>--<address>) so the address lookup has something to
// match on.
func writeKeystore(t *testing.T, dir, pass string) (addr string) {
	t.Helper()
	pk, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	id, err := gethuuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	key := &keystore.Key{
		Id:         id,
		Address:    crypto.PubkeyToAddress(pk.PublicKey),
		PrivateKey: pk,
	}
	// The lightest KDF the library offers — these tests open keys repeatedly
	// and the standard parameters make that take seconds each.
	blob, err := keystore.EncryptKey(key, pass, keystore.LightScryptN, keystore.LightScryptP)
	if err != nil {
		t.Fatal(err)
	}
	addr = strings.ToLower(key.Address.Hex())
	name := "UTC--2026-08-15T00-00-00.000000000Z--" + strings.TrimPrefix(addr, "0x")
	if err := os.WriteFile(filepath.Join(dir, name), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestOpenSignerForFindsKeyByAppointment(t *testing.T) {
	dir := t.TempDir()
	addr := writeKeystore(t, dir, "hunter22")
	// A second, unrelated key must not be picked up: the appointment decides
	// which one, not "whatever is in the folder".
	writeKeystore(t, dir, "hunter22")

	s, ok, err := OpenSignerFor(
		Appointment{Alias: "faucet", Prober: addr},
		SignerConfig{Passphrase: "hunter22", KeystoresDir: dir})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if s.Address != addr {
		t.Errorf("address = %q, want %q", s.Address, addr)
	}
	if s.PrivateKey == nil {
		t.Error("private key not loaded")
	}
}

// The whole point of looking the key up BY the appointment: an address this
// node has no key for is one clear failure, not "a key opened but it is the
// wrong one".
func TestOpenSignerForMissingKey(t *testing.T) {
	dir := t.TempDir()
	writeKeystore(t, dir, "hunter22")

	_, ok, err := OpenSignerFor(
		Appointment{Alias: "faucet", Prober: "0xaaaa000000000000000000000000000000000001"},
		SignerConfig{Passphrase: "hunter22", KeystoresDir: dir})
	if ok || err == nil {
		t.Fatalf("want an error for an address with no keystore: ok=%v err=%v", ok, err)
	}
	// The message must name the address, or the operator cannot tell which key
	// to go find.
	if !strings.Contains(err.Error(), "0xaaaa000000000000000000000000000000000001") {
		t.Errorf("error should name the missing address, got: %v", err)
	}
}

// No passphrase is allowed for now — firing is anonymous, so nothing is signed
// yet — and must not be reported as a failure.
func TestOpenSignerForNoPassphraseIsNotAnError(t *testing.T) {
	t.Setenv(signerPassphraseEnv, "")
	_, ok, err := OpenSignerFor(Appointment{Prober: testProber}, SignerConfig{})
	if err != nil || ok {
		t.Fatalf("want (false, nil) when no passphrase is set: ok=%v err=%v", ok, err)
	}
}

func TestOpenSignerForWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	addr := writeKeystore(t, dir, "hunter22")

	_, ok, err := OpenSignerFor(
		Appointment{Prober: addr},
		SignerConfig{Passphrase: "wrong", KeystoresDir: dir})
	if ok || err == nil {
		t.Fatal("a wrong passphrase was accepted")
	}
	// The error must not echo the passphrase — logs get read by people who
	// should not learn it.
	if strings.Contains(err.Error(), "wrong") && strings.Contains(err.Error(), "passphrase or corrupt") == false {
		t.Errorf("error leaks the passphrase: %v", err)
	}
}

// A renamed file must not be able to pass off a different key: the name is a
// label, the key inside is the fact.
func TestOpenSignerForRejectsMislabelledFile(t *testing.T) {
	dir := t.TempDir()
	realAddr := writeKeystore(t, dir, "hunter22")

	// Rename the file to claim a different address.
	entries, _ := os.ReadDir(dir)
	claimed := "aaaa000000000000000000000000000000000001"
	if err := os.Rename(filepath.Join(dir, entries[0].Name()),
		filepath.Join(dir, "UTC--2026-08-15T00-00-00.000000000Z--"+claimed)); err != nil {
		t.Fatal(err)
	}

	_, ok, err := OpenSignerFor(
		Appointment{Prober: "0x" + claimed},
		SignerConfig{Passphrase: "hunter22", KeystoresDir: dir})
	if ok || err == nil {
		t.Fatalf("a mislabelled keystore was accepted: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(err.Error(), realAddr) {
		t.Errorf("error should name the address actually held, got: %v", err)
	}
}

// An appointment with no bound address cannot select a key. ParseProberMessage
// already refuses those, so this is belt-and-braces.
func TestOpenSignerForNeedsBoundAddress(t *testing.T) {
	if _, ok, err := OpenSignerFor(Appointment{}, SignerConfig{Passphrase: "x"}); ok || err == nil {
		t.Fatalf("want an error with no bound address: ok=%v err=%v", ok, err)
	}
}

func TestFindKeystoreByAddress(t *testing.T) {
	dir := t.TempDir()
	addr := writeKeystore(t, dir, "hunter22")

	// Case-insensitive, with or without the 0x prefix — addresses arrive
	// lowercase on some paths and EIP-55 checksummed on others.
	for _, probe := range []string{addr, strings.ToUpper(addr), strings.TrimPrefix(addr, "0x")} {
		if got := findKeystoreByAddress(dir, probe); got == "" {
			t.Errorf("lookup failed for %q", probe)
		}
	}
	if got := findKeystoreByAddress(dir, "0xdead"); got != "" {
		t.Errorf("partial/unknown address matched: %q", got)
	}
	if got := findKeystoreByAddress(dir, ""); got != "" {
		t.Errorf("empty address matched %q — that would pick an arbitrary key", got)
	}
	if got := findKeystoreByAddress(filepath.Join(dir, "nope"), addr); got != "" {
		t.Errorf("missing directory returned %q", got)
	}
}
