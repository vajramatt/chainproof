# ChainProof Investigation Protocol v1

`chainproof.investigation.v1` defines local, machine-readable evidence search
and inspection. Search results come from a rebuildable derived index. Event
inspection returns canonical proof material. Consumers MUST preserve this
boundary.

## Structured search

```sh
chainproof search [--run ID] [--agent NAME] [--kind KIND] [--tool TOOL] \
  [--status STATUS] [--mode MODE] [--limit N] [QUERY]
```

Flags precede optional free text. At least one filter or non-empty query is
required. `--limit` accepts 1 through 500. `--mode` accepts `observed`,
`reported`, `imported`, or `derived`. Filters combine with AND.

Successful output contains:

- `schema_version`: string `1`
- `query`: normalized filters and free text used for lookup
- `hits`: bounded summary records linking to canonical `event_id` values
- `total`: all matches before limit
- `facets`: counts for agents, kinds, tools, statuses, and provenance modes

Search hits are navigation data, not evidence. Consumers SHOULD follow a hit
with canonical inspection before citing its payload or proof fields.

## Canonical event inspection

```sh
chainproof inspect event EVENT_ID
```

Output is canonical provenance event described by `provenance-v1.md`, including
`previous_hash` and stored `event_hash`. Command reads SQLite ledger directly;
no HTTP service is required.

## Verified run inspection

```sh
chainproof inspect run RUN_ID
```

Successful output contains schema version `1`, canonical run record, proof
verification, optional parent run, and child runs. Parent and child relations
come from run metadata and are not part of event hash chain. Invalid proof is
emitted with `verification.valid: false` and command fails verification exit
class.

## Trust boundary

SQLite search index can be deleted and rebuilt from canonical events without
changing proof. Search order, summaries, totals, and facets therefore MUST NOT
be presented as hash-chain evidence. Canonical event bytes and verification
result remain authority. Protocol adds no authentication; configured ledger
and loopback-only service keep existing local OS-user trust boundary.
