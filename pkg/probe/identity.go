package probe

// identity.go — the key a prober signs ISANN-PROBE with.
//
// 🔴 THE NODE IDENTITY KEY, not a wallet key, and the split is deliberate
// (design G22):
//
//	probe request ISANN-PROBE:…   node identity key   one free inference
//	receipts, claims, payment     wallet key          money
//
// The node identity key is a FINGERPRINT. It is derived from the hardware, has
// no file on disk, cannot be backed up or moved, and becomes a different
// address if the mainboard is replaced. That makes it useless for holding value
// and exactly right for saying "this machine is that machine" — which is the
// entire claim a probe makes.
//
// It also removes a whole layer that used to sit here. The appointment existed
// because a prober needed to PROVE it was entitled to hand out free requests,
// with a keystore, a passphrase, an issuer signature and an expiry. None of
// that survives: the RV names its probers in faucet.json, the merkle root
// carries that list to every node, and this key proves we are one of them.
//
// The same key signs the register frame this node already sends, so nothing new
// is being trusted — only used for a second purpose.

import (
	"crypto/ecdsa"
	"fmt"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/isannai/mesh/pkg/setup"
)

var (
	nodeKeyOnce sync.Once
	nodeKey     *ecdsa.PrivateKey
	nodeKeyAddr string
	nodeKeyErr  error
)

// NodeSigningKey derives this machine's identity key.
//
// Cached because deriving it touches hardware (TPM, mainboard UUID, GPU) and a
// prober asks for it on every shot. The answer cannot change while the process
// lives: if the hardware changed, this is a different machine.
func NodeSigningKey() (*ecdsa.PrivateKey, string, error) {
	nodeKeyOnce.Do(func() {
		id, err := setup.DeriveNodeIdentity()
		if err != nil {
			nodeKeyErr = fmt.Errorf("derive node identity: %w", err)
			return
		}
		hex := strings.TrimPrefix(id.PrivateKeyHex(), "0x")
		if hex == "" {
			nodeKeyErr = fmt.Errorf("node identity carries no private key")
			return
		}
		key, err := crypto.HexToECDSA(hex)
		if err != nil {
			nodeKeyErr = fmt.Errorf("node identity key is unusable: %w", err)
			return
		}
		nodeKey = key
		nodeKeyAddr = strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

		// 🔴 The derived address must be the one the node registers under, or
		// the RV's prober list will never match us. They come from the same
		// derivation, so a mismatch means the two processes disagree about
		// what machine they are on — worth failing loudly rather than
		// signing as a stranger.
		if want := strings.ToLower(strings.TrimSpace(id.Address)); want != "" && want != nodeKeyAddr {
			nodeKeyErr = fmt.Errorf("node identity says %s but its key is %s", want, nodeKeyAddr)
			nodeKey, nodeKeyAddr = nil, ""
		}
	})
	return nodeKey, nodeKeyAddr, nodeKeyErr
}

// selfAddressOrEmpty is NodeSigningKey's address, or "" when it cannot be
// derived. Used where a missing answer only makes a set smaller — Refresh
// reports the failure properly.
func selfAddressOrEmpty() string {
	_, addr, err := NodeSigningKey()
	if err != nil {
		return ""
	}
	return addr
}
