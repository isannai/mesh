package faucet

import (
	"math/rand"
	"testing"
)

// testAddr derives a deterministic address for index i. Deriving rather than
// hard-coding means the golden vectors can be regenerated in any language from
// the same one-line rule.
func testAddr(i int) Addr {
	h := keccak([]byte("faucet-test-node"), uint64be(uint64(i)))
	var a Addr
	copy(a[:], h[:20])
	return a
}

func testAddrs(n int) []Addr {
	out := make([]Addr, n)
	for i := range out {
		out[i] = testAddr(i)
	}
	return out
}

func testProbers(n int) []Addr {
	out := make([]Addr, n)
	for i := range out {
		h := keccak([]byte("faucet-test-prober"), uint64be(uint64(i)))
		copy(out[i][:], h[:20])
	}
	return out
}

func TestEpoch(t *testing.T) {
	cases := []struct {
		sec  int64
		want int64
	}{
		{0, 0},
		{SlotSeconds - 1, 0},
		{SlotSeconds, 1},
		{1755475200, 1755475200 / SlotSeconds},
	}
	for _, c := range cases {
		if got := Epoch(c.sec); got != c.want {
			t.Errorf("Epoch(%d) = %d, want %d", c.sec, got, c.want)
		}
	}
	// A slot must start on its own boundary, or day = floor(epoch/8) stops
	// lining up with a UTC date.
	for _, e := range []int64{0, 1, 165464} {
		if got := Epoch(SlotStart(e)); got != e {
			t.Errorf("Epoch(SlotStart(%d)) = %d", e, got)
		}
	}
}

func TestGroupSizes(t *testing.T) {
	cases := []struct {
		n, numOfNode int
		want         []int
	}{
		{0, 16, nil},
		{1, 16, []int{1}},
		{15, 16, []int{15}},
		{16, 16, []int{16}},
		{17, 16, []int{17}},
		{31, 16, []int{31}},     // one group right up to 2*target-1
		{32, 16, []int{16, 16}}, // first split
		{62, 16, []int{21, 21, 20}},
		{100, 16, []int{17, 17, 17, 17, 16, 16}},
	}
	for _, c := range cases {
		got := GroupSizes(c.n, c.numOfNode)
		if len(got) != len(c.want) {
			t.Fatalf("GroupSizes(%d,%d) = %v, want %v", c.n, c.numOfNode, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("GroupSizes(%d,%d) = %v, want %v", c.n, c.numOfNode, got, c.want)
			}
		}
	}
}

// The two properties the split exists for: nothing is lost, and no group ends
// up under the target. A short group would have fewer nodes than the operator
// asked for while still consuming a full prober slot.
func TestGroupSizesInvariants(t *testing.T) {
	for _, numOfNode := range []int{1, 4, 16, 50} {
		for n := 1; n <= 400; n++ {
			sizes := GroupSizes(n, numOfNode)
			sum, min, max := 0, sizes[0], sizes[0]
			for _, s := range sizes {
				sum += s
				if s < min {
					min = s
				}
				if s > max {
					max = s
				}
			}
			if sum != n {
				t.Fatalf("n=%d target=%d: sizes %v sum to %d", n, numOfNode, sizes, sum)
			}
			if max-min > 1 {
				t.Fatalf("n=%d target=%d: sizes %v differ by more than 1", n, numOfNode, sizes)
			}
			if n >= numOfNode && min < numOfNode {
				t.Fatalf("n=%d target=%d: sizes %v went below the target", n, numOfNode, sizes)
			}
		}
	}
}

// Order must depend on the address set and the epoch, and on nothing else.
// If it depended on the order the caller supplied, the config file's line
// order would become a hidden input to the root.
func TestOrderIgnoresInputOrder(t *testing.T) {
	base := testAddrs(40)
	want := Order(base, 165464)

	shuffled := append([]Addr(nil), base...)
	rng := rand.New(rand.NewSource(7))
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	got := Order(shuffled, 165464)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Order depends on input order at %d", i)
		}
	}
	if base[0] != testAddr(0) {
		t.Fatal("Order mutated its input")
	}
}

