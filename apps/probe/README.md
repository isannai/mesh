# probe mesh

The faucet prober. Asks public nodes a question, records the answer, and does
nothing at all until it holds an appointment.

## Install

```
<install_root>/artifacts/addon/meshes/probe/
  mesh.json
  bin/probe[.exe]
  conf/probe.json
```

```bash
isann cred add --alias faucet --token ianprb_…   # the appointment
isann mesh on probe --now                        # run now + on every boot
isann mesh status
```

`isann mesh on` only starts it. **Without an appointment it stays idle** and
logs one line saying so — the intended state for a node that has the mesh
installed but has not been appointed.

## What it needs

| | |
|---|---|
| isannd running | it calls the node-bridge for everything: discovery, NAT traversal and the HTTP/3 hop to the target all happen there |
| an appointment | read from isannd, not from this config. `isann cred add` installs it, `isann cred list` shows it. **No wallet unlock needed** — the prober reads the one cred route isannd leaves ungated |
| a signing key (recommended) | needed to sign tickets, and — already useful now — to check at boot that the appointment is bound to a key this node actually holds |
| a text engine (optional) | generates geography/animal/colour questions. Without one it runs on arithmetic alone, which is the one category whose answers are certain |

## The signing key

The appointment names exactly one address that may sign tickets, so **there is
no key path to configure** — only a passphrase. The key is found by that
address in `artifacts/keystores/`, the same way the RV finds its voucher key.

```json
{ "signer": { "passphrase": "…" } }
```

or, keeping it out of the file, `PROBE_SIGNER_PASSPHRASE` (mesh marks it
`secret: true`, so `isann mesh config` handles it).

That is deliberately the only knob. With a configurable path there are two
things to get right and they can disagree; by address there is one question
with one answer — either this node has the key or it does not:

```
[probe] no keystore for 0xc8ff97… in …/artifacts/keystores — the appointment is
        bound to an address this node has no key for
```

Whichever key you use, the appointment must be bound to it
(`ivm account issue --kind prober --bind <that address>`). A purpose-made key
is fine and means a leak costs the prober role and nothing else; the node's own
wallet works too.

Leaving the passphrase unset is allowed for now — firing probes is anonymous,
so nothing is signed yet. The startup log will say the key is UNVERIFIED.

`PROBE_KEYSTORES_DIR` overrides where to look, for installs that keep keys
somewhere unusual.

## Where things land

```
logs/            probe log (the mesh runs with this folder as its cwd)
probe.db         SQLite - questions, shots, directory observations
conf/probe.json  every setting has a working default
```

## What it does not do

It does not score answers yet. Every answer is stored verbatim so scoring can
be applied retroactively rather than by re-firing at every node.
