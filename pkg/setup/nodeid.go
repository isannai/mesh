package setup

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"

	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/hkdf"
)

// NodeIdentity represents a deterministic node identity derived from hardware.
type NodeIdentity struct {
	Address        string `json:"address"`                    // EOA: 0x...
	MainboardUUID  string `json:"mainboard_uuid"`             // System UUID
	TPMFingerprint string `json:"tpm_fingerprint,omitempty"`  // fTPM EK SHA256 (when available)
	GPUUID         string `json:"gpu_uuid,omitempty"`         // Fallback (when no TPM)
	EKCertRaw      []byte `json:"-"`                          // EK certificate DER (for TPM verification)
	EKCertIssuer   string `json:"ek_cert_issuer,omitempty"`   // EK cert issuer CN
	privateKey     *ecdsa.PrivateKey
}

// DeriveNodeIdentity derives a deterministic EOA address from hardware fingerprint.
//
// Priority:
//  1. fTPM EK fingerprint + Mainboard UUID
//  2. GPU UUID + Mainboard UUID (fallback if no TPM)
//
// The private key is re-derived each time from hardware — never stored on disk.
//
// 🔴 A MISSING INGREDIENT IS AN ERROR, NEVER AN EMPTY STRING.
//
// Both ingredients are read by shelling out to something the OS provides, and
// both readers answer "" when that something is gone. Folding "" into the HKDF
// still produces a perfectly good key — for a DIFFERENT address. On 2026-09-20
// a Windows update removed wmic, fetchMainboardUUID started returning "", and
// two dev nodes silently became other nodes:
//
//	derive("2859A1A8-CABB-…", ek) -> 0x0d60eb4d…   before the update
//	derive("",                ek) -> 0x2917031d…   after
//
// Everything keyed to the old address stopped matching at once (the RV's TPM
// key binding, its prober roster, the name on chain) and nothing said why, for
// three days. An address is this node's identity: refusing to produce one is
// loud and recoverable, inventing one is neither. See docs/issues.md WS-05.
//
// 🔴 KEEP THIS IDENTICAL TO glink pkg/setup — isannd re-signs the register
// frames station sends, so the two must derive the same address.
func DeriveNodeIdentity() (NodeIdentity, error) {
	mainboardUUID := fetchMainboardUUID()
	if mainboardUUID == "" {
		return NodeIdentity{}, fmt.Errorf(
			"cannot read the mainboard UUID, so this node's address cannot be derived%s", mainboardUUIDHint())
	}

	// Try fTPM first
	tpmInfo, tpmErr := ReadTPMInfo()
	if tpmErr == nil && tpmInfo.Fingerprint != "" {
		log.Printf("[nodeid] using fTPM EK + mainboard UUID")
		privKey, err := derivePrivateKey(mainboardUUID, tpmInfo.Fingerprint)
		if err != nil {
			return NodeIdentity{}, err
		}
		return NodeIdentity{
			Address:        crypto.PubkeyToAddress(privKey.PublicKey).Hex(),
			MainboardUUID:  mainboardUUID,
			TPMFingerprint: tpmInfo.Fingerprint,
			EKCertRaw:      tpmInfo.EKCertRaw,
			EKCertIssuer:   tpmInfo.EKCertIssuer,
			privateKey:     privKey,
		}, nil
	}

	// Fallback: GPU UUID
	log.Printf("[nodeid] fTPM not available (%v), using GPU UUID + mainboard UUID", tpmErr)
	gpuUUID := fetchGPUUUID()
	if gpuUUID == "" {
		// fetchGPUUUID says "no-gpu" for a machine without one, which is a real
		// (if weak) ingredient every GPU-less node shares. Empty means the probe
		// itself came back with nothing, and that is the silent-drift case again.
		return NodeIdentity{}, fmt.Errorf(
			"no TPM (%v) and the GPU probe returned nothing, so this node's address cannot be derived", tpmErr)
	}
	privKey, err := derivePrivateKey(mainboardUUID, gpuUUID)
	if err != nil {
		return NodeIdentity{}, err
	}

	return NodeIdentity{
		Address:       crypto.PubkeyToAddress(privKey.PublicKey).Hex(),
		MainboardUUID: mainboardUUID,
		GPUUID:        gpuUUID,
		privateKey:    privKey,
	}, nil
}

// Sign signs a message hash with the node's derived private key.
func (n *NodeIdentity) Sign(msgHash []byte) ([]byte, error) {
	return crypto.Sign(msgHash, n.privateKey)
}

// PrivateKeyHex returns the hex-encoded private key (use with caution).
func (n *NodeIdentity) PrivateKeyHex() string {
	return hex.EncodeToString(crypto.FromECDSA(n.privateKey))
}

// HasTPM returns true if the node identity was derived from fTPM.
func (n *NodeIdentity) HasTPM() bool {
	return n.TPMFingerprint != ""
}

func derivePrivateKey(part1, part2 string) (*ecdsa.PrivateKey, error) {
	secret := []byte(part1 + "|" + part2)
	salt := []byte("iann-node-v1")

	reader := hkdf.New(sha256.New, secret, salt, nil)
	privKeyBytes := make([]byte, 32)
	if _, err := io.ReadFull(reader, privKeyBytes); err != nil {
		return nil, err
	}

	return crypto.ToECDSA(privKeyBytes)
}
