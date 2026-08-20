// Package faucet computes the per-slot group assignment that the faucet is
// built on: who is in which group, which probers check them, and the merkle
// root that freezes the whole partition into 32 bytes.
//
// EVERY FUNCTION HERE IS PURE. Given the same (nodes, probers, epoch, conf) it
// returns the same groups and the same root, on any machine, forever. That is
// not a style preference — it is what lets the RV hand out a root, a node keep
// a proof for two weeks, and the voucher server verify that proof later without
// any of the three sharing state.
//
// # THREE IMPLEMENTATIONS MUST AGREE
//
// This package is copied, not imported, into GLink (the node) and later into
// the voucher server: isann-servers does not depend on GLink and vice versa.
// So the encoding rules below are a WIRE FORMAT, not an implementation detail.
// If one copy sorts differently, drops a domain tag, or encodes an integer in
// another width, its roots silently diverge and every claim it verifies is
// rejected. testdata/vectors.json exists so that divergence fails a test in
// each repo instead of failing money in production.
//
// # THE RULES, IN ONE PLACE
//
//	hash            keccak256
//	slot            epoch = floor(unix / 10800)          3 hours
//	group count     k = max(1, floor(N / NumOfNode))     N counts nodes only
//	group sizes     base, rem = N/k, N%k. remainder goes one each from the front
//	node order      key = keccak256(epoch || addr), ascending, ties by address
//	prober order    same rule. NEVER the order written in the config file
//	probers/group   m = min(NumOfProber, P)
//	group i probers order_p[(i*m + j) mod P] for j in 0..m-1
//	leaf            keccak256(0x00 || epoch || sorted(members) || 0xFF || sorted(probers))
//	internal        keccak256(0x01 || min(a,b) || max(a,b))    sorted pair
//	odd leaf        promoted unpaired. NEVER duplicated
//	root            keccak256(0x02 || epoch || N || merkleRoot)
//	address         lowercase 20 raw bytes
//	integers        8 bytes big-endian
//
// See docs/TODO/20260818/README.md in GLink for the reasoning behind each.
package faucet

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

// SlotSeconds is the slot length. Three hours, and it has to divide 24 evenly
// so that day = floor(epoch/8) lines up with a UTC date without timezone code.
const SlotSeconds = 10800

// Addr is a node identity: 20 raw bytes, no 0x, no checksum casing.
//
// Deliberately NOT go-ethereum's common.Address. That type's String() returns
// EIP-55 mixed case, and a mixed-case address hashed into a leaf produces a
// different root than the lowercase one the next implementation uses. Having
// our own type means the only way to render one is Hex(), which is lowercase.
type Addr [20]byte

// Hash is a keccak256 output.
type Hash [32]byte

// Conf is the operator's group shape, from faucet.json.
type Conf struct {
	// NumOfNode is the target inference-node count per group. Probers are
	// added on top, so a group of 16 with 3 probers has 19 members.
	NumOfNode int
	// NumOfProber is how many probers to attach to each group.
	NumOfProber int
}

// Group is one leaf: the nodes checked in this slot and who checks them.
//
// Both slices are sorted ascending, because that is what goes into the leaf
// hash. Storing them any other way would invite a caller to hash them in
// arrival order.
type Group struct {
	Members []Addr
	Probers []Addr
}

// Assignment is a whole slot: the partition, its root, and one path per group.
type Assignment struct {
	Epoch  int64
	N      int
	Groups []Group
	Root   Hash
	Paths  [][]Hash
}

// Epoch returns the slot number containing unixSec, at the production length.
func Epoch(unixSec int64) int64 { return EpochAt(unixSec, SlotSeconds) }

// SlotStart returns the unix second at which epoch begins.
func SlotStart(epoch int64) int64 { return SlotStartAt(epoch, SlotSeconds) }