func TestOrderRotatesEachSlot(t *testing.T) {
	a := Order(testAddrs(40), 165464)
	b := Order(testAddrs(40), 165465)
	same := true
	for i := range a {
		if a[i] != b[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("order did not change between slots: groups would never reshuffle")
	}
}

// m = min(NumOfProber, P). Without the clamp, a group would list the same
// prober twice and one prober alone could satisfy a quorum that counts
// DISTINCT signers.
func TestAssignProbersDistinctWithinGroup(t *testing.T) {
	groups := Assign(testAddrs(62), testProbers(2), 165464, Conf{NumOfNode: 16, NumOfProber: 5})
	if len(groups) == 0 {
		t.Fatal("no groups")
	}
	for i, g := range groups {
		if len(g.Probers) != 2 {
			t.Fatalf("group %d: %d probers, want clamp to 2", i, len(g.Probers))
		}
		if g.Probers[0] == g.Probers[1] {
			t.Fatalf("group %d: duplicate prober", i)
		}
	}
}

// With fewer probers than groups one prober covers several groups. Capping the
// group count instead would collapse a small network into a single group, and
// then every node's proof would carry every other node's address.
func TestAssignProberReuseAcrossGroups(t *testing.T) {
	groups := Assign(testAddrs(62), testProbers(2), 165464, Conf{NumOfNode: 16, NumOfProber: 1})
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(groups))
	}
	seen := map[Addr]int{}
	for _, g := range groups {
		if len(g.Probers) != 1 {
			t.Fatalf("want 1 prober per group, got %d", len(g.Probers))
		}
		seen[g.Probers[0]]++
	}
	if len(seen) != 2 {
		t.Fatalf("expected both probers used, got %d", len(seen))
	}
}

// A prober is an ordinary node the rest of the time, so it is checked like one
// and earns like one. Leaving it out would exclude a working machine from the
// slots it happens to be picked for.
func TestAssignKeepsProbersAsMembers(t *testing.T) {
	nodes := testAddrs(20)
	probers := []Addr{nodes[0], nodes[1]}
	groups := Assign(nodes, probers, 165464, Conf{NumOfNode: 16, NumOfProber: 1})

	n, found := 0, map[Addr]bool{}
	for _, g := range groups {
		n += len(g.Members)
		for _, m := range g.Members {
			found[m] = true
		}
	}
	if n != 20 {
		t.Fatalf("N = %d, want all 20 nodes counted", n)
	}
	if !found[probers[0]] || !found[probers[1]] {
		t.Fatal("a prober was dropped from the member pool")
	}
}

func TestAssignEmptyInputs(t *testing.T) {
	if g := Assign(testAddrs(10), nil, 165464, Conf{NumOfNode: 16, NumOfProber: 1}); g != nil {
		t.Fatal("no probers must mean no slot: nobody would check anyone")
	}
	if g := Assign(nil, testProbers(2), 165464, Conf{NumOfNode: 16, NumOfProber: 1}); g != nil {
		t.Fatal("no nodes must mean no groups")
	}
}

// The round trip the whole design rests on: what the RV builds is what a node
// or the voucher server can check from the group contents alone.
func TestBuildVerifyRoundTrip(t *testing.T) {
	for _, n := range []int{1, 16, 17, 31, 32, 62, 100, 257} {
		a := Build(testAddrs(n), testProbers(3), int64(165464), Conf{NumOfNode: 16, NumOfProber: 3})
		if len(a.Groups) == 0 {
			t.Fatalf("n=%d: no groups", n)
		}
		for i, g := range a.Groups {
			if !Verify(a.Epoch, a.N, g.Members, g.Probers, a.Paths[i], a.Root) {
				t.Fatalf("n=%d group %d failed to verify", n, i)
			}
		}
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	a := Build(testAddrs(62), testProbers(3), 165464, Conf{NumOfNode: 16, NumOfProber: 3})
	g, path := a.Groups[0], a.Paths[0]

	if Verify(a.Epoch+1, a.N, g.Members, g.Probers, path, a.Root) {
		t.Error("a proof from one slot verified against another slot's root")
	}
	if Verify(a.Epoch, a.N+1, g.Members, g.Probers, path, a.Root) {
		t.Error("N is not bound to the root")
	}

	swapped := append([]Addr(nil), g.Members...)
	swapped[0] = testAddr(9999)
	if Verify(a.Epoch, a.N, swapped, g.Probers, path, a.Root) {
		t.Error("an outsider was accepted into a group")
	}

	extraProber := append(append([]Addr(nil), g.Probers...), testAddr(8888))
	if Verify(a.Epoch, a.N, g.Members, extraProber, path, a.Root) {
		t.Error("an unassigned prober was accepted")
	}
}

// A leaf is a function of the SET, not of arrival order: the RV builds it from
// a shuffle and the voucher server rebuilds it from a claim.
func TestLeafIgnoresOrder(t *testing.T) {
	members, probers := testAddrs(16), testProbers(3)
	want := LeafHash(165464, members, probers)

	rev := make([]Addr, len(members))
	for i := range members {
		rev[i] = members[len(members)-1-i]
	}
	if got := LeafHash(165464, rev, probers); got != want {
		t.Fatal("leaf depends on the order members were supplied in")
	}
}

// 0xFF keeps the two columns apart. Without it, recasting a checked node as its
// own checker would leave the concatenation, and so the leaf, unchanged.
func TestLeafSeparatesColumns(t *testing.T) {
	all := testAddrs(4)
	a := LeafHash(165464, all[:3], all[3:])
	b := LeafHash(165464, all[:2], all[2:])
	if a == b {
		t.Fatal("moving an address between the member and prober columns did not change the leaf")
	}
}

func TestPairHashIsOrderIndependent(t *testing.T) {
	x, y := keccak([]byte("x")), keccak([]byte("y"))
	if PairHash(x, y) != PairHash(y, x) {
		t.Fatal("sorted-pair folding is not order independent")
	}
}

// One group is the normal state of a small network, so the degenerate tree is
// the path that runs in production first.
func TestSingleLeafHasEmptyPath(t *testing.T) {
	a := Build(testAddrs(10), testProbers(1), 165464, Conf{NumOfNode: 16, NumOfProber: 1})
	if len(a.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(a.Groups))
	}
	if len(a.Paths[0]) != 0 {
		t.Fatalf("want an empty path, got %d hashes", len(a.Paths[0]))
	}
	if !Verify(a.Epoch, a.N, a.Groups[0].Members, a.Groups[0].Probers, nil, a.Root) {
		t.Fatal("single-leaf proof failed")
	}
}

