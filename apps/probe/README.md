# probe mesh

The faucet prober. Asks public nodes a question, records the answer, and does
nothing at all until the RV names this node a prober.

## Install

```
<install_root>/artifacts/addon/meshes/probe/
  mesh.json
  bin/probe[.exe]
  conf/probe.json
```

```bash
isann mesh on probe --now     # run now + on every boot
isann mesh status
```

`isann mesh on` only starts it. **Unless the RV lists this node under `probers`
in its `faucet.json`, it stays idle** and logs one line saying so — the intended
state for a node that has the mesh installed but is not a prober.

## What it needs

| | |
|---|---|
| isannd running | it calls the node-bridge for everything: discovery, NAT traversal and the HTTP/3 hop to the target all happen there |
| a slot assignment | the RV names its probers in `faucet.json`; every three-hour slot it publishes groups and a merkle root, and isannd hands this node its share. Nothing to install here, no wallet unlock |
| a TPM | tickets are signed by the TPM key the RV recorded for this machine. No TPM, no tickets — checks are still recorded locally |
| a question writer (optional) | an allied node that writes geography/animal/colour questions. Need not be this machine — see below. Without one it runs on arithmetic alone, which is the one category whose answers are certain |

## Who writes the questions

A prober is a small machine whose job is to ask questions, not to host a 14B
model. So the writers are named in the config and tried in round-robin; a
failure moves to the next, and a failed entry is skipped for ten minutes.

```json
"generators": ["this", "0xbada8be8…", "home/llm-api-2"],
"generator_service": "llm-api"
```

`""` / `this` / `local` / `self` all mean this node. Anything else is a node id
or a favorite alias — isannd resolves both on the `/node/` path, so nothing is
parsed here. A `/service` suffix overrides the default for that one entry.

**Absent and empty are different statements:**

```
generators absent   →  ["this"]   the old behaviour
generators []       →  no generation at all; arithmetic only, chosen deliberately
clips absent or []  →  the image track does not run
```

Clips default to off because judging has no local fallback the way arithmetic
is a fallback for questions — firing at an image node with no judge waiting only
burns someone else's GPU.

🔴 **These are ALLIED nodes.** A writer learns every question before it is
asked, and a CLIP validator decides outright whether a node gets paid. Neither
is a role to hand to a stranger off the directory. If a writer is compromised
the floor is arithmetic: generated here, fresh per shot, and impossible to leak
in advance.

An allied node in `protected` mode is fine — isannd attaches the active
inference-access credential (`isann cred add --kind infer`) to outbound `/svc`
calls by itself, so there is nothing to configure here.

This node is dropped from the target list: a prober earning its own tickets
measures nothing. (`"fire_at_self": true` turns that off, for a single-machine
test.) The writers are **not** dropped — arithmetic is generated here, fresh per
shot, so an allied node still answers something it could not have prepared.

## When it asks

A slot is three hours. Every text node is asked the quiz **twice** in it, once in
each sampling window, at a random moment inside the window:

```
0:00 ........ the slot opens; nothing is sent
0:30 - 1:00   round 1: every node is asked once, at its own random moment
2:00 - 2:30   round 2: every node is asked once, at its own random moment
2:31 - 2:59   the slot's tickets are decided, signed and handed to the nodes
3:00 ........ the slot ends
```

- Every node behind one /24 is asked at the same moment, because a farm of
  registrations sharing one GPU answers one at a time fine and fails all at once.
  The moment is drawn per /24 and per round.
