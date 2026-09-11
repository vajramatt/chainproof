# ChainProof Mission Workspace Format v1

A mission workspace is a portable filesystem projection of one verified
ChainProof mission. It carries canonical continuity proof records, deterministic
readable views, and content-addressed artifact bodies without copying a live
SQLite database, local agent identity, or lease state.

## Directory layout

```text
manifest.json
continuity.json
events.jsonl
README.md
artifacts/
  SHA256
```

`manifest.json` has format `chainproof.mission-workspace.v1`, creation time,
mission ID, and a sorted `files` array. Every file record contains a relative
path, lowercase SHA-256 digest, byte size, and media type. Manifest does not
list itself.

Only these paths are valid:

- `continuity.json` with media type `application/json`
- `events.jsonl` with media type `application/x-ndjson`
- `README.md` with media type `text/markdown; charset=utf-8`
- `artifacts/HASH`, where `HASH` is 64 lowercase hexadecimal characters

Implementations MUST reject absolute paths, traversal, duplicate or unsorted
records, symlinks, non-regular files, undeclared files, and unexpected
directories.

## Canonical and derived content

`continuity.json` is a `chainproof.continuity.bundle.v1` proof and is canonical
workspace evidence. It MUST verify under continuity v1.

`events.jsonl` is a deterministic projection of full canonical events from the
longest bundled prefix of each run. Runs follow first appearance in checkpoint
order; events retain sequence order. Repeated shorter prefixes are omitted.

`README.md` is a deterministic human-readable view of mission manifest, latest
checkpoint summary, and next actions. It states proof boundary. README and
JSONL are rebuildable views, not independent evidence.

Artifact references use event `artifacts` entries containing `hash` and
optional `media_type`. Every valid referenced hash MUST have exactly one
`artifacts/HASH` body. Body bytes MUST hash to filename and manifest digest.
Conflicting media types for same hash invalidate workspace.

## Verification

Verifier MUST:

1. validate root, manifest, path allowlist, complete file inventory, byte sizes,
   and every file hash;
2. verify continuity bundle and manifest mission ID;
3. regenerate `events.jsonl` and `README.md` from continuity proof and require
   byte equality;
4. require artifact inventory to match referenced content hashes and media
   types.

Manifest is not signed or externally anchored. Attacker able to replace whole
workspace can substitute another internally valid mission. Preserve manifest
digest or continuity chain head through separate trusted channel when that
threat matters.

## Import

Importer MUST complete workspace verification before mutation. Mission, run,
event, checkpoint, search-index, and artifact changes MUST commit in one local
SQLite transaction. Existing identical content-addressed artifact bodies MAY
be reused. Mission, run, event, checkpoint, or conflicting artifact collisions
MUST abort without partial state.

Import preserves canonical IDs, timestamps, hashes, provenance modes, and
mission lifecycle status. It MUST NOT import agent profiles, private keys,
leases, lease history, or other machine-local coordination state.

Continuing same active mission independently on source and destination can fork
future work. Mission workspace v1 provides portable continuity, not multi-host
ownership or reconciliation.