// Odd counts are where a merkle implementation usually breaks: 3 and 5 leaves
// both promote an unpaired node.
func TestOddLeafCounts(t *testing.T) {
	for _, leafCount := range []int{3, 5, 7, 9} {
		n := leafCount * 16
		a := Build(testAddrs(n), testProbers(3), 165464, Conf{NumOfNode: 16, NumOfProber: 3})
		if len(a.Groups) != leafCount {
			t.Fatalf("n=%d: got %d groups, want %d", n, len(a.Groups), leafCount)
		}
		for i, g := range a.Groups {
			if !Verify(a.Epoch, a.N, g.Members, g.Probers, a.Paths[i], a.Root) {
				t.Fatalf("leaves=%d: group %d failed", leafCount, i)
			}
		}
	}
}

func TestRootIsDeterministic(t *testing.T) {
	cfg := Conf{NumOfNode: 16, NumOfProber: 3}
	a := Build(testAddrs(62), testProbers(3), 165464, cfg)

	shuffledNodes := append([]Addr(nil), testAddrs(62)...)
	rng := rand.New(rand.NewSource(11))
	rng.Shuffle(len(shuffledNodes), func(i, j int) {
		shuffledNodes[i], shuffledNodes[j] = shuffledNodes[j], shuffledNodes[i]
	})
	shuffledProbers := []Addr{testProbers(3)[2], testProbers(3)[0], testProbers(3)[1]}

	b := Build(shuffledNodes, shuffledProbers, 165464, cfg)
	if a.Root != b.Root {
		t.Fatal("root changed when the inputs were supplied in a different order")
	}

	c := Build(testAddrs(62), testProbers(3), 165465, cfg)
	if a.Root == c.Root {
		t.Fatal("root did not change between slots")
	}
}

func TestParseAndPack(t *testing.T) {
	a := testAddr(1)
	// Checksummed input must normalise, or a mixed-case address would hash to
	// a different leaf than the lowercase one another implementation uses.
	got, err := ParseAddr("0X" + upperHex(a))
	if err != nil {
		t.Fatal(err)
	}
	if got != a {
		t.Fatal("ParseAddr did not normalise casing")
	}
	if _, err := ParseAddr("0x1234"); err == nil {
		t.Fatal("short address accepted")
	}

	addrs := testAddrs(5)
	back, err := UnpackAddrs(PackAddrs(addrs))
	if err != nil {
		t.Fatal(err)
	}
	for i := range addrs {
		if back[i] != addrs[i] {
			t.Fatalf("pack round trip failed at %d", i)
		}
	}
	if _, err := UnpackAddrs([]byte{1, 2, 3}); err == nil {
		t.Fatal("misaligned blob accepted")
	}
}

func upperHex(a Addr) string {
	const hexUpper = "0123456789ABCDEF"
	out := make([]byte, 0, 40)
	for _, b := range a {
		out = append(out, hexUpper[b>>4], hexUpper[b&0x0f])
	}
	return string(out)
}
