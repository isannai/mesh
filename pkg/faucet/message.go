package faucet

import "strconv"

// ProbeMessagePrefix tags the string a prober signs when it comes to check a
// node. It follows the shape already used by ISANN-CREDENTIAL and ISANN-ACCESS
// so that one convention covers every signed artifact on the wire.
const ProbeMessagePrefix = "ISANN-PROBE:"

// ProbeMessage builds the string a prober signs to open a node's free gate.
//
//	ISANN-PROBE:<epoch>:<root>:<node>
//
// # WHY A SIGNATURE AT ALL
//
// The group bundle a prober carries is not a secret. Every node in the group
// receives it, and the prober presents it in the open. If holding the bundle
// were enough to be served for free, anyone who had ever seen one could replay
// it. The signature is what separates "I have a copy of the assignment" from
// "I hold the key of a prober named in it", and it costs the verifier a single
// RecoverAddress.
//
// # WHY THE NODE ADDRESS IS IN IT
//
// Without the last field this string is IDENTICAL for the whole network for
// three hours, so one signature would work against every node in the group:
//
//	prober P checks node X   ->  X now holds P's signature
//	X presents it to node Y  ->  Y is also P's, so it verifies
//	                         ->  Y's one free request for P is spent
//	                         ->  when P actually arrives, Y refuses
//	                         ->  Y loses the slot through no fault of its own
//
// Naming the recipient makes a signature usable only where it was sent. A
// prober signs once per node it visits, which is tens of ECDSA operations per
// slot and costs nothing next to the inference it is about to request.
//
// # ENCODING
//
// epoch is decimal, root and node are 0x-prefixed lowercase hex. Signer and
// verifier must build byte-identical strings or the recovered address is a
// different one, so both sides call THIS function rather than formatting their
// own. Same reasoning as the leaf encoding: it is a wire format, not a detail.
func ProbeMessage(epoch int64, root Hash, node Addr) string {
	return ProbeMessagePrefix + strconv.FormatInt(epoch, 10) + ":" + root.Hex() + ":" + node.Hex()
}
