package faucet

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// Golden vectors are the ONLY thing that catches the failure this package is
// most exposed to: three copies of the same rules drifting apart.
//
// A missing domain tag or a different integer width is invisible to a
// round-trip test, because the copy that made the mistake verifies its own
// output happily. It only shows up when two copies meet, and by then it shows
// up as claims being rejected. So the expected bytes are committed, and every
// repo holding a copy of this package tests against the same file.
//
// Regenerate with:  go test ./pkg/faucet -update
// Then copy testdata/vectors.json into the other repos, unchanged.
var update = flag.Bool("update", false, "rewrite testdata/vectors.json")

const vectorsPath = "testdata/vectors.json"

type vectorCase struct {
	Name        string     `json:"name"`
	Epoch       int64      `json:"epoch"`
	NodeCount   int        `json:"node_count"`
	ProberCount int        `json:"prober_count"`
	NumOfNode   int        `json:"num_of_node"`
	NumOfProber int        `json:"num_of_prober"`
	N           int        `json:"n"`
	GroupSizes  []int      `json:"group_sizes"`
	Root        string     `json:"root"`
	Leaves      []string   `json:"leaves"`
	Paths       [][]string `json:"paths"`
	// ProbeMessages is the string a prober signs for the first member of each
	// group. One per group is enough to pin the format; the interesting part is
	// the encoding, not the address.
	ProbeMessages []string `json:"probe_messages"`
	Groups        []struct {
		Members []string `json:"members"`
		Probers []string `json:"probers"`
	} `json:"groups"`
}

type vectorFile struct {
	// Note is copied into the file so someone opening it in another repo knows
	// what it is for without finding this test first.
	Note string `json:"note"`
	// AddrRule states how the inputs were derived, so a non-Go implementation
	// can rebuild the same case list from scratch.
	AddrRule   string       `json:"addr_rule"`
	ProberRule string       `json:"prober_rule"`
	Cases      []vectorCase `json:"cases"`
}

func buildVectors() vectorFile {
	specs := []struct {
		name                   string
		epoch                  int64
		nodes, probers         int
		numOfNode, numOfProber int
	}{
		{"n1-single-group", 165464, 1, 1, 16, 1},
		{"n2", 165464, 2, 1, 16, 1},
		{"n15-below-target", 165464, 15, 3, 16, 3},
		{"n16-exact-target", 165464, 16, 3, 16, 3},
		{"n17-still-one-group", 165464, 17, 3, 16, 3},
		{"n31-largest-single-group", 165464, 31, 3, 16, 3},
		{"n32-first-split", 165464, 32, 3, 16, 3},
		{"n62-three-groups", 165464, 62, 3, 16, 3},
		{"n100-six-groups", 165464, 100, 3, 16, 3},
		{"n62-next-slot", 165465, 62, 3, 16, 3},
		{"odd-three-leaves", 165464, 48, 3, 16, 3},
		{"odd-five-leaves", 165464, 80, 3, 16, 3},
		{"probers-fewer-than-groups", 165464, 62, 2, 16, 1},
		{"probers-fewer-than-numofprober", 165464, 62, 2, 16, 5},
	}

	out := vectorFile{
		Note:       "faucet group assignment golden vectors. Every implementation of pkg/faucet must reproduce these exactly.",
		AddrRule:   "node i = first 20 bytes of keccak256(\"faucet-test-node\" || uint64be(i))",
		ProberRule: "prober i = first 20 bytes of keccak256(\"faucet-test-prober\" || uint64be(i))",
	}

	for _, s := range specs {
		cfg := Conf{NumOfNode: s.numOfNode, NumOfProber: s.numOfProber}
		a := Build(testAddrs(s.nodes), testProbers(s.probers), s.epoch, cfg)

		c := vectorCase{
			Name:        s.name,
			Epoch:       s.epoch,
			NodeCount:   s.nodes,
			ProberCount: s.probers,
			NumOfNode:   s.numOfNode,
			NumOfProber: s.numOfProber,
			N:           a.N,
			Root:        a.Root.Hex(),
		}
		for i, g := range a.Groups {
			c.GroupSizes = append(c.GroupSizes, len(g.Members))
			c.Leaves = append(c.Leaves, LeafHash(a.Epoch, g.Members, g.Probers).Hex())

			path := []string{}
			for _, h := range a.Paths[i] {
				path = append(path, h.Hex())
			}
			c.Paths = append(c.Paths, path)

			var row struct {
				Members []string `json:"members"`
				Probers []string `json:"probers"`
			}
			for _, m := range g.Members {
				row.Members = append(row.Members, m.Hex())
			}
			for _, p := range g.Probers {
				row.Probers = append(row.Probers, p.Hex())
			}
			c.Groups = append(c.Groups, row)

			if len(g.Members) > 0 {
				c.ProbeMessages = append(c.ProbeMessages, ProbeMessage(a.Epoch, a.Root, g.Members[0]))
			}
		}
		out.Cases = append(out.Cases, c)
	}
	return out
}

