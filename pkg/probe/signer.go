package probe

// signer.go — the key this prober signs faucet tickets with.
//
// WHICH KEY: not a configured one. The appointment already names the single
// address allowed to sign under it, and a keystore file carries its address in
// its name (geth's UTC--<time>--<address>), so the key is FOUND BY THE
// APPOINTMENT rather than pointed at.
//
// That removes a whole class of misconfiguration. With a configured path the
// failure is "a key opened, but it is the wrong one" — two things were set and
// they disagree. By address there is only one thing to be wrong about: either
// the key is in artifacts/keystores/ or it is not.
//
// Deliberately read-only: it opens a key an operator created elsewhere
// (`ivm account create`) and never generates, imports or writes one. A prober
// that could mint its own identity would blur who is answering for a ticket.
//
// WHY THE KEY IS OPENED NOW, BEFORE ANY TICKET IS SIGNED
//
// Firing probes is anonymous, so this milestone signs nothing. The key is
// opened anyway because it is how "this node cannot sign under its own
// appointment" gets caught at startup instead of surfacing much later as "the
// setup is done but nothing ever gets paid".
//
// A DEDICATED KEY, NOT NECESSARILY THE NODE WALLET
//
// Whatever key the appointment is bound to is the one used. Binding to a
// purpose-made key means a leak costs the prober role and nothing else; binding
// to the node's own wallet also works.

import (
	"crypto/ecdsa"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/keystore"
)

// SignerConfig is how the ticket-signing key is unlocked.
//
// There is no path here on purpose — see the file comment. KeystoresDir only
// exists for installs that keep keys somewhere unusual; the default is derived
// from where the mesh is running.
type SignerConfig struct {
	// Passphrase decrypts the keystore. PROBE_SIGNER_PASSPHRASE overrides it,
	// which is how an operator keeps the secret out of a config file — mesh
	// marks such keys `secret: true` and injects them as environment.
	Passphrase string `json:"passphrase,omitempty"`
	// KeystoresDir overrides the search directory. Empty = derived (see
	// DefaultKeystoresDir).
	KeystoresDir string `json:"keystores_dir,omitempty"`
}

// Signer is an opened ticket-signing key.
//
// PrivateKey must never reach a log line, an error string or a stored row.
// Address is the only part that leaves this struct.
type Signer struct {
	Address    string // lowercase 0x…
	PrivateKey *ecdsa.PrivateKey
}

const (
	signerPassphraseEnv = "PROBE_SIGNER_PASSPHRASE"
	keystoresDirEnv     = "PROBE_KEYSTORES_DIR"
)

// DefaultKeystoresDir locates <install_root>/artifacts/keystores.
//
// A mesh runs with its own folder as the working directory
// (<root>/artifacts/addon/meshes/<name>), so the root is found by walking up
// until an artifacts/keystores appears rather than by counting levels — the
// depth is a layout detail and counting it would break the day it changes.
func DefaultKeystoresDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "artifacts", "keystores")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// keystoresDir resolves the directory to search.
func keystoresDir(cfg SignerConfig) string {
	if v := strings.TrimSpace(os.Getenv(keystoresDirEnv)); v != "" {
		return v
	}
	if v := strings.TrimSpace(cfg.KeystoresDir); v != "" {
		return v
	}
	return DefaultKeystoresDir()
}

// OpenSignerFor unlocks the key the appointment is bound to.
//
// ok=false with a nil error means no passphrase was configured, which is
// allowed for now: this milestone only fires probes, and firing is anonymous.
// The cost is that the appointment's bind address goes unverified, which the
// caller reports.
//
// A MISSING KEY IS AN ERROR, not a silent skip: an appointment exists, so
// somebody meant this node to sign under it, and not being able to is a
// mistake worth stopping on.
func OpenSignerFor(appt Appointment, cfg SignerConfig) (Signer, bool, error) {
	pass := cfg.Passphrase
	if v := os.Getenv(signerPassphraseEnv); v != "" {
		pass = v
	}
	if pass == "" {
		return Signer{}, false, nil
	}
	if appt.Prober == "" {
		return Signer{}, false, fmt.Errorf("appointment binds no address, so no signing key can be selected")
	}

	dir := keystoresDir(cfg)
	if dir == "" {
		return Signer{}, false, fmt.Errorf("cannot locate artifacts/keystores (set %s)", keystoresDirEnv)
	}
	path := findKeystoreByAddress(dir, appt.Prober)
	if path == "" {
		return Signer{}, false, fmt.Errorf("no keystore for %s in %s — the appointment is bound to an address this node has no key for",
			appt.Prober, dir)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Signer{}, false, fmt.Errorf("read signer keystore: %w", err)
	}
	// The error says nothing about the passphrase or the file's contents: a
	// wrong passphrase and a corrupt file are both just "cannot decrypt",
	// since anything sharper helps whoever can read logs more than it helps
	// the operator.
	key, err := keystore.DecryptKey(data, pass)
	if err != nil {
		return Signer{}, false, fmt.Errorf("cannot decrypt %s (wrong passphrase or corrupt file)", filepath.Base(path))
	}

	addr := strings.ToLower(key.Address.Hex())
	// The file name is a label; the key inside is the fact. They can only
	// differ if a file was renamed, and signing as whoever the key actually is
	// while believing otherwise is exactly the failure this whole path exists
	// to prevent.
	if !strings.EqualFold(addr, appt.Prober) {
		return Signer{}, false, fmt.Errorf("keystore %s holds %s, not the appointed %s",
			filepath.Base(path), addr, appt.Prober)
	}
	return Signer{Address: addr, PrivateKey: key.PrivateKey}, true, nil
}

// findKeystoreByAddress returns the keystore in dir whose name carries addr.
// geth writes UTC--<time>--<address>, so a substring match on the hex is
// enough. Mirrors the RV's lookup for its voucher keys.
func findKeystoreByAddress(dir, addr string) string {
	needle := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(addr)), "0x")
	if needle == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.Contains(strings.ToLower(e.Name()), needle) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}
