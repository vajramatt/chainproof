# ChainProof Provenance Format v1

ChainProof records what an agent, harness, adapter, or observer reports. A valid
chain establishes continuity of the recorded bytes. It does not establish that
the report was truthful or complete.

## Canonical event

Every hashed event contains exactly these fields, in this order:

1. `schema_version` — the string `1`
2. `event_id` — globally unique identifier
3. `run_id` — identifier of the containing run
4. `sequence` — zero-based contiguous integer
5. `previous_hash` — 64 lowercase hexadecimal characters
6. `timestamp` — UTC RFC 3339 timestamp
7. `kind` — stable or extension event name
8. `actor` — JSON object
9. `source` — JSON object including `mode` and adapter identity
10. `payload` — JSON value
11. `artifacts` — JSON array
12. `extensions` — JSON object

Objects are recursively sorted by Unicode code-point key order. Arrays retain
their order. Undefined values are rejected. The canonical UTF-8 JSON bytes are
hashed with SHA-256. The first event's `previous_hash` is 64 zeroes.

Numbers: an integer literal (optional `-` followed by digits only) is emitted
exactly as written, with no float conversion, so integers beyond 2^53 are
hashed precisely. Any number with a fraction or exponent is emitted as the
shortest decimal that round-trips the IEEE 754 binary64 value, using exponent
form (`1e+21`, `1e-7`) at magnitudes of 1e21 and above or below 1e-6. This
equals Go `encoding/json` float64 output, which every v1 chain already uses.

## Bundle parsing

A verifier MUST parse a proof bundle so that every reader sees the same
document. It MUST reject a bundle that contains:

- duplicate keys in any object, including payload, metadata, and extensions;
- a key in a schema object (bundle, run, event, actor, source, checkpoint) that
  is not a declared field name, including keys that differ only by case;
- a number whose text differs from its canonical form above (`1.0`, `1E2`);
- data after the top-level JSON value.

It MUST keep numbers in free-form values exact rather than converting them to
binary64 before hashing.

## Collection modes

- `observed`: captured by a wrapper or direct observer as execution occurred.
- `reported`: submitted by an agent or harness.
- `imported`: collected after the fact from another record.
- `derived`: calculated from one or more recorded events.

Collection mode describes provenance quality; it is not a trust score.

## Verification

A verifier MUST check genesis, contiguous sequence numbers, run identity,
previous-hash links, every event hash, declared entry count, and declared chain
head. Verifying only the hashes present in a supplied suffix or prefix is not
sufficient. A bundle's declared head and count cannot reveal tail truncation;
a verifier that holds a trusted head or count from outside the bundle SHOULD
require the bundle to match it.

### Run declaration boundary

The bundle `run` object is a declaration, not hashed evidence. Version 1 binds
only `run_id` (every event repeats it), `entry_count`, `chain_head`, and the
`chainproof.agent.v1` metadata extension (it must match event extensions). The
fields `agent`, `harness`, `model`, `status`, `started_at`, `completed_at`, and
every other `metadata` key, including `mission_id`, are covered by no hash and
can be edited without breaking verification. A verifier MUST NOT present them
as verified; ChainProof reports them as `unauthenticated_run_fields` on every
valid result. Use event content (actor, timestamps, payloads), which is hashed,
when those facts matter. Mission membership is authenticated only through
continuity checkpoints, whose run anchors are hashed.

## Agent attribution extension

Run metadata and event extensions MAY contain `chainproof.agent.v1`. The value
contains public local attribution such as stable key-derived `agent_id`,
`display_name`, profile, public key, optional mission role, and optional
ephemeral `worker_id`.

When run metadata contains this extension and run has one or more events, at
least one event MUST contain canonically identical extension value. Every event
that contains extension MUST match run metadata. An event extension without
matching run metadata is invalid. A verifier MUST also confirm that `agent_id`
is the SHA-256 fingerprint of the declared Ed25519 public key and validate
required field and worker-ID shapes. ChainProof copies run attribution into
each new event and rejects caller attempts to replace the reserved extension.

Because extension lives inside canonical event, changing bound attribution
breaks event hash or metadata-to-event comparison. This is tamper-evident
attribution, not signature, authentication, proof of key possession, or proof
that attributed agent produced report.
