package faucet

import (
	"bytes"

	"github.com/ethereum/go-ethereum/crypto"
)

// Domain tags. Every hash in this package is prefixed with one.
//
// Without them a sorted-pair tree is forgeable: an attacker presents an
// INTERNAL node as if it were a leaf, and since both were hashed the same way
// the proof still folds to the root. The tag makes the two preimage spaces
// disjoint, so no internal hash can ever be read as a leaf.
//
// 🔴 If all three implementations omit a tag identically they stay consistent
// with each other and no test in any repo notices. Only the committed golden
// vectors catch that, which is why they are part of the deliverable.
const (
	tagLeaf     = 0x00 // leaf preimage
	tagInternal = 0x01 // internal node preimage
	tagRoot     = 0x02 // final root preimage
	sepProbers  = 0xFF // separates the member column from the prober column
)

// LeafHash hashes one group.
//
//	keccak256(0x00 || epoch || sorted(members) || 0xFF || sorted(probers))
//
// Both address lists MUST be sorted. Sorting is what makes the leaf a function
// of the SET rather than of the order the caller happened to receive: the RV
// builds it from a shuffle, a node rebuilds it from what a prober handed over,
// and the voucher server rebuilds it from a claim submitted two weeks later.
//
// 0xFF separates the two columns. Without it, moving one address from the
// member column to the prober column leaves the concatenation unchanged, and
// a group where a checked node was recast as its own checker would hash to the
// same leaf.
//
// The function sorts defensively rather than trusting the caller, because the
// failure mode of an unsorted call is a wrong root, which surfaces as rejected
// claims and not as a crash.
func LeafHash(epoch int64, members, probers []Addr) Hash {
	m := append([]Addr(nil), members...)
	p := append([]Addr(nil), probers...)
	sortAddrs(m)
	sortAddrs(p)

	buf := make([]byte, 0, 1+8+len(m)*20+1+len(p)*20)
	buf = append(buf, tagLeaf)
	buf = append(buf, uint64be(uint64(epoch))...)
	for _, a := range m {
		buf = append(buf, a[:]...)
	}
	buf = append(buf, sepProbers)
	for _, a := range p {
		buf = append(buf, a[:]...)
	}
	return keccak(buf)
}

// PairHash folds two nodes, smaller one first.
//
//	keccak256(0x01 || min(a,b) || max(a,b))
//
// Sorting the pair drops the left/right distinction, and dropping it is what
// removes the group index from the wire: a proof no longer says WHERE a leaf
// sits, only THAT it is in the tree, which is all anyone needs to check. It
// also matches the shape of the widely used Solidity merkle libraries, so an
// on-chain verifier later needs no custom code.
func PairHash(a, b Hash) Hash {
	buf := make([]byte, 0, 1+64)
	buf = append(buf, tagInternal)
	if bytes.Compare(a[:], b[:]) <= 0 {
		buf = append(buf, a[:]...)
		buf = append(buf, b[:]...)
	} else {
		buf = append(buf, b[:]...)
		buf = append(buf, a[:]...)
	}
	return keccak(buf)
}

// BuildTree folds leaves into a merkle root and returns one proof per leaf.
//
// An odd node at any level is PROMOTED unpaired to the level above. The
// alternative — duplicating it so it can pair with itself — is the well known
// duplicate-leaf weakness, where a tree with an odd count accepts a proof for a
// leaf that was never in it.
//
// Returns the zero hash and nil for an empty input. Callers do not build empty
// slots (Assign returns no groups when there is nobody to check), so an empty
// tree means a bug upstream, and a zero root will never match a stored one.
func BuildTree(leaves []Hash) (Hash, [][]Hash) {
	if len(leaves) == 0 {
		return Hash{}, nil
	}
	paths := make([][]Hash, len(leaves))
	pos := make([]int, len(leaves))
	for i := range pos {
		pos[i] = i
	}

	cur := append([]Hash(nil), leaves...)
	for len(cur) > 1 {
		// Record each leaf's sibling at this level before collapsing it.
		for l := range paths {
			sib := pos[l] ^ 1
			if sib < len(cur) {
				paths[l] = append(paths[l], cur[sib])
			}
			pos[l] = pos[l] / 2
		}
		next := make([]Hash, 0, (len(cur)+1)/2)
		for i := 0; i < len(cur); i += 2 {
			if i+1 == len(cur) {
				next = append(next, cur[i]) // promoted, no sibling
				continue
			}
			next = append(next, PairHash(cur[i], cur[i+1]))
		}
		cur = next
	}
	return cur[0], paths
}

// Root binds the slot number and the node count to the merkle root.
//
//	keccak256(0x02 || epoch || N || merkleRoot)
//
// N is not inside any leaf, so without this step it would travel as an
// unauthenticated number. Today it is only displayed; the moment it feeds a
// payout rate it is money, and by then every root already published would have
// to be recomputed to add it. Folding it in now costs one hash.
//
// epoch is folded here as well as into every leaf: belt and braces against a
// proof from one slot being replayed against another slot's root.
func Root(epoch int64, n int, merkleRoot Hash) Hash {
	buf := make([]byte, 0, 1+8+8+32)
	buf = append(buf, tagRoot)
	buf = append(buf, uint64be(uint64(epoch))...)
	buf = append(buf, uint64be(uint64(n))...)
	buf = append(buf, merkleRoot[:]...)
	return keccak(buf)
}

// Verify checks that this group was part of the slot committed to by root.
//
// This is the function the node runs when a prober shows up, and the one the
// voucher server runs on a claim. Both are checking the same statement: these
// members and these probers were one group of an N-node partition at this
// epoch, and the RV published that partition as root.
//
// The caller must have obtained root from a trusted source. For a node that is
// the RV over its own control connection; for the voucher server it is its own
// roots table. Verifying a bundle against a root that came from the same party
// that supplied the bundle proves nothing.
func Verify(epoch int64, n int, members, probers []Addr, path []Hash, root Hash) bool {
	h := LeafHash(epoch, members, probers)
	for _, sib := range path {
		h = PairHash(h, sib)
	}
	return Root(epoch, n, h) == root
}

func keccak(parts ...[]byte) Hash {
	var h Hash
	copy(h[:], crypto.Keccak256(parts...))
	return h
}
