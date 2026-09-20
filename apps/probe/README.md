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

Answers are scored as they arrive, in the same write as the answer itself — a
ticket is signed at fire time, so a verdict that turns up later has no ticket
left to affect.

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
