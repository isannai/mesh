package probe

// bundle.go — the proof a prober hands the node it is about to check.
//
// It rides as ONE header on an otherwise completely ordinary inference request:
//
//	X-ISANN-Probe: base64url({epoch, n, members, probers, path, sig})
//
// A header rather than a body field, because the probe has to exercise the same
// path any other caller takes. A request whose BODY differs by caller stops
// measuring what an ordinary user would experience.
//
// The node reads it in two halves, and neither half works alone:
//
//	merkle     fold the leaf up the path and compare with the root the node
//	           already holds from its own register ack. Proves the group is the
//	           one the RV drew, not one we invented.
//	signature  recover the signer of ISANN-PROBE:<epoch>:<root>:<node>. Proves
//	           we are a prober NAMED in that group.
//
// 🔴 The bundle is PUBLIC. Every member of the group receives it, and we hand it
// over in the open. So holding one proves nothing, and the signature is the only
// thing separating us from anyone who has seen a probe go past.
//
// 🔴 THE MESSAGE NAMES THE RECIPIENT, and that field is not decoration. Without
// it the string we sign is identical for every node in the network for the whole
// slot. The first node we check could copy our signature and present it to a
// node we have not reached yet, spending that node's one free request and
// costing it the slot when we finally arrive.

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/isannai/mesh/pkg/auth"
	"github.com/isannai/mesh/pkg/faucet"
)

// ProbeHeader is the header name. Must match isannd's probeHeader exactly.
const ProbeHeader = "X-ISANN-Probe"

// probeBundle is the wire shape. Field names and types must match isannd's
// struct of the same name — this is a wire contract between two repos that do
// not import each other.
//
// Hex strings rather than raw bytes so an operator can diff a rejected bundle
// against `GET /v1/faucet/current` by eye. Base64 bytes would make the one
// diagnostic that matters unreadable.
type probeBundle struct {
	Epoch   int64    `json:"epoch"`
	N       int      `json:"n"`
	Members []string `json:"members"`
	Probers []string `json:"probers"`
	Path    []string `json:"path"`
	Sig     string   `json:"sig"`
}

// BuildProbeHeader signs a bundle for ONE target node.
//
// Called per shot, not per group, because the signature covers the recipient.
// The merkle half is identical for every member of a group; only the last field
// of the signed string changes, so the cost of re-signing is one ECDSA op.
func BuildProbeHeader(a Assignment, groupIndex int, target string, key *ecdsa.PrivateKey) (string, error) {
	if key == nil {
		return "", fmt.Errorf("no signing key: a prober cannot prove anything without one")
	}
	if groupIndex < 0 || groupIndex >= len(a.Groups) {
		return "", fmt.Errorf("group %d is not in this assignment", groupIndex)
	}
	targetAddr, err := faucet.ParseAddr(target)
	if err != nil {
		return "", fmt.Errorf("target %q is not an address: %w", target, err)
	}

	g := a.Groups[groupIndex]
	sig, err := auth.SignMessage(faucet.ProbeMessage(a.Epoch, a.Root32(), targetAddr), key)
	if err != nil {
		return "", fmt.Errorf("sign probe message: %w", err)
	}

	blob, err := json.Marshal(probeBundle{
		Epoch:   a.Epoch,
		N:       a.N,
		Members: lowerAll(g.Members),
		Probers: lowerAll(g.Probers),
		Path:    lowerAll(g.Path),
		Sig:     sig,
	})
	if err != nil {
		return "", fmt.Errorf("encode probe bundle: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(blob), nil
}

// lowerAll normalises hex casing.
//
// 🔴 Not cosmetic. The node re-hashes members and probers to rebuild the leaf,
// and it parses these strings into 20-byte values first — so casing cannot
// change the hash. But the RV renders lowercase, and shipping mixed case would
// make a rejected bundle look different from the RV's own response in exactly
// the diff an operator reaches for.
func lowerAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(strings.TrimSpace(s)))
	}
	return out
}
