# ChainProof Agent Work Protocol v1

Agent Work Protocol v1 gives any local harness the same mission bootstrap
contract without teaching ChainProof how that harness accepts prompts.

## Invocation

```sh
chainproof run --mission MISSION_ID -- COMMAND [ARGS...]
```

Before starting the command, ChainProof:

1. loads the active mission;
2. verifies mission continuity and every anchored run prefix;
3. compiles bounded context, including uncheckpointed-work warnings;
4. creates a new mission-associated provenance run; and
5. starts the command with the environment below.

Invalid continuity stops invocation before a child process starts.

## Child environment

| Variable | Meaning |
| --- | --- |
| `CHAINPROOF_MISSION_ID` | durable mission identifier |
| `CHAINPROOF_RUN_ID` | provenance run created for this invocation |
| `CHAINPROOF_CONTEXT_FILE` | absolute path to compiled context JSON |
| `CHAINPROOF_LEASE_ID` | optional current mission lease token supplied by native integrations |
| `CHAINPROOF_AGENT_ID` | stable key-derived local agent identifier |
| `CHAINPROOF_AGENT_NAME` | readable profile display name |
| `CHAINPROOF_AGENT_PROFILE` | selected local profile name |
| `CHAINPROOF_AGENT_ROLE` | optional role for this agent within current mission |
| `CHAINPROOF_WORKER_ID` | ephemeral identifier unique to this wrapped process |

The context file is created with owner-only mode `0600` and is removed when the
wrapper returns after process exit. Forced wrapper termination can prevent
cleanup and leave the file in the operating system temporary directory. It uses
the `MissionContext` schema documented in
[continuity-v1.md](continuity-v1.md). Harness integrations should read it before
beginning work.

Compiled context may include an active mission lease. Harness integrations
should not begin competing work when `lease_active` names another holder.
Lease coordination is local control state, not proof of agent identity. Native
claims default holder to `CHAINPROOF_AGENT_ID`; explicit holder strings remain
supported.

Inside a wrapped process, `chainproof context` uses
`CHAINPROOF_MISSION_ID` when `--mission` is omitted. Explicit flags still take
precedence.

Context is data, not an automatically injected prompt. This prevents a generic
wrapper from guessing harness syntax, preserves exact JSON proof boundaries,
and lets each integration decide how to place context in its model input.

## Native Codex profile

```sh
chainproof codex work [--mission MISSION_ID | --acquire] [--holder HOLDER] [--lease-ttl 30m] [--exec] [--prompt TEXT] -- [CODEX_OPTIONS]
```

This profile uses the generic child environment and injects a first-line
linkage marker into Codex's initial prompt:

```text
CHAINPROOF_AGENT_WORK_V1 {"mission_id":"MISSION_ID","parent_run_id":"RUN_ID"}
```

`parent_run_id` identifies the observed wrapper run. The Codex collector may
attach its imported native-session run to the mission only after confirming
that the parent exists, uses harness `codex`, and has the same `mission_id`.
Marker text alone is insufficient to create a linkage.

```text
ChainProof wrapper              Codex                    Codex collector
      |                           |                            |
      |-- verified context env -->|                            |
      |-- protocol prompt ------->|                            |
      |<-- observed exit ----------|                            |
      |                                                        |
      |                 local session JSONL ------------------>|
      |<======== parent_run_id + mission_id validated =========|
```

Wrapper run proves observed process lifecycle. Collector run proves continuity
of imported Codex records. Linkage preserves both provenance modes; it does not
merge events, deduplicate claims, or make imported evidence observed.

Wrapper run metadata and canonical events contain matching
`chainproof.agent.v1` attribution. Validated imported child run inherits that
attribution when marker is accepted. Attribution is hash-bound but unsigned.

Native Codex profile verifies continuity before atomically claiming mission
lease. It renews at half-TTL intervals and releases only when same lease token
still owns mission. A changed token indicates explicit handoff and is preserved.
Lease ID and holder are recorded in wrapper-run metadata; this linkage remains
local coordination state, not canonical proof material.

With `--acquire`, profile atomically chooses oldest available active mission,
verifies continuity, and claims lease before launch. It skips live leases and
can reclaim expired ones. Returned context includes claimed token. Explicit
`--mission` and `--acquire` cannot be combined.

## Recovery rule

When `has_uncheckpointed_work` is true, the latest checkpoint remains the
trusted resume base. Entries in `uncheckpointed_work` identify newer verified
run tails for inspection. A harness must not treat those event payloads as
accepted mission state until it reconciles them and writes another checkpoint.

## Checkpoint rule

Before ending useful work, an agent should submit a checkpoint against its
environment-bound mission and run:

```sh
chainproof checkpoint --current CHECKPOINT_JSON
```

`--current` reads `CHAINPROOF_MISSION_ID` and `CHAINPROOF_RUN_ID`. Explicit
mission and run arguments remain available for recovery or external tooling.

Checkpoint JSON should include summary, complete commitment snapshot, next
actions, blockers, and evidence references from the current anchored run
prefix. ChainProof verifies these constraints before append.

The generic wrapper does not invent a checkpoint from process exit. Exit status
is observed evidence, not a reliable semantic account of mission state.

## Process lifecycle

ChainProof appends observed `run.started` and `run.completed` events around the
child process and closes the run using its exit status. Native integrations may
add richer reported or observed evidence to `CHAINPROOF_RUN_ID` during work.

## Design boundary

Protocol v1 assumes one local user and cooperative harnesses. Agent ID is
derived from local Ed25519 public key, but environment variables and current
metadata are attribution hints, not authentication or signed proof of key
possession. Future versions may add multi-host lease coordination, signed agent
attestations, key recovery, and durable context-file handoff for remote
executors without changing continuity checkpoint hashes.