func TestGoldenVectors(t *testing.T) {
	got := buildVectors()

	if *update {
		if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o755); err != nil {
			t.Fatal(err)
		}
		blob, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, append(blob, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d cases)", vectorsPath, len(got.Cases))
		return
	}

	blob, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("%v (run: go test ./pkg/faucet -update)", err)
	}
	var want vectorFile
	if err := json.Unmarshal(blob, &want); err != nil {
		t.Fatal(err)
	}
	if len(want.Cases) != len(got.Cases) {
		t.Fatalf("vector count changed: file has %d, code produces %d", len(want.Cases), len(got.Cases))
	}

	for i, w := range want.Cases {
		g := got.Cases[i]
		if w.Name != g.Name {
			t.Fatalf("case %d: name %q != %q", i, w.Name, g.Name)
		}
		if w.Root != g.Root {
			t.Errorf("%s: root %s != %s", w.Name, g.Root, w.Root)
		}
		if w.N != g.N {
			t.Errorf("%s: N %d != %d", w.Name, g.N, w.N)
		}
		if len(w.Leaves) != len(g.Leaves) {
			t.Errorf("%s: %d leaves != %d", w.Name, len(g.Leaves), len(w.Leaves))
			continue
		}
		for j := range w.Leaves {
			if w.Leaves[j] != g.Leaves[j] {
				t.Errorf("%s: leaf %d %s != %s", w.Name, j, g.Leaves[j], w.Leaves[j])
			}
		}
		if len(w.ProbeMessages) != len(g.ProbeMessages) {
			t.Errorf("%s: %d probe messages != %d", w.Name, len(g.ProbeMessages), len(w.ProbeMessages))
			continue
		}
		for j := range w.ProbeMessages {
			if w.ProbeMessages[j] != g.ProbeMessages[j] {
				t.Errorf("%s: probe message %d: got %s, want %s", w.Name, j, g.ProbeMessages[j], w.ProbeMessages[j])
			}
		}
	}
}

// Every committed vector must still verify through the public API. This is what
// another repo's copy runs to prove it agrees, without needing to rebuild the
// assignment itself.
func TestGoldenVectorsVerify(t *testing.T) {
	blob, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Skipf("no vectors yet: %v", err)
	}
	var vf vectorFile
	if err := json.Unmarshal(blob, &vf); err != nil {
		t.Fatal(err)
	}

	for _, c := range vf.Cases {
		root, err := ParseHash(c.Root)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		for i, g := range c.Groups {
			members := parseAddrList(t, c.Name, g.Members)
			probers := parseAddrList(t, c.Name, g.Probers)

			path := make([]Hash, 0, len(c.Paths[i]))
			for _, h := range c.Paths[i] {
				ph, err := ParseHash(h)
				if err != nil {
					t.Fatalf("%s: %v", c.Name, err)
				}
				path = append(path, ph)
			}
			if !Verify(c.Epoch, c.N, members, probers, path, root) {
				t.Errorf("%s: group %d does not verify against the committed root", c.Name, i)
			}
		}
	}
}

func parseAddrList(t *testing.T, caseName string, in []string) []Addr {
	t.Helper()
	out := make([]Addr, 0, len(in))
	for _, s := range in {
		a, err := ParseAddr(s)
		if err != nil {
			t.Fatalf("%s: %v", caseName, err)
		}
		out = append(out, a)
	}
	return out
}
