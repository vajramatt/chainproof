# ChainProof Integration Guide Format v1

`chainproof.integration-guide.v1` is side-effect-free, agent-readable lifecycle
guidance bundled inside ChainProof binary. It describes current integration
behavior; it is not executable policy or proof evidence.

Each guide contains:

- schema and format version
- stable harness ID, readable name, integration mode, and implementation status
- canonical first-party source URL
- ordered lifecycle steps with phase, command template, and purpose
- relevant `CHAINPROOF_*` environment variables
- explicit provenance modes and proof boundary
- current limitations

`chainproof integration list` emits sorted summary records.
`chainproof integration show HARNESS` emits one complete guide. Both commands
MUST operate without opening or creating ledger, identity, lease, or network
state.

Command templates use uppercase placeholders such as `MISSION_ID`, `RUN_ID`,
`EVENT_ID`, `QUERY`, `CHECKPOINT_JSON`, `DIRECTORY`, and `COMMAND`. Consumers
replace placeholders; they MUST NOT send templates directly to shell without
argument-safe substitution.

Every bundled lifecycle includes derived evidence search followed by canonical
event inspection. A search hit is not proof evidence until consumer loads its
canonical event and retains provenance and hash fields.

Guide status describes shipped integration surface:

- `built_in`: behavior is implemented in ChainProof binary
- `included`: repository contains integration package
- `generic_available`: built-in generic protocol supports harness, without a
  harness-specific native runner

Guide provenance labels retain meanings from provenance v1. Observed process
lifecycle does not make hook reports observed. Imported histories remain
imported. Guide output does not authenticate harness or agent.
