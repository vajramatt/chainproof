# ChainProof Continuity Format v1

ChainProof continuity links bounded agent runs into a durable mission. It does
not change the [provenance v1](provenance-v1.md) event format. Each mission
checkpoint anchors an exact, independently verifiable prefix of one existing
run proof.

## Mission manifest

A mission names an agent and objective, tracks lifecycle state, and declares
the checkpoint count and current checkpoint-chain head. Operational metadata,
status, and timestamps are local control state. Checkpoints bind the agent and
SHA-256 hash of the objective into the continuity proof.

## Canonical checkpoint

Every hashed checkpoint contains these fields:

1. `schema_version` — the string `1`
2. `checkpoint_id` — globally unique identifier
3. `mission_id` — identifier of the durable mission
4. `sequence` — zero-based contiguous integer
5. `previous_hash` — previous checkpoint hash, or 64 zeroes at genesis
6. `timestamp` — UTC RFC 3339 timestamp
7. `agent` — durable agent name declared by the mission
8. `source` — provenance mode and adapter for resumable state
9. `objective_hash` — SHA-256 of the mission objective's UTF-8 bytes
10. `run` — anchored `run_id`, `entry_count`, and `chain_head`
11. `summary` — required resumable state written at the checkpoint
12. `commitments` — optional ordered stable obligations with ID, description,
    status, acceptance criteria, and evidence references; omitted when empty
13. `next_actions` — ordered pending actions
14. `blockers` — ordered conditions preventing progress
15. `evidence` — event IDs from the anchored run prefix, with optional notes
16. `extensions` — namespaced extension data

Checkpoint source defaults to `reported` with adapter `continuity`. Objects use
provenance v1 canonical JSON. Arrays retain order. The
`checkpoint_hash` annotation is excluded from hashed bytes. Evidence references
must resolve inside the run prefix named by the checkpoint.

Commitment status is `pending`, `blocked`, or `completed`. IDs are unique per
checkpoint. Once declared, a commitment's description and acceptance criteria
are immutable, completed status is terminal, and every later checkpoint must
retain the commitment. Commitment evidence must also resolve inside the
checkpoint's anchored run prefix.

## Checkpoint chain

```text
genesis = 0000000000000000000000000000000000000000000000000000000000000000

checkpoint[0] = sha256(canonical checkpoint[0])
checkpoint[1] = sha256(canonical checkpoint[1])
...
mission chain head = final checkpoint hash
```

Every checkpoint carries the previous checkpoint hash. Verification checks
genesis, contiguous sequences, mission and agent identity, objective hash,
every hash link, commitment definitions and terminal transitions, checkpoint
count, and declared mission chain head.

## Run anchors

A run anchor freezes one provenance proof at a specific `entry_count` and
`chain_head`. Later events may be appended to the same active run without
invalidating an earlier checkpoint. Verification uses only events with sequence
numbers below the anchored entry count.

## Portable bundle

`chainproof.continuity.bundle.v1` contains:

- mission manifest
- ordered canonical checkpoints
- one provenance v1 run proof per checkpoint, in checkpoint order

Offline verification checks both chains and requires every bundled run proof
to match its checkpoint's run ID, entry count, and chain head exactly.

## Derived context envelope

`chainproof context` emits schema version `1` with source adapter
`context-compiler` and mode `derived`. It contains the mission, latest
checkpoint, continuity verification result, and a bounded list of canonical
events cited by that checkpoint. Evidence is ordered by first citation,
deduplicated by event ID, and annotated with `evidence_truncated` when capped.
The envelope is a rebuildable view, not canonical proof material, and must not
be emitted when mission or run-anchor verification fails.

## Proof boundary

A valid continuity bundle proves that checkpoint bytes and anchored run-proof
bytes are internally continuous relative to the declared heads. It does not
prove that a reported or imported claim was true, that the named agent has a
cryptographic identity, that an objective was achieved, or that the complete
bundle existed before a verifier first learned its chain head. Signing and
external anchoring require a future version.
