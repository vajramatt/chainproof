# Investigating provenance

ChainProof stores two related representations in the same local SQLite file:

1. **Canonical ledger events** are append-only, hash-chained, exportable, and
   independently verifiable.
2. **The provenance index** is a derived projection used for search and facets.
   It is not evidence and is automatically backfilled from ledger events.

This distinction lets the interface be fast without pretending that a search
index is authoritative. Opening an older database creates the index and
backfills any missing rows; canonical events and chain heads are untouched.

## Indexed evidence

Each event projects these fields:

- run ID, sequence, and timestamp
- agent, harness, and model
- event kind
- collection mode: `observed`, `reported`, `imported`, or `derived`
- tool and outcome/status when present
- a compact evidence summary
- flattened payload keys and scalar values for local search

The active content policy still applies. In the default Codex `hashes` mode,
message bodies, commands, output, and change details are represented by their
SHA-256 digest and byte count. The index sees the digest, not the omitted body.

## Local API

```text
GET /api/search?q=failed&agent=chainproof&kind=tool.result&tool=shell&status=failed&mode=imported&limit=100
GET /api/events/{event_id}
GET /api/runs/{run_id}/lineage
```

All parameters are optional. Filters combine with AND. The response contains
matching hits, total count, and facets for agents, kinds, tools, statuses, and
collection modes. The event endpoint returns the canonical event, including
its previous hash and stored event hash.

Run metadata may include `parent_run_id` (or the legacy `parent_chain_id`). The
lineage endpoint resolves that relationship in both directions without making
vendor-specific assumptions about how a harness names subagents.

The API remains bound to loopback by default and has no multi-user
authentication. Do not expose it to a network.
