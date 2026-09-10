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
sufficient.

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
