# Agent bootstrap

Current `main` exposes a three-command machine bootstrap. These commands are
newer than release `v0.6.0`.

```sh
chainproof capabilities --json
chainproof init --json
chainproof doctor --json
```

Use `CHAINPROOF_DB`, `CHAINPROOF_AGENT_HOME`, and
`CHAINPROOF_AGENT_PROFILE` before calling them when an instance must live
outside `~/.chainproof/`.

## Discover capabilities

`chainproof capabilities --json` reports only shipped behavior. It resolves
configuration but does not create directories, a database, or an identity.

Top-level fields:

- `schema_version`: capability document version, currently `1`
- `product`: `chainproof`
- `version`: release version or `development` for an untagged source build
- `platform`: current `os` and `arch`
- `paths`: resolved `ledger` and `agent_home`
- `network`: fixed local API URL, `loopback` listen scope, and `none`
  authentication mode
- `protocols`: exact provenance, continuity, Agent Work, and identity format IDs
- `features`: sorted identifiers for behavior compiled into this binary

Capability output never claims planned behavior. In particular, signed
attestations, key recovery, portable mission import, and multi-host
coordination are absent until implemented.

## Initialize state

`chainproof init --json` opens or creates configured SQLite ledger, applies
current schema, creates or loads selected agent profile, and returns:

```json
{
  "schema_version": "1",
  "status": "ready",
  "ledger": "/resolved/path/chainproof.db",
  "agent": {
    "schema_version": "1",
    "profile": "default",
    "agent_id": "agent:ed25519:...",
    "display_name": "agent-...",
    "public_key": "...",
    "created_at": "..."
  }
}
```

Repeated calls preserve `agent_id`, public key, and existing display name.
Private-key material never appears in output. Initialization may repair ledger,
profile, and key permissions to owner-only modes on supported POSIX platforms.

## Diagnose state

`chainproof doctor --json` performs read-only diagnosis and does not initialize
missing state. Report fields:

- `schema_version`: diagnostic document version, currently `1`
- `status`: `ready`, `uninitialized`, or `attention`
- `paths`: resolved ledger and agent-profile roots
- `checks`: named results for `platform`, `ledger`, `ledger_permissions`,
  `agent_identity`, and `loopback_api`

Check status values:

- `pass`: check succeeded
- `missing`: initialization or identity material is absent
- `warn`: usable state violates a recommended safety condition
- `fail`: corruption, mismatch, unsupported platform, or unreadable state
- `inactive`: optional runtime component is stopped
- `not_applicable`: check does not apply on this platform or state

Ledger diagnosis opens SQLite read-only, runs `PRAGMA quick_check`, and confirms
core ChainProof tables. Identity diagnosis validates public profile fingerprint
and confirms matching local private key without creating either file. Stopped
loopback API reports `inactive` and does not make otherwise healthy state fail.

Until stable exit codes land, consumers must treat JSON `status` and individual
check statuses as authoritative. Structured error output and stable nonzero exit
codes remain release gates.

## Security boundary

Machine bootstrap does not add authentication. Local HTTP remains loopback-only
and assumes cooperative processes under one operating-system user. Stable agent
IDs are local attribution; current events and checkpoints are unsigned and do
not prove private-key possession.
