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
- `protocols`: exact provenance, continuity, mission workspace, integration
  guide, Agent Work, and identity format IDs
- `features`: sorted identifiers for behavior compiled into this binary

Capability output never claims planned behavior. Current `main` advertises
`mission_import`, `mission_workspaces`, and `integration_guides`; signed
attestations, key recovery, and multi-host coordination remain absent until
implemented.

`chainproof integration list` and `chainproof integration show HARNESS` are
also side-effect-free. They return bundled lifecycle profiles for `codex`,
`claude-code`, `openclaw`, and `generic` using
`chainproof.integration-guide.v1`.

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

`agent_identity.code` is stable for autonomous branching:

- `identity_uninitialized`: both identity files are absent
- `identity_ready`: profile is valid and matching key is present
- `identity_incomplete`: exactly one established identity file is absent
- `identity_invalid`: material is unreadable, corrupt, or mismatched

Check status values:

- `pass`: check succeeded
- `missing`: both profile and private key are absent, so identity is
  uninitialized
- `warn`: usable state violates a recommended safety condition
- `fail`: corruption, mismatch, unsupported platform, or unreadable state
- `inactive`: optional runtime component is stopped
- `not_applicable`: check does not apply on this platform or state

Ledger diagnosis opens SQLite read-only, runs `PRAGMA quick_check`, and confirms
core ChainProof tables. Identity diagnosis validates public profile fingerprint
and confirms matching local private key without creating either file. A lone
profile, lone key, corrupt file, or key mismatch reports `fail`; diagnostics
and later `ensure` calls do not generate replacement identity material. Stopped
loopback API reports `inactive` and does not make otherwise healthy state fail.

Doctor health is data rather than command failure. Consumers must treat report
`status` and individual check statuses as authoritative; a successfully emitted
report exits `0` even when status is `uninitialized` or `attention`.

## Structured failures

Commands containing `--json` automatically emit failures as one JSON document
on stderr. Pass global `--json-errors` anywhere before a child-command `--`
separator for same contract; flag is removed before command dispatch and does
not change successful output or child arguments.

```json
{
  "schema_version": "1",
  "error": {
    "code": "usage",
    "message": "unknown command \"inspectt\"",
    "exit_code": 2
  }
}
```

Stable exit classes:

- `0`: command executed and produced its result
- `1`, `command_failed`: command could not complete
- `1`, `identity_incomplete`: one established identity file is missing; no
  replacement was generated
- `1`, `backup_invalid`: backup manifest, file hashes, ledger, or identities
  failed verification
- `1`, `destination_exists`: backup or restore refused to overwrite a path
- `1`, `mission_import_collision`: imported mission, run, event, or checkpoint
  ID already exists; destination remains unchanged
- `2`, `usage`: arguments or command name are invalid
- `3`, `verification_failed`: proof verification rejected input
- `3`, `mission_import_invalid`: continuity import failed format, chain, run
  prefix, or record validation before mutation
- `3`, `mission_workspace_invalid`: workspace manifest, file inventory,
  checksum, proof, derived view, or artifact validation failed before mutation

Flag parsing is quiet, so structured stderr is never prefixed by parser prose.
Unknown commands are rejected before database or identity initialization.

## Back up and restore without overwriting state

`chainproof backup BACKUP_DIR` emits schema `1` JSON with status `backed_up`,
the absolute backup path, and a `chainproof.backup.v1` manifest. Snapshot
creation remains consistent while independent writers append to the WAL
ledger. It includes every valid local identity, including private keys.

`chainproof restore BACKUP_DIR NEW_INSTANCE_DIR` verifies every declared file
hash, SQLite integrity, every run and mission proof chain, content-addressed
artifacts, and profile/key pairing before it emits status `restored`. It copies
only manifest-declared files and refuses any existing destination. Returned
`ledger` and `agent_home` fields can be assigned to `CHAINPROOF_DB` and
`CHAINPROOF_AGENT_HOME`; restore never switches or overwrites the running
instance automatically.

## Import verified mission continuity

`chainproof mission import PROOF.json` verifies a
`chainproof.continuity.bundle.v1` document, rejects conflicting proofs for same
run, then inserts canonical mission, checkpoint, run, and event records in one
transaction. Search rows are rebuilt as derived state. Import preserves source
mission status and imports no lease, profile, private key, or artifact body.
Every destination identifier must be unused; any collision aborts whole import.

## Export and import a mission workspace

```sh
chainproof mission workspace export MISSION_ID DIRECTORY
chainproof mission workspace verify DIRECTORY
chainproof mission workspace import DIRECTORY
```

Workspace format `chainproof.mission-workspace.v1` carries continuity proof,
deterministic event JSONL and Markdown projections, checksum manifest, and every
referenced content-addressed artifact body. Offline verification creates no
ledger or identity. Import verifies complete package, then commits mission and
artifact records in one SQLite transaction. Profiles, private keys, leases, and
lease history never enter workspace.

## Keep configured state across login

Current `main` makes `chainproof service install` snapshot resolved local state
and collector configuration into the macOS LaunchAgent or Linux systemd user
unit. It persists this fixed allowlist:

- `CHAINPROOF_DB`
- `CHAINPROOF_AGENT_HOME`
- `CHAINPROOF_AGENT_PROFILE`
- `CHAINPROOF_CODEX_ROOT` when configured
- `CHAINPROOF_CODEX_CONTENT` when configured
- `CHAINPROOF_CODEX_DISABLED` when configured

State and collector paths become absolute before installation. Daemon output
goes to `daemon.log` beside configured database. Re-run service installation
after changing configuration. ChainProof does not copy ambient variables,
tokens, or credentials into service definition.

## Security boundary

Machine bootstrap does not add authentication. Local HTTP remains loopback-only
and assumes cooperative processes under one operating-system user. Stable agent
IDs are local attribution; current events and checkpoints are unsigned and do
not prove private-key possession.