// EpochAt is Epoch with an explicit slot length, for a test network that cannot
// wait three hours to see a round.
//
// Floor division, not Go's truncation, so the function stays monotonic if it is
// ever handed a pre-1970 timestamp from a machine with a broken clock.
//
// 🔴 slotSec is not a local choice. A node re-derives the slot number from its
// own clock to decide whether an assignment has gone stale, so two sides
// running different lengths disagree about which slot it is and every delivery
// is discarded on arrival.
func EpochAt(unixSec int64, slotSec int) int64 {
	if slotSec <= 0 {
		slotSec = SlotSeconds
	}
	d := int64(slotSec)
	if unixSec < 0 {
		return -((-unixSec + d - 1) / d)
	}
	return unixSec / d
}

// SlotStartAt is SlotStart with an explicit slot length.
func SlotStartAt(epoch int64, slotSec int) int64 {
	if slotSec <= 0 {
		slotSec = SlotSeconds
	}
	return epoch * int64(slotSec)
}

// GroupSizes splits n nodes into groups of at least numOfNode each.
//
// The count rounds DOWN, which is the whole point: a remainder never becomes a
// short group of its own, it is spread one node at a time across the groups
// that already exist. So sizes are always >= numOfNode (up to 2*numOfNode-1
// when there is only one group), never below it.
//
//	n=16 -> [16]        n=31 -> [31]
//	n=62 -> [21 21 20]  n=100 -> [17 17 17 17 16 16]
//
// Rounding UP was the earlier rule and it produced groups smaller than the
// target, which only inflated how many probers the network needed.
func GroupSizes(n, numOfNode int) []int {
	if n <= 0 {
		return nil
	}
	if numOfNode < 1 {
		numOfNode = 1
	}
	k := n / numOfNode
	if k < 1 {
		k = 1
	}
	base, rem := n/k, n%k
	out := make([]int, k)
	for i := range out {
		out[i] = base
		if i < rem {
			out[i]++
		}
	}
	return out
}

// Order shuffles addrs deterministically for this epoch.
//
// It is a SORT, not a Fisher-Yates shuffle, and that is deliberate. A shuffle
// reproduces only if every implementation also reproduces the random source
// bit for bit, which means specifying a PRNG across three languages. Sorting by
// keccak256(epoch || addr) needs no such agreement: anyone with the address set
// and the epoch derives the same order.
//
// Ties break on the address so the result is total even in the (practically
// impossible) case of a key collision.
//
// The input is not mutated.
func Order(addrs []Addr, epoch int64) []Addr {
	out := make([]Addr, len(addrs))
	copy(out, addrs)
	keys := make(map[Addr]Hash, len(addrs))
	for _, a := range out {
		keys[a] = keccak(uint64be(uint64(epoch)), a[:])
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := keys[out[i]], keys[out[j]]
		if c := bytes.Compare(ki[:], kj[:]); c != 0 {
			return c < 0
		}
		return bytes.Compare(out[i][:], out[j][:]) < 0
	})
	return out
}

// Assign partitions nodes into groups and attaches probers to each.
//
// Probers are dealt round-robin from their own shuffled order and MAY repeat
// across groups: with two probers and three groups, one of them covers two.
// Capping the group count at P/NumOfProber instead would collapse a small
// network into a single group, and a single group means every node receives
// every other node's address in its proof.
//
// Within one group the probers are always distinct, because m consecutive
// indices mod P cannot repeat while m <= P. That matters: the receipt quorum
// counts DISTINCT probers, so a duplicate would let one prober satisfy it alone.
//
// A prober IS ALSO a checked node. It runs the same engines as everyone else,
// so leaving it out of the member pool would exclude a working machine from
// earning for the slots it happens to be picked. It simply never checks itself:
// a prober signing for a machine it controls measures nothing, so the checking
// side skips its own address and that node earns only when another prober in
// its group covers it.
//
// Inputs are deduplicated rather than trusted, because a repeated address would
// corrupt the tree in a way that only shows up as rejected claims weeks later.
// Returns nil when there is no one to check, or no one to check them.
func Assign(nodes, probers []Addr, epoch int64, cfg Conf) []Group {
	probers = dedupe(probers)
	nodes = dedupe(nodes)

	p := len(probers)
	if p == 0 || len(nodes) == 0 {
		return nil
	}
	m := cfg.NumOfProber
	if m < 1 {
		m = 1
	}
	if m > p {
		m = p
	}

	sizes := GroupSizes(len(nodes), cfg.NumOfNode)
	orderN := Order(nodes, epoch)
	orderP := Order(probers, epoch)

	out := make([]Group, len(sizes))
	at := 0
	for i, size := range sizes {
		members := make([]Addr, size)
		copy(members, orderN[at:at+size])
		at += size
		sortAddrs(members)

		assigned := make([]Addr, m)
		for j := 0; j < m; j++ {
			assigned[j] = orderP[(i*m+j)%p]
		}
		sortAddrs(assigned)

		out[i] = Group{Members: members, Probers: assigned}
	}
	return out
}