- Nobody is asked twice in a round, whatever came back. There is no retry.
- A ticket needs the quiz passed in **both rounds**. A round the node was asked in
  and did not pass is lost: a wrong answer, one cut off by the token cap, a
  refusal, a full queue, or no answer inside its deadline. A round it was not
  asked in is not a pass either (the prober was down for the whole window, the node
  was not in the directory during it, or the prober's own shot failed), and the
  windows are closed by the time the slot is settled, so that slot has no ticket.
- Nothing is issued when an answer comes back. Every node is decided once, from a
  minute after the second window closes until the slot ends, and a node keeps only
  the first ticket it is handed for a slot.
- A node in a group of three or more running the same model is also sent a
  sentence request in each window and must not lose either round (`compare.go`).
- Pictures are ordered once a slot, as soon as the firing window opens, and are
  not part of any of this.

Two limits to size against, both from putting every ticket in the last half hour:

```
subnets_per_round   /24s taken per minute. A 30-minute window takes at most 30 times this
                    many, so 2 (the shipped value) is 60 networks per round. A node
                    not reached in both rounds gets no ticket for the slot
TPM signing         about 3 seconds a signature, one at a time; a model ticket signs twice.
                    A few hundred tickets per prober per slot is the ceiling
```

## Designated models

Every node that passes the check gets a ticket. The designated model list only
decides what the ticket carries:

```
model on the list   the ticket also carries the track (text), the parameter count
                    in billions (14.768) and the model hash, under a second TPM signature
anything else       a plain ticket, as before
```

The first signature is unchanged either way, so an RV that does not read the
model fields pays every ticket as before. `pkg/faucet/ticket_ext.go` has the
format.

The list is not in this folder. It is on chain, in the FaucetModelRegistry
contract: anyone requests a model file, and the operators approve it (M-of-N)
for a period, or reject or stop it. One registry line is one file for one
period:

```
hash     sha256 of the file
params   the parameter count the faucet pays it by
startAt  the period, startAt <= t < endAt
endAt
```

The prober asks its own isannd which chain query API it uses (`receipt.api` in
isannd's conf, shown as `api` on `/internal/api/info`), then reads the lines in
their period at the start of the current slot from that API directly
(`/v1/faucet-models?activeAt=<slot start>`), with each directory refresh. Every
prober of a slot reads the same list; a line approved or stopped mid-slot
changes the tickets from the next slot. Until the first read succeeds, or when
the API lists nothing, every ticket is a plain one. A failed read keeps the list
from the last one. An isannd older than the `api` field names no API, and its
prober writes plain tickets only.

The hash is the file's sha256 as the node reports it in the RV directory
(`model_hash`). The parameter count is the operators' number, not the node's: a
mixture-of-experts model can be listed at what it costs to run. List
single-file models (GGUF) only: a sharded model's `model_hash` covers its index
file, not the weights.

## The signing keys

There is no key to configure and no passphrase. Two keys are used, both tied to
this machine:

```
X-ISANN-Probe header   node identity key (derived from the hardware)   "this machine is a prober of this slot"
ticket                 TPM key (never leaves the chip)                  "this machine checked that node"
```

The RV already knows both — it recorded them when this node registered — so
nothing extra travels with a ticket to say who signed it. A copied config file
cannot mint tickets in this machine's name.

## Where things land

```
logs/            probe log (the mesh runs with this folder as its cwd)
probe.db         SQLite - questions, shots, directory observations
conf/probe.json  every setting has a working default
```

## Scoring

Answers are scored as they arrive, in the same write as the answer itself. The
ticket is signed later, when the slot settles, from the verdicts on record.

```
math            the first number, compared exactly. "192" is not "92"
everything else normalise → length gate → word-set containment either way
```

Containment runs in both directions because neither side is reliably the longer
one: a draft of `Delhi` meets a node saying `New Delhi`, and a draft of
`Washington D.C.` meets a node saying `Washington`. It is containment and not
overlap — `New Delhi` and `New York` share a word and are not the same place —
and a length gate caps how much a node may add, without which answering with a
list of capitals would pass every geography question.

`truncated` is a third verdict, distinct from a fail: the node was still
speaking when our own token cap cut it off, so what came back is a prefix rather
than an answer. A run of those means the cap is set too low.

## What it does not do

Image probes. `clips` wires up the validator call, but generating the prompts,
firing them and collecting the images is not built yet.
