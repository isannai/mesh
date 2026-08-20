package probe

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/isannai/mesh/pkg/auth"
	"github.com/isannai/mesh/pkg/faucet"
	"github.com/isannai/mesh/pkg/rvnodes"
)

// buildSlot draws a real assignment the way the RV does, then renders it into
// the JSON shape FetchAssignment would have decoded.
//
// Real tree, real root, real paths. A hand-built fixture would only prove the
// bundle agrees with itself, when what matters is that a node folding it with
// the SAME package arrives at the same root.
func buildSlot(t *testing.T, members int) (Assignment, faucet.Addr, faucet.Addr) {
	t.Helper()

	var prober faucet.Addr
	prober[0], prober[19] = 0x9a, 0x01
	var target faucet.Addr
	target[0], target[19] = 0x9a, 0x02

	nodes := []faucet.Addr{prober, target}
	for i := 0; i < members-2; i++ {
		var a faucet.Addr
		a[0], a[18], a[19] = 0x77, byte(i>>8), byte(i)
		nodes = append(nodes, a)
	}

	epoch := faucet.Epoch(time.Now().Unix())
	built := faucet.Build(nodes, []faucet.Addr{prober}, epoch,
		faucet.Conf{NumOfNode: 16, NumOfProber: 1})

	hexes := func(in []faucet.Addr) []string {
		out := make([]string, 0, len(in))
		for _, v := range in {
			out = append(out, v.Hex())
		}
		return out
	}
	a := Assignment{
		RV: "rv.test:9100", SlotSec: faucet.SlotSeconds,
		Epoch: epoch, Root: built.Root.Hex(), N: built.N,
	}
	for i, g := range built.Groups {
		path := make([]string, 0, len(built.Paths[i]))
		for _, h := range built.Paths[i] {
			path = append(path, h.Hex())
		}
		a.Groups = append(a.Groups, AssignGroup{
			Members: hexes(g.Members), Probers: hexes(g.Probers), Path: path,
		})
	}
	root, err := faucet.ParseHash(a.Root)
	if err != nil {
		t.Fatal(err)
	}
	a.root = root
	return a, prober, target
}

// TestBundleFoldsToRoot is the contract with isannd: what we build has to fold
// back to the root the node was given, using the same package on both sides.
func TestBundleFoldsToRoot(t *testing.T) {
	a, _, target := buildSlot(t, 6)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	gi := a.GroupOf(target.Hex())
	if gi < 0 {
		t.Fatal("the target is in no group")
	}

	hdr, err := BuildProbeHeader(a, gi, target.Hex(), key)
	if err != nil {
		t.Fatal(err)
	}

	blob, err := base64.RawURLEncoding.DecodeString(hdr)
	if err != nil {
		t.Fatalf("the header is not base64url: %v", err)
	}
	var b probeBundle
	if err := json.Unmarshal(blob, &b); err != nil {
		t.Fatalf("the header is not a bundle: %v", err)
	}

	members := parseAll(t, b.Members)
	probers := parseAll(t, b.Probers)
	path := make([]faucet.Hash, 0, len(b.Path))
	for _, h := range b.Path {
		v, err := faucet.ParseHash(h)
		if err != nil {
			t.Fatal(err)
		}
		path = append(path, v)
	}

	if !faucet.Verify(b.Epoch, b.N, members, probers, path, a.Root32()) {
		t.Fatal("the bundle we build does not fold to the root we were given")
	}
}

// TestBundleSignatureNamesTheTarget guards the field that stops a bundle being
// a bearer token. Two targets in the same group must get DIFFERENT signatures.
func TestBundleSignatureNamesTheTarget(t *testing.T) {
	a, _, target := buildSlot(t, 6)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	gi := a.GroupOf(target.Hex())

	other := parseAll(t, a.Groups[gi].Members)
	var second faucet.Addr
	for _, m := range other {
		if m != target {
			second = m
			break
		}
	}

	one, err := BuildProbeHeader(a, gi, target.Hex(), key)
	if err != nil {
		t.Fatal(err)
	}
	two, err := BuildProbeHeader(a, gi, second.Hex(), key)
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("two nodes in one group got the same header — either could spend the other's free request")
	}

	// And the signature must recover to the signing key, over the message that
	// names the target. Same string isannd rebuilds.
	var b probeBundle
	blob, _ := base64.RawURLEncoding.DecodeString(one)
	if err := json.Unmarshal(blob, &b); err != nil {
		t.Fatal(err)
	}
	got, err := auth.RecoverAddress(faucet.ProbeMessage(a.Epoch, a.Root32(), target), b.Sig)
	if err != nil {
		t.Fatal(err)
	}
	want := crypto.PubkeyToAddress(key.PublicKey).Hex()
	if !strings.EqualFold(got, want) {
		t.Fatalf("recovered %s, want %s", got, want)
	}
}