// Build produces everything the RV needs for one slot.
//
// N counts the nodes that ended up in a group, NOT the input length: duplicates
// are collapsed, and N is folded into the root, so it has to be the number the
// tree was actually built from.
func Build(nodes, probers []Addr, epoch int64, cfg Conf) Assignment {
	groups := Assign(nodes, probers, epoch, cfg)
	if len(groups) == 0 {
		return Assignment{Epoch: epoch}
	}
	n := 0
	leaves := make([]Hash, len(groups))
	for i, g := range groups {
		n += len(g.Members)
		leaves[i] = LeafHash(epoch, g.Members, g.Probers)
	}
	merkleRoot, paths := BuildTree(leaves)
	return Assignment{
		Epoch:  epoch,
		N:      n,
		Groups: groups,
		Root:   Root(epoch, n, merkleRoot),
		Paths:  paths,
	}
}

// GroupOf returns the index of the group containing addr, or -1.
func (a Assignment) GroupOf(addr Addr) int {
	for i, g := range a.Groups {
		for _, m := range g.Members {
			if m == addr {
				return i
			}
		}
		for _, p := range g.Probers {
			if p == addr {
				return i
			}
		}
	}
	return -1
}

// Hex renders the address as lowercase 0x hex.
func (a Addr) Hex() string { return "0x" + hex.EncodeToString(a[:]) }

// Hex renders the hash as lowercase 0x hex.
func (h Hash) Hex() string { return "0x" + hex.EncodeToString(h[:]) }

// ParseAddr accepts an address with or without 0x, in any casing.
//
// Casing is accepted on the way IN (operators paste checksummed addresses) and
// discarded immediately, so nothing downstream can hash a mixed-case address.
func ParseAddr(s string) (Addr, error) {
	var a Addr
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "0x")
	if len(s) != 40 {
		return a, errors.New("faucet: address must be 20 bytes of hex")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return a, errors.New("faucet: address is not hex")
	}
	copy(a[:], b)
	return a, nil
}

// ParseHash accepts a 32-byte hash with or without 0x.
func ParseHash(s string) (Hash, error) {
	var h Hash
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "0x")
	if len(s) != 64 {
		return h, errors.New("faucet: hash must be 32 bytes of hex")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return h, errors.New("faucet: hash is not hex")
	}
	copy(h[:], b)
	return h, nil
}

// PackAddrs concatenates addresses for storage (the current-slot snapshot row).
func PackAddrs(addrs []Addr) []byte {
	out := make([]byte, 0, len(addrs)*20)
	for _, a := range addrs {
		out = append(out, a[:]...)
	}
	return out
}

// UnpackAddrs is the inverse of PackAddrs.
func UnpackAddrs(b []byte) ([]Addr, error) {
	if len(b)%20 != 0 {
		return nil, errors.New("faucet: packed addresses must be a multiple of 20 bytes")
	}
	out := make([]Addr, len(b)/20)
	for i := range out {
		copy(out[i][:], b[i*20:(i+1)*20])
	}
	return out, nil
}

func sortAddrs(a []Addr) {
	sort.Slice(a, func(i, j int) bool { return bytes.Compare(a[i][:], a[j][:]) < 0 })
}

func dedupe(in []Addr) []Addr {
	seen := make(map[Addr]struct{}, len(in))
	out := make([]Addr, 0, len(in))
	for _, a := range in {
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}

func uint64be(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}
