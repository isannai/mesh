// Package artifactmeta defines the standard provenance/compatibility header
// shared by every uploadable iSANN artifact — recipe (.ian), preset, profile,
// and addon tools.json.
//
// Two halves make the full header:
//
//   - identity  : `name` + `version` — what this is and which revision. These
//     live on each artifact's own struct (preset/profile/tools.json already
//     carry `name`; `version` is added where missing) because every artifact
//     names/versions itself differently.
//   - provenance: this Meta — `author` + `license` + `min_isann`. Embedded
//     (anonymous) into each artifact struct so its three fields flatten to the
//     same top-level JSON keys everywhere. Recipes carry the same data as
//     comments instead (`#pragma ISANN <ver>` + `# author:`/`# license:` —
//     see cmd/isann/recipe/parser.go parseDocString).
//
// Empty fields mean "unset". They are emitted (NO omitempty) so a freshly
// authored file shows the full template, and so the hub can auto-fill them on
// upload — in particular `author`, which the hub sets to (and verifies against)
// the signer's wallet, making a stored value provenance rather than a free claim.
package artifactmeta

// Meta is the provenance/compatibility trio embedded into preset / profile /
// tools.json structs. Identity (`name`, `version`) lives on the host struct.
type Meta struct {
	// MinIsann is the minimum isann version this artifact supports — the JSON
	// equivalent of a recipe's `#pragma ISANN <ver>`. Empty = no floor.
	MinIsann string `json:"min_isann"`
	// Author is the creator's display NAME — a free-form label written in the
	// file (e.g. "sd-lab"). It is NOT the identity anchor: the signing wallet
	// ADDRESS (derived/verified from the upload signature at the hub, not stored
	// here) is the real provenance. Author is just a human-readable label.
	Author string `json:"author"`
	// License is an SPDX-ish license id (MIT, Apache-2.0, CC-BY-4.0, …).
	// Empty = unspecified.
	License string `json:"license"`
}
