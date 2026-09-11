# Investigating provenance

ChainProof stores three related representations in the same local SQLite file:

1. **Canonical ledger events** are append-only, hash-chained, exportable, and
   independently verifiable.
2. **Mission checkpoints** form a separate append-only continuity chain. Each
   checkpoint anchors an exact prefix of one canonical run proof and carries
   resumable agent state.
3. **The provenance index** is a derived projection used for search and facets.
   It is not evidence and is automatically backfilled from ledger events.

This distinction lets the interface be fast without pretending that a search
index is authoritative. Opening an older database creates the index and
backfills any missing rows; canonical events and chain heads are untouched.

## SQLite and portable files

SQLite is the operational store, not a network dependency. It gives concurrent
local agents atomic appends, serialized lease changes, indexes, and consistent
queries. Canonical events remain independently verifiable because proof bundles
carry structured run and event records plus chain heads; verification does not
require original database or running ChainProof instance.

Current `main` exercises that claim with independent OS processes, not only
goroutines. Concurrent appenders and checkpoint writers retry full transactions
and retain every record in valid chains. Concurrent mission claims, queue
acquisitions, handoffs, renewals, and releases retry from fresh state and return
semantic ownership or queue results; lease history remains serialized.
Concurrent recovery of an expired lease produces one new owner linked to the
abandoned lease. A forced process kill during an uncommitted event or checkpoint
rolls back every partial row and manifest update through SQLite WAL recovery;
the next event or checkpoint then appends and verifies normally.

Markdown is useful as generated mission context or human-readable summary, but
is not suitable as canonical coordination state: parsing is ambiguous, atomic
multi-agent updates are difficult, and schema evolution is fragile. Current
`main` can verify and atomically import continuity JSON into another SQLite
instance while rebuilding search rows. It also exports and verifies first-class
portable mission workspaces with artifact files, checksum manifests,
deterministic JSONL projections, and generated Markdown views. Import commits
canonical proof rows, derived search rows, and artifact bodies atomically.

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

The API accepts only loopback listen addresses and has no multi-user
authentication. Non-loopback binding is rejected before the server starts.
