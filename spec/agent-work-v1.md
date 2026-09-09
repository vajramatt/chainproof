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

The context file is created with owner-only mode `0600` and is removed when the
wrapper returns after process exit. Forced wrapper termination can prevent
cleanup and leave the file in the operating system temporary directory. It uses
the `MissionContext` schema documented in
[continuity-v1.md](continuity-v1.md). Harness integrations should read it before
beginning work.

Context is data, not an automatically injected prompt. This prevents a generic
wrapper from guessing harness syntax, preserves exact JSON proof boundaries,
and lets each integration decide how to place context in its model input.

## Recovery rule

When `has_uncheckpointed_work` is true, the latest checkpoint remains the
trusted resume base. Entries in `uncheckpointed_work` identify newer verified
run tails for inspection. A harness must not treat those event payloads as
accepted mission state until it reconciles them and writes another checkpoint.

## Checkpoint rule

Before ending useful work, an agent should submit a checkpoint using the IDs
from its environment:

```sh
chainproof checkpoint "$CHAINPROOF_MISSION_ID" "$CHAINPROOF_RUN_ID" CHECKPOINT_JSON
```

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

Protocol v1 assumes one local user and cooperative harnesses. Environment
variables are discovery hints, not cryptographic agent identity. Future
versions may add mission leases, signed agent identity, and durable context-file
handoff for remote executors without changing continuity checkpoint hashes.