// TestBundleRefusesWithoutKey — an unproven shot still costs the node a real
// inference, so a missing key must stop the shot rather than degrade it.
func TestBundleRefusesWithoutKey(t *testing.T) {
	a, _, target := buildSlot(t, 6)
	if _, err := BuildProbeHeader(a, a.GroupOf(target.Hex()), target.Hex(), nil); err == nil {
		t.Fatal("a bundle was built with no signing key")
	}
}

// TestAssignedTargetsSkipsSelf — a prober is seated in its own group by the RV,
// because the leaf must hash the group as it really is. It cannot be its own
// evidence, so the skip lives on the checking side.
//
// Tested through assignedTargets rather than on the Assignment, because that is
// where the rule is actually enforced: a test on the data structure would have
// passed while the firing path did the opposite.
func TestAssignedTargetsSkipsSelf(t *testing.T) {
	a, prober, target := buildSlot(t, 6)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	p := &Prober{
		assign: a, hasAssign: true,
		self:    strings.ToLower(prober.Hex()),
		signKey: key,
	}
	in := []Target{
		{Node: rvnodes.Node{ID: "S:" + prober.Hex()}},
		{Node: rvnodes.Node{ID: "S:" + target.Hex()}},
	}

	out := p.assignedTargets(in)
	for _, tg := range out {
		if strings.EqualFold(nodeAddressOf(tg.Node.ID), prober.Hex()) {
			t.Fatal("the prober would check itself")
		}
	}
	if len(out) != 1 {
		t.Fatalf("expected the one other member, got %d", len(out))
	}
	if out[0].Probe == "" {
		t.Fatal("a target went out with no proof attached")
	}

	// The prober is still IN the group. Removing it there would change the leaf
	// and therefore the root.
	if a.GroupOf(prober.Hex()) < 0 {
		t.Fatal("the prober was dropped from its own group, which changes the root")
	}
}

// TestAssignedTargetsHonoursFireAtSelf — the flag exists for a single-PC setup
// with nowhere else to aim. eligible() already respects it; if this second gate
// ignored it the flag would be switched on and do nothing.
func TestAssignedTargetsHonoursFireAtSelf(t *testing.T) {
	a, prober, target := buildSlot(t, 6)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	p := &Prober{
		assign: a, hasAssign: true,
		self:    strings.ToLower(prober.Hex()),
		signKey: key,
		cfg:     Config{FireAtSelf: true},
	}
	in := []Target{
		{Node: rvnodes.Node{ID: "S:" + prober.Hex()}},
		{Node: rvnodes.Node{ID: "S:" + target.Hex()}},
	}

	if out := p.assignedTargets(in); len(out) != 2 {
		t.Fatalf("fire_at_self is on but self was still dropped: got %d target(s)", len(out))
	}
}

// TestAssignmentStale — a slot that has turned over must stop being used. A
// previous slot's bundle is rejected by the node, which reads like the NODE
// misbehaving.
func TestAssignmentStale(t *testing.T) {
	a, _, _ := buildSlot(t, 4)
	now := time.Now()
	if a.Stale(now) {
		t.Fatal("a fresh assignment reported stale")
	}
	if !a.Stale(now.Add(time.Duration(faucet.SlotSeconds) * time.Second)) {
		t.Fatal("an assignment survived its slot")
	}
}

func parseAll(t *testing.T, in []string) []faucet.Addr {
	t.Helper()
	out := make([]faucet.Addr, 0, len(in))
	for _, s := range in {
		a, err := faucet.ParseAddr(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}
